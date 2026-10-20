"use client";

// HUI-2626：今日工作台行内会话面板。
//
// 同一个会话 API（/api/reception/sessions/{id}）与同一条服务端草稿链路
// （/api/workbench/sessions/{id}/reply-draft）。这里不是第二条会话通道：
// 没有本地消息副本、没有客户端“已发送”状态；发送/批准/接管都由服务端裁决。
//
// 关键正常路径（HUI-2598 D3/D4）：
//   写回复 → 保存草稿 → 转人工（如需要）→ 批准 → 发送
// 任一失败都保留输入并说明恢复动作；不会自动重发、不会把草稿当已发送。
import Link from "next/link";
// 默认导入供 vitest/esbuild 的 classic JSX 转换使用（Next SWC automatic runtime 不受影响）。
import React, { useCallback, useEffect, useRef, useState } from "react";
import { Button, Textarea } from "@/vendor/painuo/react/v1/src/index";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { postJson } from "@/lib/fetchJson";
import { failureText, listTenantHeader, productError } from "@/lib/productShell";
import { REPLY_STATUS_TEXT, newStaffToken } from "@/lib/reception";
import { conversationActions, draftPrefill, type ConversationFacts, type ReplyFacts } from "@/lib/todayWorkspace";
import { receptionOpenHref } from "@/lib/workbench";

interface SessionView extends ConversationFacts {
  id: string;
  lead_id?: string;
  owner_member_id?: string;
  pending_reason?: string;
}

interface MessageView {
  id: string;
  body: string;
  created_at?: string;
}

interface ReplyView extends ReplyFacts {
  body: string;
  created_at?: string;
}

interface SessionBody {
  session?: SessionView;
  messages?: MessageView[];
  replies?: ReplyView[];
  message?: string;
  error?: string;
}

function modeText(mode?: string): string {
  if (mode === "human") return "人工接管";
  if (mode === "assist") return "人工确认后发送";
  return "AI 接待";
}

