"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { RecordList, RecordListSkeleton, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { DateTimeField } from "@/components/workbench/dateTimeField";
import { DetailDrawer } from "@/components/workbench/detailDrawer";
import { Button as PainuoButton, SegmentedControl, Textarea } from "@/vendor/painuo/react/v1/src/index";
import { Button } from "@/components/ui/button";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { postJson } from "@/lib/fetchJson";
import { leadRowFacts } from "@/lib/finish";
import { eventLine, humanTime } from "@/lib/humanize";
import { persistListMemory, readListMemory, recallListState, rememberListState } from "@/lib/listMemory";
import { NARROW_ACTIONS, deskState, sourceText, type StatusFacts } from "@/lib/workbench";
import { MISSING_SCOPE, acceptLeadRows, failureText, listTenantHeader, pagePrimary, productError } from "@/lib/productShell";

// 原生线索页（HUI-1680）。只打本站 BFF。租户由工作区选择，服务端再校验成员身份。
// 未分配的线索明确写「待分配」，不把空负责人伪装成已分派。
// HUI-2626 finish-r1（票面 A/B 组）：状态筛选 + 抽屉详情统一模式。
// 服务端 /api/v1/leads 无查询参数，返回当前范围全量（owner 全量、销售只看名下），
// 状态筛选是对这份全量结果的客户端过滤，文案如实标注，不冒充服务端行为。

interface LeadRow {
  id: string;
  contact_id: string;
  status: string;
  assigned_member_id?: string;
  created_by?: string;
  // HUI-2598 D1/D6：服务端列表投影。姓名/来源/负责人一次返回，页面不再二次拉联系人。
  contact_name?: string;
  source_type?: string;
  owner_label?: string;
  assignment_reason?: string;
  source?: { form?: string; activity?: string; channel?: string; at?: string; source_app?: string };
}

interface TimelineEvent {
  at: string;
  kind: string;
  summary: string;
}

interface DrawerTimeline {
  source?: { form?: string; activity?: string; channel?: string };
  statuses?: StatusFacts;
  next?: { label?: string; at?: string };
  events?: TimelineEvent[];
  owner_label?: string;
  assignment_reason?: string;
  message?: string;
  error?: string;
}

const STATUS_FILTERS = [
  { value: "all", label: "全部" },
  { value: "new", label: "新线索" },
  { value: "in_progress", label: "跟进中" },
  { value: "converted", label: "已转化" },
  { value: "closed", label: "已关闭" },
  { value: "filtered", label: "已过滤" },
];

