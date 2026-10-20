// HUI-2626 今日工作台：把服务端已分好的事实映射成“先看谁 / 从哪进 / 能不能回”。
//
// 这里只有纯规则：不发请求、不读 DOM、不推断权限。服务端仍是范围、轮次、
// 可发送性的唯一真源；组件只按这里判过的动作渲染。
import { isEngineeringRef } from "@/lib/humanize";
import {
  TODAY_GROUPS,
  TODAY_LABELS,
  receptionOpenHref,
  searchToken,
  type DeskItem,
  type TodayGroup,
} from "@/lib/workbench";

export type TodayDestinationKind = "conversation" | "lead" | "opportunity";

export interface TodayDestination {
  href: string;
  label: string;
  kind: TodayDestinationKind;
}

// 会话类行（接待 / AI 草稿）必须先回到会话：草稿的批准、发送、轮次都在那里。
// 如果先落到线索详情，用户找不到“发送”，这是 HUI-2598 的 D2 断点。
export function todayDestination(item: DeskItem): TodayDestination | null {
  const session = searchToken(item.session_id ?? "");
  const lead = searchToken(item.lead_id ?? "");
  const opportunity = searchToken(item.opportunity_id ?? "");
  const conversationKind = item.kind === "reception" || item.kind === "ai_draft" || item.kind === "human_takeover";
  if (session) {
    return { href: receptionOpenHref(session, item.id), label: "打开会话", kind: "conversation" };
  }
  if (conversationKind) return null;
  if (lead) return { href: `/leads/${lead}`, label: "查看线索", kind: "lead" };
  if (opportunity) return { href: `/opportunities/${opportunity}`, label: "打开商机", kind: "opportunity" };
  return null;
}

export interface AttentionRow {
  group: TodayGroup;
  label: string;
  count: number;
  href: string;
}

export interface AttentionSummary {
  total: number;
  rows: AttentionRow[];
  empty: boolean;
}

// 注意力条：固定组序、只列非空组；它是导航，不是 KPI 趋势图。
export function attentionSummary(groups: Partial<Record<TodayGroup, DeskItem[]>> | null): AttentionSummary {
  const rows: AttentionRow[] = [];
  let total = 0;
  for (const group of TODAY_GROUPS) {
    const count = groups?.[group]?.length ?? 0;
    total += count;
    if (count === 0) continue;
    rows.push({ group, label: TODAY_LABELS[group], count, href: `#today-group-${group}` });
  }
  return { total, rows, empty: total === 0 };
}

// 一行“为什么在今天”：组名 + 具体原因/下一步。工程引用只留在数据层。
export function rowReason(item: DeskItem, group: TodayGroup): string {
  const label = TODAY_LABELS[group];
  if (item.kind === "ai_draft") return `${label} · 待确认，不会自动发送`;
  const reason = (item.reason ?? "").trim();
  if (reason && !isEngineeringRef(reason)) return `${label} · ${reason}`;
  const next = (item.next?.label ?? "").trim();
  if (next) return `${label} · 下一步：${next}`;
  return `${label} · 还没有安排下一步`;
}

export interface ConversationFacts {
  status?: string;
  mode?: string;
  epoch?: number;
  version?: number;
}

export interface ReplyFacts {
  id?: string;
  body?: string;
  status?: string;
  kind?: string;
  epoch?: number;
  session_version?: number;
}

export interface ConversationActions {
  open: boolean;
  canTakeover: boolean;
  canRelease: boolean;
  canApprove: boolean;
  canSend: boolean;
  canHumanReply: boolean;
  blockedReason: string;
}

// 与服务端一致的可执行性：approve 接受 assist/human 的 current-epoch generated；
// send 只接受 current-epoch approved；human 直接回复要求 mode=human。
export function conversationActions(session: ConversationFacts | null, reply: ReplyFacts | null): ConversationActions {
  const open = session?.status === "open";
  const sameEpoch = Boolean(reply && session && reply.epoch !== undefined && reply.epoch === session.epoch);
  const generated = sameEpoch && reply?.status === "generated";
  const approved = sameEpoch && reply?.status === "approved";
  const mode = session?.mode ?? "";
  const canTakeover = open && mode !== "human";
  const canRelease = open && mode === "human";
  const canApprove = open && generated && (mode === "assist" || mode === "human");
  const canSend = open && approved;
  const canHumanReply = open && mode === "human";
  let blockedReason = "";
  if (!session) blockedReason = "会话还没载入";
  else if (!open) blockedReason = "会话已结案，不能再回复";
  else if (generated && !canApprove) blockedReason = "先转人工接管或改为协助，才能批准这条草稿";
  else if (!approved && !generated && mode !== "human") blockedReason = "AI 会话不能直接发送人工回复";
  return { open, canTakeover, canRelease, canApprove, canSend, canHumanReply, blockedReason };
}

export interface DraftPrefill {
  replyId: string;
  body: string;
}

// 人工接管后回到会话，最近一条未发送草稿要能直接接着写。
// 只预填 generated draft；已发送/作废/旧轮次都不算。
export function draftPrefill(replies: ReplyFacts[] | null | undefined, mode: string | undefined): DraftPrefill | null {
  if (mode !== "human" || !replies || replies.length === 0) return null;
  for (let i = replies.length - 1; i >= 0; i--) {
    const reply = replies[i];
    if (!reply || reply.status !== "generated") continue;
    if (reply.kind !== "draft" && reply.kind !== "ai") continue;
    const id = (reply.id ?? "").trim();
    const body = reply.body ?? "";
    if (!id || !body.trim()) continue;
    return { replyId: id, body };
  }
  return null;
}