export function ConversationPanel({
  sessionId,
  focusId,
  initialEpoch,
  onChanged,
}: {
  sessionId: string;
  focusId: string;
  initialEpoch?: number;
  onChanged?: () => void;
}) {
  const scope = useCrmScope();
  const [session, setSession] = useState<SessionView | null>(null);
  const [messages, setMessages] = useState<MessageView[]>([]);
  const [replies, setReplies] = useState<ReplyView[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [text, setText] = useState("");
  const [draftId, setDraftId] = useState("");
  const [loaded, setLoaded] = useState(false);
  const ticket = useRef({ seq: 0, tenant: "" });
  const dirty = useRef(false);

  const load = useCallback(async () => {
    const tenant = scope.tenant ?? "";
    const next = { seq: ticket.current.seq + 1, tenant };
    ticket.current = next;
    const headers = listTenantHeader(tenant);
    if (!headers) {
      setError("先在顶部选择工作范围，才能读取会话。");
      setLoaded(true);
      return;
    }
    try {
      const res = await fetch(`/api/reception/sessions/${encodeURIComponent(sessionId)}`, { headers, cache: "no-store" });
      const body = (await res.json().catch(() => ({}))) as SessionBody;
      if (ticket.current !== next) return;
      if (!res.ok || !body.session) {
        setError(failureText(body, res.status));
        setLoaded(true);
        return;
      }
      setError("");
      setSession(body.session);
      setMessages(body.messages ?? []);
      setReplies(body.replies ?? []);
      setLoaded(true);
      if (!dirty.current) {
        const prefill = draftPrefill(body.replies ?? [], body.session.mode);
        if (prefill) {
          setText(prefill.body);
          setDraftId(prefill.replyId);
        }
      }
    } catch (e) {
      if (ticket.current !== next) return;
      setError(productError(e instanceof Error ? e.message : "读取会话失败"));
      setLoaded(true);
    }
  }, [scope.tenant, sessionId]);

  useEffect(() => {
    setSession(null);
    setMessages([]);
    setReplies([]);
    setText("");
    setDraftId("");
    setNotice("");
    setError("");
    setLoaded(false);
    dirty.current = false;
    void load();
  }, [load]);

  const currentDraft = replies.find((reply) => reply.id === draftId) ?? null;
  const actions = conversationActions(session, currentDraft);
  const headers = (): Record<string, string> => ({
    "content-type": "application/json",
    ...(listTenantHeader(scope.tenant) ?? {}),
  });

  async function call(path: string, payload: unknown): Promise<Record<string, unknown> | null> {
    setBusy(true);
    setNotice("");
    const out = await postJson<Record<string, unknown>>(path, headers(), payload);
    setBusy(false);
    if (!out.ok) {
      setError(failureText(out.body, out.status));
      return null;
    }
    setError("");
    return out.body;
  }

  async function saveDraft(): Promise<string | null> {
    const body = text.trim();
    if (!body) {
      setError("先写回复内容，再保存草稿。");
      return null;
    }
    const saved = await call(`/api/workbench/sessions/${encodeURIComponent(sessionId)}/reply-draft`, {
      client_reply_id: newStaffToken("desk-"),
      body,
    });
    if (!saved) return null;
    const id = typeof saved.id === "string" ? saved.id : "";
    if (!id) {
      setError("服务端没有返回草稿编号，草稿没有保存。");
      return null;
    }
    setDraftId(id);
    setNotice(saved.sent === true ? "这条回复已经是已发送状态。" : "草稿已保存，尚未发送。");
    await load();
    return id;
  }

  async function approveDraft(id: string): Promise<boolean> {
    const out = await call(`/api/reception/sessions/${encodeURIComponent(sessionId)}/replies/${encodeURIComponent(id)}/approve`, {});
    if (!out) return false;
    setNotice("草稿已批准，尚未发送。");
    await load();
    return true;
  }

  async function sendDraft() {
    let id = draftId;
    let status = currentDraft?.status ?? "";
    if (!id || !currentDraft) {
      id = (await saveDraft()) ?? "";
      if (!id) return;
      status = "generated";
    }
    if (status === "generated") {
      const ok = await approveDraft(id);
      if (!ok) return;
      status = "approved";
    }
    const sent = await call(`/api/reception/sessions/${encodeURIComponent(sessionId)}/replies/${encodeURIComponent(id)}/send`, {
      receipt_id: newStaffToken("rcpt-"),
    });
    if (!sent) return;
    setNotice(sent.duplicate === true ? "这条回复已经发送过，没有重复发送。" : "回复已发送。");
    dirty.current = false;
    setText("");
    setDraftId("");
    await load();
    onChanged?.();
  }

  async function takeover() {
    const epoch = session?.epoch ?? initialEpoch ?? 1;
    const out = await call(`/api/reception/sessions/${encodeURIComponent(sessionId)}/takeover`, { epoch });
    if (!out) return;
    setNotice("已转人工接管。之前未发送的旧轮次草稿已作废；请确认内容后重新保存。");
    setDraftId("");
    await load();
    onChanged?.();
  }

  async function release() {
    const epoch = session?.epoch ?? 1;
    const out = await call(`/api/reception/sessions/${encodeURIComponent(sessionId)}/release`, { epoch });
    if (!out) return;
    setNotice("已交回 AI；未发送的人工草稿已作废。");
    setDraftId("");
    await load();
    onChanged?.();
  }

  const replyById = new Map(replies.map((reply) => [reply.id, reply]));
  const turns = [
    ...messages.map((message) => ({ key: message.id, at: message.created_at ?? "", who: "访客", body: message.body, status: "" })),
    ...replies.map((reply) => ({
      key: reply.id,
      at: reply.created_at ?? "",
      who: reply.kind === "human" ? "人工" : reply.kind === "draft" ? "人工草稿" : "AI 草稿",
      body: reply.body,
      status: REPLY_STATUS_TEXT[reply.status ?? ""] ?? reply.status ?? "",
    })),
  ].sort((a, b) => a.at.localeCompare(b.at));

  return (
    <section
      className="conversation-panel"
      aria-label="会话与回复"
      data-conversation-session={sessionId}
      data-conversation-state={!loaded ? "loading" : error ? "error" : session ? "ready" : "empty"}
    >
      <header className="conversation-head">
        <h3>会话与回复</h3>
        <span className="muted">
          {session ? `${modeText(session.mode)} · 轮次 ${session.epoch ?? "未记录"} · 版本 ${session.version ?? "未记录"}` : "正在读取会话"}
          {session?.owner_member_id ? " · 已有负责人" : ""}
        </span>
      </header>

      {!loaded ? (
        <div data-state="loading">
          <p className="muted">正在读取会话和草稿；发送状态以服务端为准。</p>
        </div>
      ) : null}
      {error ? (
        <div data-state="error" role="alert">
          <p className="error">{error}</p>
          <div className="queue-actions">
            <Button variant="secondary" size="sm" type="button" onClick={() => void load()}>
              重试读取
            </Button>
          </div>
        </div>
      ) : null}
      {loaded && !error && !session ? (
        <div data-state="empty">
          <p className="muted">这个会话不在当前工作范围，或已经不存在。</p>
        </div>
      ) : null}

      {session ? (
        <>
          <ol className="conversation-thread" aria-label="会话记录">
            {turns.length === 0 ? (
              <li className="muted">还没有对话内容。新的访客消息会出现在这里。</li>
            ) : (
              turns.map((turn) => (
                <li key={turn.key} className="conversation-turn" data-conversation-turn={turn.key}>
                  <p>
                    <strong>{turn.who}</strong>：{turn.body}
                  </p>
                  <p className="muted">
                    {turn.at}
                    {turn.status ? ` · ${turn.status}` : ""}
                  </p>
                </li>
              ))
            )}
          </ol>

          {!actions.open ? <p className="muted">{actions.blockedReason || "会话已结案。"}</p> : null}
          {actions.open ? (
            <div className="conversation-compose">
              <Textarea
                label="回复内容"
                value={text}
                onChange={(event) => {
                  dirty.current = true;
                  setText(event.target.value);
                  setDraftId("");
                }}
                placeholder={actions.canHumanReply ? "写给这位客户的人工回复" : "先写回复；保存后仍需人工批准才会发送"}
                maxLength={2000}
              />
              {actions.blockedReason ? <p className="muted">{actions.blockedReason}</p> : null}
              <div className="queue-actions">
                <Button variant="secondary" size="sm" type="button" disabled={busy || !text.trim()} onClick={() => void saveDraft()}>
                  保存草稿（不发送）
                </Button>
                {actions.canApprove ? (
                  <Button variant="secondary" size="sm" type="button" disabled={busy || !draftId} onClick={() => void approveDraft(draftId)}>
                    批准草稿
                  </Button>
                ) : null}
                {actions.canSend ? (
                  <Button variant="secondary" size="sm" type="button" disabled={busy} onClick={() => void sendDraft()}>
                    发送回复
                  </Button>
                ) : (
                  <Button
                    variant="secondary"
                    size="sm"
                    type="button"
                    disabled={busy || !text.trim() || !(actions.canApprove || actions.canHumanReply)}
                    onClick={() => void sendDraft()}
                  >
                    批准并发送
                  </Button>
                )}
                {actions.canTakeover ? (
                  <Button variant="secondary" size="sm" type="button" disabled={busy} onClick={() => void takeover()}>
                    转人工接管
                  </Button>
                ) : null}
                {actions.canRelease ? (
                  <Button variant="ghost" size="sm" type="button" disabled={busy} onClick={() => void release()}>
                    交回 AI
                  </Button>
                ) : null}
              </div>
              <p className="muted">
                保存草稿只写入服务端，不会发给访客；发送是不可撤销的交付。轮次变化后旧草稿会自动作废。
              </p>
            </div>
          ) : null}
          {notice ? (
            <p className="inline-note" role="status" data-conversation-notice="true">
              {notice}
            </p>
          ) : null}
          {draftId && replyById.get(draftId) ? (
            <p className="muted" data-conversation-draft={draftId}>
              当前草稿：{REPLY_STATUS_TEXT[replyById.get(draftId)?.status ?? ""] ?? replyById.get(draftId)?.status}
            </p>
          ) : null}
          <p className="muted">
            <Link href={receptionOpenHref(sessionId, focusId)}>在接待工作台打开完整会话</Link>
          </p>
        </>
      ) : null}
    </section>
  );
}