export default function LeadsPage() {
  const scope = useCrmScope();
  const width = useShellWidth();
  const [rows, setRows] = useState<Array<LeadRow & { name: string }> | null>(null);
  const [error, setError] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const [reload, setReload] = useState(0);
  const gen = useRef({ seq: 0, tenant: "" });
  const shownTenant = useRef<string | null>(null);
  const pendingScrollY = useRef<number | null>(null);

  // 详情抽屉（真实 timeline + 票号：切范围/迟到响应一律丢弃；抽屉内记跟进失败保留输入）。
  const [drawerId, setDrawerId] = useState<string | null>(null);
  const [drawerTitle, setDrawerTitle] = useState("");
  const [drawerBody, setDrawerBody] = useState<DrawerTimeline | null>(null);
  const [drawerBusy, setDrawerBusy] = useState(false);
  const [drawerErr, setDrawerErr] = useState("");
  const [drawerNote, setDrawerNote] = useState("");
  const [drawerNextAt, setDrawerNextAt] = useState("");
  const [drawerSaving, setDrawerSaving] = useState(false);
  const [drawerMsg, setDrawerMsg] = useState("");
  const drawerGen = useRef({ seq: 0, tenant: "" });

  // 只有切换工作范围时重置筛选与抽屉，并按租户恢复列表记忆（切租户绝不串状态）。
  useEffect(() => {
    const tenant = scope.tenant ?? "";
    if (shownTenant.current === tenant) return;
    shownTenant.current = tenant;
    const restored = recallListState(readListMemory(), tenant, "leads");
    setStatusFilter(restored.filter || "all");
    pendingScrollY.current = typeof restored.scrollY === "number" ? restored.scrollY : null;
    setDrawerId(null);
    setDrawerBody(null);
    setDrawerErr("");
    setDrawerNote("");
    setDrawerNextAt("");
    setDrawerMsg("");
    setRows(tenant ? null : []);
  }, [scope.tenant]);

  // 记住当前租户的筛选与滚动，返回列表时恢复原位。
  const saveListPosition = (nextFilter = statusFilter) => {
    const tenant = scope.tenant ?? "";
    if (!tenant) return;
    const scrollY = typeof window !== "undefined" ? window.scrollY : 0;
    persistListMemory(rememberListState(readListMemory(), tenant, "leads", { filter: nextFilter, scrollY }));
  };

  useEffect(() => {
    if (rows === null || pendingScrollY.current === null) return;
    const targetY = pendingScrollY.current;
    pendingScrollY.current = null;
    if (targetY > 0 && typeof window !== "undefined") {
      window.scrollTo(0, targetY);
    }
  }, [rows]);

  useEffect(() => {
    const tenant = scope.tenant ?? "";
    const ticket = { seq: gen.current.seq + 1, tenant: tenant };
    gen.current = ticket;
    setError("");
    if (!tenant) {
      setRows([]);
      return;
    }
    let cancelled = false;
    void (async () => {
      const headers = listTenantHeader(tenant);
      if (!headers) {
        if (!cancelled && acceptLeadRows(gen.current, ticket, []) !== null) setRows([]);
        return;
      }
      try {
        const leadRes = await fetch("/api/leads", { headers });
        const leadsBody = await leadRes.json();
        if (!leadRes.ok) {
          if (cancelled || acceptLeadRows(gen.current, ticket, []) === null) return;
          setError(failureText(leadsBody, leadRes.status));
          setRows([]);
          return;
        }
        const items = ((leadsBody.items ?? []) as LeadRow[]).map((lead) => ({
          ...lead,
          // 联系人缺失时如实说“待同步”，绝不伪造一个可读姓名。
          name: lead.contact_name || "未填写称呼",
        }));
        const accepted = acceptLeadRows(gen.current, ticket, items);
        if (cancelled || accepted === null) return;
        setRows(accepted);
      } catch (e) {
        if (cancelled || acceptLeadRows(gen.current, ticket, []) === null) return;
        setError(productError((e as Error).message));
        setRows([]);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [scope.epoch, scope.tenant, reload]);

  const loadDrawerTimeline = (id: string) => {
    const tenant = scope.tenant ?? "";
    const ticket = { seq: drawerGen.current.seq + 1, tenant };
    drawerGen.current = ticket;
    setDrawerErr("");
    setDrawerBusy(true);
    void (async () => {
      try {
        const res = await fetch(`/api/leads/${id}/timeline`, { headers: listTenantHeader(tenant) ?? {} });
        const body = (await res.json()) as DrawerTimeline;
        if (drawerGen.current !== ticket || scope.tenant !== ticket.tenant) return;
        if (!res.ok) {
          setDrawerErr(failureText(body, res.status));
          return;
        }
        setDrawerBody(body);
      } catch (e) {
        if (drawerGen.current === ticket) setDrawerErr(productError((e as Error).message));
      } finally {
        if (drawerGen.current === ticket) setDrawerBusy(false);
      }
    })();
  };

  const openDrawer = (id: string) => {
    saveListPosition();
    setDrawerId(id);
    setDrawerTitle(rows?.find((r) => r.id === id)?.name || "线索详情");
    setDrawerBody(null);
    setDrawerNote("");
    setDrawerNextAt("");
    setDrawerMsg("");
    loadDrawerTimeline(id);
  };

  const closeDrawer = () => {
    drawerGen.current = { seq: drawerGen.current.seq + 1, tenant: scope.tenant ?? "" };
    setDrawerId(null);
    setDrawerBody(null);
    setDrawerErr("");
    setDrawerNote("");
    setDrawerNextAt("");
    setDrawerMsg("");
  };

  const submitDrawerFollow = async (mode: "note" | "schedule") => {
    const tenant = scope.tenant ?? "";
    const headers = listTenantHeader(tenant);
    if (!headers || !drawerId || drawerSaving) return;
    const note = drawerNote.trim();
    if (!note) return;
    if (mode === "schedule" && !drawerNextAt) return;
    setDrawerSaving(true);
    setDrawerMsg("");
    const payload: Record<string, unknown> = { note, complete: true };
    if (drawerNextAt) {
      payload.next_follow_up_at = new Date(drawerNextAt).toISOString().replace(/\.\d{3}Z$/, "Z");
    }
    try {
      const out = await postJson<{ next?: { label?: string } }>(
        `/api/leads/${drawerId}/follow-through`,
        { "content-type": "application/json", ...headers },
        payload,
      );
      if (!out.ok) {
        // 失败保留用户输入，不清空 drawerNote/drawerNextAt。
        setDrawerMsg(failureText(out.body, out.status));
        return;
      }
      setDrawerNote("");
      setDrawerNextAt("");
      setDrawerMsg(out.body.next?.label ?? "已记下跟进");
      loadDrawerTimeline(drawerId);
      setReload((n) => n + 1);
    } catch (e) {
      setDrawerMsg(productError((e as Error).message));
    } finally {
      setDrawerSaving(false);
    }
  };

  const visibleRows = (rows ?? []).filter((row) => statusFilter === "all" || row.status === statusFilter);
  const firstFollowable = (rows ?? []).find((row) => row.status === "new" || row.status === "in_progress");

  return (
    <main data-page="leads" data-pattern="list">
      <header className="page-head" data-region="header">
        <h1>线索</h1>
        <Button size="sm" data-page-primary="true" data-region="primary_action" asChild>
          <Link href={firstFollowable ? `/leads/${firstFollowable.id}` : "/"}>
            {pagePrimary("leads")}
          </Link>
        </Button>
      </header>
      <p className="muted">来源、负责人和待分配原因以记录为准。这里不创建线索，也不发起触达。</p>
      {!scope.tenant ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      {error ? (
        <div data-state="error" data-region="error">
          <SurfaceState
            kind="error"
            title="线索没有载入"
            detail={error}
            action={
              <Button variant="secondary" type="button" size="sm" onClick={() => setReload((n) => n + 1)}>
                重试
              </Button>
            }
          />
        </div>
      ) : null}
      {rows === null && !error ? (
        <div data-state="loading" data-region="loading">
          <SurfaceState kind="loading" title="正在读取线索" detail="姓名、来源和负责人马上就位。" />
          <RecordListSkeleton rows={3} />
        </div>
      ) : null}
      {rows !== null && rows.length === 0 && !error ? (
        <div data-state="empty" data-region="empty">
          <SurfaceState kind="empty" title="还没有线索" detail="这个工作范围里还没有可跟进的线索。" />
        </div>
      ) : null}
      {rows !== null && rows.length > 0 ? (
        <>
          <div className="bg-card text-card-foreground border border-border rounded-[10px] p-5 mb-4" data-list-filter="leads" data-region="status">
            <SegmentedControl
              label="状态筛选（当前范围全部线索）"
              items={STATUS_FILTERS}
              value={statusFilter}
              onValueChange={(next) => {
                const chosen = next ?? "all";
                setStatusFilter(chosen);
                saveListPosition(chosen);
              }}
            />
            <p className="muted">
              筛选在本范围返回的全部线索上进行，共 {rows.length} 条，当前显示 {visibleRows.length} 条。
            </p>
          </div>
          {visibleRows.length === 0 ? (
            <div data-state="empty" data-region="empty">
              <SurfaceState kind="empty" title="这个状态没有线索" detail="换一个状态筛选再看。" />
            </div>
          ) : (
            <div data-region="body">
            <RecordList
              width={width}
              rows={visibleRows.map((row) => ({
                id: row.id,
                title: row.name,
                action: "查看详情",
                onOpen: () => openDrawer(row.id),
                facts: leadRowFacts(row),
              }))}
            />
            </div>
          )}
        </>
      ) : null}

      <DetailDrawer
        open={Boolean(drawerId)}
        onClose={closeDrawer}
        title={drawerTitle}
        busy={drawerBusy || drawerSaving}
        width={width}
        footer={
          drawerId ? (
            <Button variant="outline" size="sm" asChild>
              <Link href={`/leads/${drawerId}`} onClick={() => saveListPosition()}>
                打开完整线索
              </Link>
            </Button>
          ) : null
        }
      >
        {drawerErr ? (
          <SurfaceState
            kind="error"
            title="线索没有打开"
            detail={drawerErr}
            action={
              drawerId ? (
                <PainuoButton variant="secondary" type="button" size="sm" onClick={() => loadDrawerTimeline(drawerId)}>
                  重试
                </PainuoButton>
              ) : null
            }
          />
        ) : null}
        {!drawerErr && !drawerBody && drawerBusy ? (
          <SurfaceState kind="loading" title="正在打开线索" detail="来源、负责人和下一步马上就位。" />
        ) : null}
        {drawerBody ? (
          <>
            <ul data-facts="chips">
              <li>
                <span>来源</span>
                <strong>{sourceText(drawerBody.source)}</strong>
              </li>
              <li>
                <span>状态</span>
                <strong>{deskState(drawerBody.statuses, undefined) || "未记录"}</strong>
              </li>
              <li>
                <span>负责人</span>
                <strong>{drawerBody.owner_label || "待分配"}{drawerBody.assignment_reason && drawerBody.assignment_reason !== "待分配" ? ` · ${drawerBody.assignment_reason}` : ""}</strong>
              </li>
              <li>
                <span>下一步</span>
                <strong>{drawerBody.next?.label || "还没有安排"}</strong>
              </li>
            </ul>
            <h2>最近动态</h2>
            {(drawerBody.events ?? []).length === 0 ? (
              <SurfaceState kind="empty" title="还没有记录" detail="在下方记一次跟进后，时间线会出现在这里。" />
            ) : (
              <ol className="desk-list">
                {(drawerBody.events ?? []).slice(0, 5).map((event, index) => (
                  <li key={`${event.kind}-${event.at}-${index}`}>
                    <p>{eventLine(event.kind, event.summary)}</p>
                    <p className="muted">{humanTime(event.at)}</p>
                  </li>
                ))}
              </ol>
            )}
            <div className="stack-form" data-drawer-followup="true">
              <h2>快速记跟进</h2>
              <Textarea
                label="跟进记录"
                value={drawerNote}
                onChange={(e) => setDrawerNote(e.target.value)}
                placeholder="这次跟进了什么（提交失败会保留你的输入）"
              />
              <DateTimeField label="下一次（北京时间）" value={drawerNextAt} onChange={setDrawerNextAt} />
              <div className="queue-actions">
                <PainuoButton
                  variant="secondary"
                  type="button"
                  size="sm"
                  disabled={drawerSaving || !drawerNote.trim()}
                  onClick={() => void submitDrawerFollow("note")}
                >
                  {drawerSaving ? "提交中…" : NARROW_ACTIONS[1]}
                </PainuoButton>
                <PainuoButton
                  variant="secondary"
                  type="button"
                  size="sm"
                  disabled={drawerSaving || !drawerNote.trim() || !drawerNextAt}
                  onClick={() => void submitDrawerFollow("schedule")}
                >
                  {NARROW_ACTIONS[2]}
                </PainuoButton>
              </div>
              {drawerMsg ? <p className="muted" role="status">{drawerMsg}</p> : null}
            </div>
            <p className="muted">
              当前状态：{deskState(drawerBody.statuses, undefined)}。关闭抽屉会回到当前筛选与滚动位置。
            </p>
          </>
        ) : null}
      </DetailDrawer>
    </main>
  );
}
