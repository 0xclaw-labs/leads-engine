"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState } from "react";
import { RecordFrame, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { DateTimeField } from "@/components/workbench/dateTimeField";
import { RendererSelect } from "@/components/workbench/rendererSelect";
import { Button, Textarea } from "@/vendor/painuo/react/v1/src/index";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { eventLine, humanTime, leadTitle } from "@/lib/humanize";
import { MISSING_SCOPE, failureText, listTenantHeader, pagePrimary, productError } from "@/lib/productShell";
import { patchJson, postJson } from "@/lib/fetchJson";
import { assignBody, assignableMembers, needsOwner, type MemberOption, type MemberRow } from "@/lib/leadAssign";
import {
  NARROW_ACTIONS,
  allowedContactText,
  deskState,
  jointChainLine,
  modelAdviceLine,
  nextLine,
  ownerLine,
  sourceText,
  syncLine,
  visibleOutreach,
  type StatusFacts,
} from "@/lib/workbench";
import { CampaignMotionPanel } from "@/components/CampaignMotionPanel";
import { MatrixDraftPanel } from "@/components/MatrixDraftPanel";
import { LightCopyPanel } from "@/components/LightCopyPanel";

interface TimelineEvent {
  at: string;
  kind: string;
  summary: string;
  contact_plaintext?: string;
}

interface TimelineResponse {
  source?: { form?: string; activity?: string; channel?: string; at?: string };
  statuses?: StatusFacts;
  next?: { kind?: string; source?: string; label?: string; at?: string };
  events?: TimelineEvent[];
  owner_label?: string;
  assignment_reason?: string;
  allowed_contacts?: string[];
  sync?: { crm?: string };
  outreach_notice?: string;
  joint_chain?: { status?: string; label?: string };
  message?: string;
  error?: string;
}

export default function LeadDeskPage() {
  const params = useParams<{ id: string }>();
  const id = params?.id ?? "";
  const scope = useCrmScope();
  const [loadedFor, setLoadedFor] = useState("");
  const [body, setBody] = useState<TimelineResponse | null>(null);
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  const [nextAt, setNextAt] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [reload, setReload] = useState(0);
  // 分配块：只有待分配线索才取成员；读取失败（非 owner）则不展示，服务端仍是权限真源。
  const [members, setMembers] = useState<MemberOption[] | null>(null);
  const [pick, setPick] = useState("");
  const [assignMsg, setAssignMsg] = useState("");
  const [assignBusy, setAssignBusy] = useState(false);

  useEffect(() => {
    setBody(null);
    setLoadedFor("");
    setErr("");
    if (!scope.tenant || !id) {
      setErr("");
      return;
    }
    const tenant = scope.tenant;
    let cancelled = false;
    fetch(`/api/leads/${id}/timeline`, { headers: listTenantHeader(tenant) ?? {} })
      .then(async (res) => {
        const payload = (await res.json()) as TimelineResponse;
        if (cancelled) return;
        if (!res.ok) {
          setErr(failureText(payload, res.status));
          setBody(null);
          setLoadedFor(tenant);
          return;
        }
        setBody(payload);
        setLoadedFor(tenant);
      })
      .catch((e: Error) => {
        if (!cancelled) setErr(productError(e.message));
      });
    return () => {
      cancelled = true;
    };
  }, [scope.epoch, scope.tenant, id, reload]);

  const width = useShellWidth();
  const visible = loadedFor === scope.tenant ? body : null;
  const channel = visible?.next?.kind === "channel_follow_up";
  const assignOpen = Boolean(visible && needsOwner(visible.owner_label));

  useEffect(() => {
    setAssignMsg("");
  }, [id]);

  useEffect(() => {
    setMembers(null);
    setPick("");
    if (!assignOpen || !scope.tenant) return;
    const tenant = scope.tenant;
    let cancelled = false;
    fetch("/api/admin/members", { headers: listTenantHeader(tenant) ?? {} })
      .then(async (res) => {
        if (cancelled || !res.ok) return;
        const payload = (await res.json()) as { items?: MemberRow[] };
        if (!cancelled) setMembers(assignableMembers(payload.items ?? []));
      })
      .catch(() => {
        if (!cancelled) setMembers(null);
      });
    return () => {
      cancelled = true;
    };
  }, [assignOpen, scope.epoch, scope.tenant, id]);

  async function assign() {
    if (!scope.tenant || !pick) return;
    setAssignBusy(true);
    setAssignMsg("");
    try {
      const out = await patchJson(
        `/api/leads/${id}`,
        { "content-type": "application/json", ...(listTenantHeader(scope.tenant) ?? {}) },
        assignBody(pick),
      );
      if (!out.ok) {
        setAssignMsg(failureText(out.body, out.status));
        return;
      }
      setAssignMsg("已分配负责人");
      setReload((n) => n + 1);
    } catch (e) {
      setAssignMsg(productError((e as Error).message));
    } finally {
      setAssignBusy(false);
    }
  }

  async function submit(channelMode: boolean) {
    if (!scope.tenant) return;
    setBusy(true);
    setMsg("");
    const when = nextAt ? new Date(nextAt).toISOString().replace(/\.\d{3}Z$/, "Z") : "";
    const payload: Record<string, unknown> = { note, complete: true };
    if (when) payload.next_follow_up_at = when;
    if (channelMode) payload.channel = "in_channel";
    try {
      const out = await postJson<{ next?: { label?: string } }>(
        `/api/leads/${id}/follow-through`,
        { "content-type": "application/json", ...(listTenantHeader(scope.tenant) ?? {}) },
        payload,
      );
      if (!out.ok) {
        setMsg(failureText(out.body, out.status));
        return;
      }
      setNote("");
      setNextAt("");
      setMsg(out.body.next?.label ?? "已记下");
      setLoadedFor("");
      const again = await fetch(`/api/leads/${id}/timeline`, { headers: listTenantHeader(scope.tenant) ?? {} });
      const nextBody = await again.json();
      if (again.ok) setBody(nextBody);
      setLoadedFor(scope.tenant);
    } catch (e) {
      setMsg(productError((e as Error).message));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main data-page="lead-detail" data-pattern="detail">
      <header className="page-head" data-region="header">
        <h1>线索</h1>
        <Link className="btn" href="/">返回工作台</Link>
      </header>
      {!scope.tenant ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      {err ? (
        <div data-state="error" data-region="error">
          <SurfaceState
            kind="error"
            title="这条线索没有打开"
            detail={err}
            action={
              <>
                <Button variant="secondary" type="button" size="sm" onClick={() => setReload((n) => n + 1)}>
                  重试
                </Button>
                <Link className="btn" href="/leads">
                  返回线索列表
                </Link>
              </>
            }
          />
        </div>
      ) : null}
      {scope.tenant && visible === null && !err ? (
        <div data-state="loading" data-region="loading">
          <SurfaceState kind="loading" title="正在打开线索" detail="来源、负责人和下一步马上就位。" />
        </div>
      ) : null}
      {visible ? (
        <>
          <RecordFrame
            width={width}
            tone="human"
            title={leadTitle(visible.source)}
            facts={[
              { label: "来源", value: sourceText(visible.source) },
              { label: "负责人", value: ownerLine(visible.owner_label, visible.assignment_reason) },
              { label: "允许的联系方式", value: allowedContactText(visible.allowed_contacts) },
              { label: "状态", value: `状态：${deskState(visible.statuses, visible.sync)}` },
              { label: "同步", value: syncLine(visible.sync) },
              { label: "下一步", value: nextLine(visible.next) },
              { label: "建议", value: modelAdviceLine() },
              ...(visibleOutreach(visible.outreach_notice) ? [{ label: "触达", value: visibleOutreach(visible.outreach_notice) }] : []),
              ...(visible.next?.at ? [{ label: "时间", value: humanTime(visible.next.at) }] : []),
            ]}
          >
            <p data-joint-chain="incomplete">{jointChainLine(visible.joint_chain)}</p>
          </RecordFrame>
          {assignOpen && members && members.length > 0 ? (
            <div className="card stack-form" data-assign-owner="true">
              <h2>分配负责人</h2>
              <p className="muted">这条线索还没有负责人。分配后才能记跟进、安排下一步。</p>
              <RendererSelect
                label="负责人"
                value={pick}
                onValueChange={setPick}
                choices={[{ value: "", label: "选择成员", disabled: true }, ...members.map((m) => ({ value: m.id, label: m.label }))]}
                disabled={assignBusy}
              />
              <div className="queue-actions">
                <Button variant="secondary" type="button" size="sm" disabled={assignBusy || !pick} onClick={() => void assign()}>
                  分配负责人
                </Button>
              </div>
            </div>
          ) : null}
          {assignMsg ? (
            <p className="muted" role="status" data-assign-result="true">
              {assignMsg}
            </p>
          ) : null}
          <div className="card">
            <h2>时间线</h2>
            {(visible.events ?? []).length === 0 ? (
              <div data-state="empty" data-region="empty">
                <SurfaceState kind="empty" title="还没有记录" detail="记一次跟进后，时间线会出现在这里。" />
              </div>
            ) : null}
            <ol className="desk-list">
              {(visible.events ?? []).map((event, index) => (
                <li key={`${event.kind}-${event.at}-${index}`}>
                  {/* HUI-2626 盲评 P2③：显示层人化——类别/授权项映射成话，微秒 ISO 变本地秒级；原始值留在接口数据里。 */}
                  <p>{eventLine(event.kind, event.summary)}</p>
                  <p className="muted">{humanTime(event.at)}</p>
                  {event.contact_plaintext ? <p>联系方式 {event.contact_plaintext}</p> : null}
                </li>
              ))}
            </ol>
          </div>
          {/* HUI-2626 盲评 P2①：轻文案只在线索带活动出处时挂载——普通线索不再打必 404 的探测。 */}
          {id && visible?.source?.activity ? <LightCopyPanel subjectKind="campaign" subjectId={id} /> : null}
          {id ? <CampaignMotionPanel campaignId={id} /> : null}
          {id ? <MatrixDraftPanel campaignId={id} /> : null}
          <div className="card stack-form">
            {/* HUI-2626 fix2：提交语义收敛到显式按钮（主行动至多一个），控件走 vendored/标准件。 */}
            <h2>跟进</h2>
            <Textarea
              label="记录"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="这次跟进了什么"
            />
            <DateTimeField label="下一次" value={nextAt} onChange={setNextAt} />
            <div className="queue-actions">
              <Link className="btn" href={`/leads/${id}`} data-desk-action={NARROW_ACTIONS[0]}>{NARROW_ACTIONS[0]}</Link>
              <Button
                variant="primary"
                type="button"
                size="sm"
                data-page-primary="true"
                data-region="primary_action"
                data-desk-action={NARROW_ACTIONS[1]}
                disabled={busy || !note.trim()}
                onClick={() => void submit(false)}
              >
                {pagePrimary("lead-detail")}
              </Button>
              <Button
                variant="secondary"
                type="button"
                size="sm"
                data-desk-action={NARROW_ACTIONS[2]}
                disabled={busy || !note.trim() || !nextAt}
                onClick={() => void submit(false)}
              >
                {NARROW_ACTIONS[2]}
              </Button>
              {channel ? (
                <Button variant="ghost" type="button" size="sm" disabled={busy || !note.trim() || !nextAt} onClick={() => void submit(true)}>
                  安排渠道内跟进
                </Button>
              ) : null}
            </div>
            {msg ? <p className="muted">{msg}</p> : null}
          </div>
        </>
      ) : null}
    </main>
  );
}
