"use client";

// 获客唯一首页：today-next。HUI-2626 只换这一页的视觉外壳，不要另做首页。
// HUI-2626 / HUI-2598：首页先回答“今天先处理谁”，并在同一行展开
// 来源 → 会话 → 回复 → 下一步。会话/回复仍走服务端既有链路，不是第二套存储。

import Link from "next/link";
// 默认导入供 vitest/esbuild 的 classic JSX 转换使用（Next SWC automatic runtime 不受影响）。
import React, { useEffect, useRef, useState } from "react";
import { RecordFrame, SurfaceState, ToneBadge, useShellWidth } from "@/components/workbench/chrome";
import { ConversationPanel } from "@/components/workbench/conversationPanel";
import { DateTimeField } from "@/components/workbench/dateTimeField";
import { RendererSelect } from "@/components/workbench/rendererSelect";
import { Button, Textarea } from "@/vendor/painuo/react/v1/src/index";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { postJson } from "@/lib/fetchJson";
import { MISSING_SCOPE, factTone, failureText, listTenantHeader, pagePrimary, productError } from "@/lib/productShell";
import {
  HOME_SURFACE,
  NARROW_ACTIONS,
  TODAY_GROUPS,
  TODAY_LABELS,
  acceptToday,
  allowedContactText,
  billingCaption,
  jointChainLine,
  modelAdviceLine,
  centsText,
  moneyView,
  followNote,
  persistTodayMemory,
  questionsFor,
  receptionFactLine,
  recallToday,
  searchToken,
  readTodayMemory,
  reviseBody,
  rememberToday,
  todaySaveReady,
  renderLeadFacts,
  scopeCaption,
  serviceDraftClick,
  serviceDraftControl,
  visibleOutreach,
  attributionText,
  interactionText,
  type DeskItem,
  type MoneyFacts,
  type TodayDraft,
  type TodayGroup,
} from "@/lib/workbench";
import { attentionSummary, rowReason, todayDestination, todayRowTitle } from "@/lib/todayWorkspace";

interface JointChain {
  status?: string;
  label?: string;
}

interface DeskResponse {
  scope?: string;
  today?: Partial<Record<TodayGroup, DeskItem[]>>;
  money?: MoneyFacts;
  billing?: { ordinary_crm_charge_cents?: number };
  automation?: { auto_call?: boolean; auto_message?: boolean; create_order?: boolean };
  joint_chain?: JointChain;
  outreach_submitted?: boolean;
  message?: string;
  error?: string;
}

const EMPTY_MONEY: MoneyFacts = {
  customer_deal_cents: null,
  painuo_service_order_cents: null,
  platform_tool_spend_cents: null,
};

const DISPOSITION_CHOICES = [
  { value: "next", label: "安排下一步" },
  { value: "waiting_customer", label: "等待客户" },
] as const;

const GROUP_HINT: Record<TodayGroup, string> = {
  new_inquiry: "新线索会先留在这里；打开会话或记一次跟进后再分组。",
  due_today: "约在今天跟进的联系人会出现在这里。",
  waiting_customer: "等待客户回复的会话和线索留在这里，不催办。",
  overdue: "过了约定时间还没有下一步的，会出现在这里。",
  ai_draft: "需要人工确认的 AI 草稿会出现在这里；不会自动发送。",
  human_takeover: "已经转人工或需要接管的会话会出现在这里。",
  info_or_permission: "缺负责人、被过滤或授权不足的线索会出现在这里，先补事实。",
};

function emptyDraft(): TodayDraft {
  return { note: "", nextAt: "", disposition: "next", changed: [] };
}

export default function Home() {
  const scope = useCrmScope();
  const width = useShellWidth();
  const [reload, setReload] = useState(0);
  const [loadedFor, setLoadedFor] = useState<string | null>(null);
  const [groups, setGroups] = useState<Record<TodayGroup, DeskItem[]> | null>(null);
  const [money, setMoney] = useState<MoneyFacts>(EMPTY_MONEY);
  const [scopeLabel, setScopeLabel] = useState("");
  const [jointChain, setJointChain] = useState<JointChain | null>(null);
  const [err, setErr] = useState("");
  const [draftMsg, setDraftMsg] = useState("");
  const [focusId, setFocusId] = useState("");
  const [drafts, setDrafts] = useState<Record<string, TodayDraft>>({});
  const [conversationFor, setConversationFor] = useState("");
  const appliedTenant = useRef<string | null>(null);
  const shownTenant = useRef<string | null>(null);

  useEffect(() => {
    const restored = recallToday(readTodayMemory(), scope.tenant);
    const focus = searchToken(new URLSearchParams(window.location.search).get("focus"));
    setDrafts(restored.drafts);
    setFocusId(focus || restored.focusId);
    setConversationFor("");
    appliedTenant.current = null;
  }, [scope.tenant]);

  useEffect(() => {
    if (!todaySaveReady(appliedTenant.current, scope.tenant)) {
      appliedTenant.current = scope.tenant ?? null;
      return;
    }
    persistTodayMemory(rememberToday(readTodayMemory(), scope.tenant ?? "", focusId, drafts));
  }, [scope.tenant, focusId, drafts]);

  // HUI-2598 D4：只有换工作范围才清空列表。行内回复/接管后的 reload 若把
  // groups 置空，会卸载会话面板并丢掉刚写好的输入与提示；刷新应保留旧数据，
  // 新数据到达后原地替换。
  useEffect(() => {
    if (shownTenant.current === scope.tenant) return;
    shownTenant.current = scope.tenant;
    setLoadedFor(null);
    setGroups(null);
    setMoney(EMPTY_MONEY);
    setJointChain(null);
    setConversationFor("");
  }, [scope.tenant]);

  useEffect(() => {
    const headers = listTenantHeader(scope.tenant);
    if (!headers) {
      setErr("");
      setGroups(acceptToday(null, null));
      setLoadedFor("");
      return;
    }
    const tenant = scope.tenant ?? "";
    let cancelled = false;
    fetch("/api/workbench", { headers })
      .then(async (res) => {
        const body = (await res.json()) as DeskResponse;
        if (cancelled) return;
        if (!res.ok) {
          setErr(failureText(body, res.status));
          setGroups(acceptToday(tenant, null));
          setJointChain(body.joint_chain ?? null);
          setLoadedFor(tenant);
          return;
        }
        setErr("");
        setScopeLabel(scopeCaption(body.scope));
        setGroups(acceptToday(tenant, body.today ?? null));
        setMoney(body.money ?? EMPTY_MONEY);
        setJointChain(body.joint_chain ?? null);
        setLoadedFor(tenant);
      })
      .catch((e: Error) => {
        if (!cancelled) setErr(productError(e.message));
      });
    return () => {
      cancelled = true;
    };
  }, [scope.epoch, scope.tenant, reload]);

  const visible = loadedFor === (scope.tenant ?? "") ? groups : null;
  const lines = moneyView(visible ? money : EMPTY_MONEY);
  const attention = attentionSummary(visible);
  const primaryId = focusId || (visible ? TODAY_GROUPS.flatMap((key) => visible[key]).find((item) => item.lead_id)?.id ?? "" : "");

  useEffect(() => {
    if (!focusId || !visible) return;
    document.getElementById(`today-item-${focusId}`)?.scrollIntoView({ block: "center" });
  }, [focusId, visible]);

  function draftFor(id: string): TodayDraft {
    return drafts[id] ?? emptyDraft();
  }

  function updateDraft(id: string, patch: Partial<TodayDraft>) {
    setFocusId(id);
    setDrafts((current) => ({ ...current, [id]: { ...draftFor(id), ...current[id], ...patch } }));
  }

  async function submitFollow(item: DeskItem, mode: "note" | "schedule") {
    const headers = listTenantHeader(scope.tenant);
    if (!headers || !item.lead_id) return;
    const draft = draftFor(item.id);
    const note = followNote(draft.note);
    if (!note) return;
    if (mode === "schedule" && !draft.nextAt) return;
    setDraftMsg("");
    const waiting = mode === "note" && draft.disposition === "waiting_customer";
    const payload: Record<string, unknown> = {
      note,
      complete: true,
      disposition: waiting ? "waiting_customer" : "next",
    };
    if (!waiting && draft.nextAt) {
      payload.next_follow_up_at = new Date(draft.nextAt).toISOString().replace(/\.\d{3}Z$/, "Z");
    }
    const out = await postJson<{ today_group?: string; next?: { label?: string } }>(`/api/leads/${item.lead_id}/follow-through`, { "content-type": "application/json", ...headers }, payload);
    if (!out.ok) {
      setDraftMsg(failureText(out.body, out.status));
      return;
    }
    setDrafts((current) => {
      const next = { ...current };
      delete next[item.id];
      return next;
    });
    setDraftMsg(out.body.today_group === "scheduled_next" ? "已安排下一步，不在今天的待办里" : (out.body.next?.label ?? "已记下"));
    setReload((n) => n + 1);
  }

  async function reviseDraft(item: DeskItem) {
    const headers = listTenantHeader(scope.tenant);
    if (!headers) return;
    const body = reviseBody(draftFor(item.id).draftBody, item.draft_body).trim();
    if (!body) return;
    const out = await postJson<{ sent?: boolean; message?: string; error?: string }>(`/api/workbench/drafts/${item.id}/revise`, { "content-type": "application/json", ...headers }, { body });
    if (!out.ok || out.body.sent === true) {
      setDraftMsg(!out.ok ? failureText(out.body, out.status) : (out.body.message ?? out.body.error ?? "没有修改"));
      return;
    }
    setDraftMsg("草稿已修改，尚未发送");
    setReload((n) => n + 1);
  }

  async function ignoreDraft(item: DeskItem) {
    const headers = listTenantHeader(scope.tenant);
    if (!headers) return;
    const out = await postJson<{ sent?: boolean; message?: string; error?: string }>(`/api/workbench/drafts/${item.id}/ignore`, { "content-type": "application/json", ...headers }, {});
    if (!out.ok || out.body.sent === true) {
      setDraftMsg(!out.ok ? failureText(out.body, out.status) : (out.body.message ?? out.body.error ?? "没有忽略"));
      return;
    }
    setDraftMsg("已忽略草稿，没有发送");
    setReload((n) => n + 1);
  }

  return (
    <main data-today-home={HOME_SURFACE} data-page="today" data-pattern="workspace">
      <header className="page-head" data-region="header">
        <h1>今天要完成的客户工作</h1>
        <p className="muted">
          {scopeLabel ? `当前视图：${scopeLabel}。` : ""}
          {billingCaption()}
        </p>
      </header>

      {draftMsg ? <p className="muted" role="status">{draftMsg}</p> : null}

      <section className="attention-strip" data-region="status" aria-label="今天的注意力">
        {visible && !attention.empty ? (
          <ul>
            {attention.rows.map((row) => (
              <li key={row.group}>
                <a className="attention-chip" href={row.href} data-attention-group={row.group}>
                  {row.label}
                  <strong>{row.count}</strong>
                </a>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">{visible ? "今天没有需要处理的客户工作。" : "正在整理今天的工作。"}</p>
        )}
        <details className="work-rationale">
          <summary>排序与金额口径</summary>
          <p>
            <ToneBadge tone="human" /> 顺序：新咨询 → 今天应跟进 → 等待客户 → 逾期 → AI 草稿 → 人工接管 → 信息或权限。
            手工安排的下一步优先。
          </p>
          <p data-tone="automation">
            <ToneBadge tone="automation" /> 这里不会自动外呼、发消息或创建订单。
          </p>
          <p data-tone="ai">
            <ToneBadge tone="ai" /> {modelAdviceLine()}。意向分级只给下一步建议，不自动触达。
          </p>
          <p data-joint-chain="incomplete">{jointChainLine(jointChain)}</p>
          <ul className="money-lines">
            {lines.lines.map((line) => (
              <li key={line.key}>
                {line.label}：{centsText(line.cents)}
              </li>
            ))}
          </ul>
        </details>
      </section>

      {!scope.tenant ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      {scope.tenant && err ? (
        <div data-state="error" data-region="error">
          <SurfaceState
            kind="error"
            title="今天的工作没有载入"
            detail={err}
            action={
              <Button variant="secondary" type="button" size="sm" onClick={() => setReload((n) => n + 1)}>
                重试
              </Button>
            }
          />
        </div>
      ) : null}
      {scope.tenant && !err && visible === null ? (
        <div data-state="loading" data-region="loading">
          <SurfaceState kind="loading" title="正在整理今天的工作" detail="分组和金额马上就位。" />
        </div>
      ) : null}
      {scope.tenant && !err && visible && attention.empty ? (
        <div data-state="empty" data-region="empty">
          <SurfaceState
            kind="empty"
            title="今天没有需要处理的客户工作"
            detail="新的咨询、到期的跟进、未回复的会话和待确认的草稿会按顺序出现在这里。先去线索或接待看看也可以。"
            action={<Link className="btn" href="/leads">打开线索列表</Link>}
          />
        </div>
      ) : null}
      {scope.tenant && !err && visible && !attention.empty
        ? TODAY_GROUPS.map((key) => {
          const rows = visible[key];
          if (rows.length === 0) return null;
          return (
            <section className="today-group" key={key} id={`today-group-${key}`} data-today-group={key} data-region="body">
              <header className="today-group-head">
                <h2>
                  {TODAY_LABELS[key]}
                  <span className="today-count">{rows.length}</span>
                </h2>
                <p className="muted">{GROUP_HINT[key]}</p>
              </header>
              <ul className="desk-list">
                {rows.map((item) => (
                  <TodayRow
                    key={`${key}-${item.id}`}
                    width={width}
                    group={key}
                    primaryRow={item.id === primaryId}
                    item={item}
                    jointChain={jointChain}
                    draft={draftFor(item.id)}
                    conversationOpen={conversationFor === item.id}
                    onToggleConversation={() => setConversationFor((current) => (current === item.id ? "" : item.id))}
                    onConversationChanged={() => setReload((n) => n + 1)}
                    onDraft={(patch) => updateDraft(item.id, patch)}
                    onFollow={(mode) => void submitFollow(item, mode)}
                    onRevise={() => void reviseDraft(item)}
                    onIgnore={() => void ignoreDraft(item)}
                    onService={() => setDraftMsg(serviceDraftClick().message)}
                  />
                ))}
              </ul>
            </section>
          );
        })
        : null}
      {/* HUI-2626 盲评 P3：foot-nav 让每个链接整体换行，390 不再从词中间折断。 */}
      <p className="muted foot-nav">
        <Link href="/leads">线索</Link> · <Link href="/contacts">客户档案</Link> · <Link href="/opportunities">商机</Link> ·{" "}
        <Link href="/reception">接待</Link> · <Link href="/intent">意向分级</Link> · <Link href="/attribution">来源与费用</Link> · <Link href="/subscription">订阅与用量</Link> ·{" "}
        <Link href="/isolation">租户隔离与导出</Link>
      </p>
    </main>
  );
}

function TodayRow({
  width,
  group,
  primaryRow,
  item,
  jointChain,
  draft,
  conversationOpen,
  onToggleConversation,
  onConversationChanged,
  onDraft,
  onFollow,
  onRevise,
  onIgnore,
  onService,
}: {
  width: number;
  group: TodayGroup;
  primaryRow: boolean;
  item: DeskItem;
  jointChain: JointChain | null;
  draft: TodayDraft;
  conversationOpen: boolean;
  onToggleConversation: () => void;
  onConversationChanged: () => void;
  onDraft: (patch: Partial<TodayDraft>) => void;
  onFollow: (mode: "note" | "schedule") => void;
  onRevise: () => void;
  onIgnore: () => void;
  onService: () => void;
}) {
  const facts = renderLeadFacts(item, jointChain);
  const outreach = visibleOutreach(item.outreach_notice);
  const receptionLine = receptionFactLine(item);
  const service = serviceDraftControl(item.service_draft);
  const confirmed = item.context?.facts ?? [];
  const questions = questionsFor(confirmed, item.context?.ask ?? item.ask ?? [], []);
  const tone = factTone({
    kind: item.kind,
    auto: item.next?.auto_call === true || item.next?.auto_message === true || item.next?.create_order === true,
  });
  const destination = todayDestination(item);
  const sourceLine = attributionText(item.source);
  const factRows = [
    { label: "为什么在今天", value: rowReason(item, group) },
    ...(receptionLine ? [{ label: "接待", value: receptionLine }] : []),
    { label: "来源", value: sourceLine },
    { label: "已确认", value: `${item.context?.customer || "客户未记录"}${item.context?.business ? ` · ${item.context.business}` : ""}${confirmed.length ? ` · ${confirmed.join("、")}` : ""}` },
    { label: "负责人", value: item.owner_label || (item.session_id ? "尚未接管" : facts.owner) },
    { label: "状态", value: `状态：${facts.state}` },
    { label: "允许的联系方式", value: allowedContactText(item.allowed_contacts) },
    { label: "最近一次互动", value: interactionText(item.last_interaction) },
    { label: "下一步", value: facts.next },
    { label: "依据", value: item.basis || modelAdviceLine() },
    { label: "同步", value: facts.sync },
    ...(outreach ? [{ label: "触达", value: outreach }] : []),
    ...(item.reason && item.kind !== "reception" ? [{ label: "原因", value: item.reason }] : []),
    ...(questions.length > 0 ? [{ label: "待补充", value: questions.join("、") }] : []),
    ...(item.draft_body ? [{ label: "草稿", value: item.draft_body }] : []),
  ];
  const followDisabled = !draft.note.trim();
  const primaryFollow = (
    <Button
      variant="primary"
      type="button"
      size="sm"
      data-page-primary="true"
      data-desk-action={NARROW_ACTIONS[1]}
      disabled={followDisabled}
      onClick={() => onFollow("note")}
    >
      {pagePrimary("today")}
    </Button>
  );
  const secondaryFollow = (
    <Button
      variant="secondary"
      type="button"
      size="sm"
      data-page-primary="false"
      data-desk-action={NARROW_ACTIONS[1]}
      disabled={followDisabled}
      onClick={() => onFollow("note")}
    >
      {pagePrimary("today")}
    </Button>
  );
  return (
    <li id={`today-item-${item.id}`} data-today-kind={item.kind}>
      <RecordFrame width={width} tone={tone} title={todayRowTitle(item, group)} facts={factRows} primary={primaryRow && item.lead_id ? primaryFollow : undefined}>
        {item.kind === "reception" ? <p data-reception-fact="owner">{receptionLine}</p> : null}
        <p data-joint-chain="incomplete">{facts.chain}</p>
        <div className="queue-actions">
          {item.session_id ? (
            <Button variant="secondary" type="button" size="sm" data-conversation-toggle={item.session_id} aria-expanded={conversationOpen} onClick={onToggleConversation}>
              {conversationOpen ? "收起会话" : "展开会话与回复"}
            </Button>
          ) : null}
          {/* HUI-2626 行内导航至多一个：展开会话是控件，真正跳转只留一条。 */}
          {destination ? (
            <Link className="btn" href={destination.href} data-desk-action={destination.kind === "conversation" ? undefined : NARROW_ACTIONS[0]}>
              {destination.kind === "conversation" ? "在接待台打开" : destination.label}
            </Link>
          ) : null}
          {service.present ? (
            <Button variant="secondary" type="button" size="sm" disabled={!service.enabled} onClick={onService}>
              创建服务需求草稿
            </Button>
          ) : null}
        </div>
        {service.present && !service.enabled ? <p className="muted">{service.reason}</p> : null}
        {conversationOpen && item.session_id ? (
          <ConversationPanel
            sessionId={item.session_id}
            focusId={item.id}
            initialEpoch={item.reception?.epoch}
            onChanged={onConversationChanged}
          />
        ) : null}
        {item.lead_id ? (
          <div className="stack-form">
            {/* HUI-2626 fix2（gate-r2 #1/#9 同批）：提交语义收敛到显式按钮（主行动至多一个），控件走 vendored/标准件。 */}
            <Textarea
              label="记录"
              value={draft.note}
              placeholder={item.next?.label || "这次跟进了什么"}
              onFocus={() => onDraft({})}
              onChange={(e) => onDraft({ note: e.target.value })}
            />
            <RendererSelect
              label="处理"
              value={draft.disposition}
              onValueChange={(next) => onDraft({ disposition: next as TodayDraft["disposition"] })}
              choices={DISPOSITION_CHOICES}
            />
            <DateTimeField label="下一次（北京时间）" value={draft.nextAt} onChange={(v) => onDraft({ nextAt: v })} />
            <div className="queue-actions">
              {primaryRow ? null : secondaryFollow}
              <Button variant="secondary" type="button" size="sm" data-desk-action={NARROW_ACTIONS[2]} disabled={!draft.note.trim() || !draft.nextAt} onClick={() => onFollow("schedule")}>
                {NARROW_ACTIONS[2]}
              </Button>
            </div>
          </div>
        ) : null}
        {item.kind === "ai_draft" ? (
          <div className="stack-form">
            <Textarea
              label="修改草稿"
              value={reviseBody(draft.draftBody, item.draft_body)}
              onChange={(e) => onDraft({ draftBody: e.target.value })}
              placeholder="改成要保留的措辞"
            />
            <div className="queue-actions">
              <Button variant="secondary" type="button" size="sm" onClick={onRevise}>保存修改</Button>
              <Button variant="ghost" type="button" size="sm" onClick={onIgnore}>忽略</Button>
            </div>
          </div>
        ) : null}
        <p className="muted">
          下一步只记录在服务端；离开页面不会把未保存的输入当成已发送。
        </p>
      </RecordFrame>
    </li>
  );
}
