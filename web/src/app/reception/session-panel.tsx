"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ToneBadge } from "@/components/workbench/chrome";
import { humanTime } from "@/lib/humanize";
import { failureText, listTenantHeader, productError } from "@/lib/productShell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { DateTimeField } from "@/components/workbench/dateTimeField";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  MODE_TEXT,
  REPLY_STATUS_TEXT,
  approveCall,
  assistCall,
  closeCall,
  followUpCall,
  followUpSubmitAllowed,
  humanReplyCall,
  leadCall,
  leadSubmitAllowed,
  newStaffToken,
  nextFollowUpRFC3339,
  releaseCall,
  replyApprovable,
  replySendable,
  sendCall,
  takeoverCall,
  type DeskCall,
  type LeadFields,
} from "@/lib/reception";

interface SessionView {
  id: string;
  mode: string;
  epoch: number;
  version: number;
  status: string;
  owner_member_id?: string;
  owner_label?: string;
  lead_id?: string;
  contact_id?: string;
  pending_reason?: string;
}

interface ReplyView {
  id: string;
  epoch: number;
  session_version?: number;
  kind: string;
  body: string;
  status: string;
  created_at?: string;
}

interface ModelView {
  task_id?: string;
  cost_cents?: number;
  billing_verdict?: string;
}

interface MessageView {
  id: string;
  body: string;
  created_at?: string;
}

const EMPTY_LEAD: LeadFields = {
  purpose: "",
  allowContact: false,
  contactName: "",
  phone: "",
  noticeVersion: "",
};

const PURPOSE_OPTIONS = [
  { value: "sales_followup", label: "销售跟进" },
  { value: "after_sales", label: "售后" },
  { value: "general_qa", label: "一般问答" },
];

export default function SessionPanel({
  tenant,
  sessionId,
  onChanged,
}: {
  tenant: string;
  sessionId: string;
  onChanged: () => void;
}) {
  const [session, setSession] = useState<SessionView | null>(null);
  const [messages, setMessages] = useState<MessageView[]>([]);
  const [replies, setReplies] = useState<ReplyView[]>([]);
  const [model, setModel] = useState<ModelView | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [humanText, setHumanText] = useState("");
  const [lead, setLead] = useState<LeadFields>(EMPTY_LEAD);
  const [note, setNote] = useState("");
  const [nextAt, setNextAt] = useState("");
  const loadGen = useRef({ seq: 0, tenant: "", sessionId: "" });

  const load = useCallback(async () => {
    const ticket = { seq: loadGen.current.seq + 1, tenant, sessionId };
    loadGen.current = ticket;
    const res = await fetch(`/api/reception/sessions/${sessionId}`, {
      headers: listTenantHeader(tenant) ?? {},
    });
    const body = await res.json().catch(() => ({}));
    if (loadGen.current !== ticket) return;
    if (!res.ok) {
      setError(failureText(body, res.status));
      setSession(null);
      return;
    }
    setSession(body.session ? { ...body.session, owner_label: typeof body.owner_label === "string" ? body.owner_label : body.session.owner_label } : body.session);
    setMessages(body.messages ?? []);
    setReplies(body.replies ?? []);
    setModel(body.model ?? null);
  }, [sessionId, tenant]);

  useEffect(() => {
    setSession(null);
    setMessages([]);
    setReplies([]);
    setModel(null);
    setError("");
    setLead(EMPTY_LEAD);
    setNote("");
    setNextAt("");
    setHumanText("");
    void load().catch((e: Error) => setError(productError(e.message)));
  }, [load]);

  async function run(call: DeskCall | null, refused: string): Promise<boolean> {
    if (!call) {
      setError(refused);
      return false;
    }
    const ticket = loadGen.current;
    setBusy(true);
    setError("");
    try {
      const res = await fetch(call.path, {
        method: call.method,
        headers: { "content-type": "application/json", ...listTenantHeader(tenant) },
        body: JSON.stringify(call.body),
      });
      const body = await res.json().catch(() => ({}));
      if (loadGen.current !== ticket) return false;
      if (!res.ok) {
        setError(failureText(body, res.status));
        return false;
      }
      await load();
      onChanged();
      return true;
    } catch (e) {
      if (loadGen.current === ticket) setError(e instanceof Error ? productError(e.message) : "操作失败");
      return false;
    } finally {
      if (loadGen.current === ticket) setBusy(false);
    }
  }

  async function sendHuman() {
    if (!session) return;
    const clientReplyId = newStaffToken("reply-");
    const created = humanReplyCall(session.id, clientReplyId, humanText);
    if (!created) {
      setError("先写人工回复");
      return;
    }
    const ticket = loadGen.current;
    setBusy(true);
    setError("");
    try {
      const res = await fetch(created.path, {
        method: "POST",
        headers: { "content-type": "application/json", ...listTenantHeader(tenant) },
        body: JSON.stringify(created.body),
      });
      const body = await res.json().catch(() => ({}));
      if (loadGen.current !== ticket) return;
      if (!res.ok) {
        setError(failureText(body, res.status));
        return;
      }
      const replyId = body.reply?.id as string | undefined;
      if (!replyId) {
        setError("服务端没有返回回复");
        return;
      }
      const send = sendCall(session.id, replyId, newStaffToken("rcpt-"));
      const sent = await fetch(send!.path, {
        method: "POST",
        headers: { "content-type": "application/json", ...listTenantHeader(tenant) },
        body: JSON.stringify(send!.body),
      });
      const sentBody = await sent.json().catch(() => ({}));
      if (loadGen.current !== ticket) return;
      if (!sent.ok) {
        setError(failureText(sentBody, sent.status));
        await load();
        return;
      }
      setHumanText("");
      await load();
      onChanged();
    } catch (e) {
      if (loadGen.current === ticket) setError(e instanceof Error ? productError(e.message) : "发送失败");
    } finally {
      if (loadGen.current === ticket) setBusy(false);
    }
  }

  // 载入中：骨架占位，不渲染会话主体。
  if (!session && !error) {
    return (
      <Card aria-label="会话载入中" role="status">
        <CardContent className="space-y-3">
          <Skeleton className="h-6 w-1/3" />
          <Skeleton className="h-4 w-2/3" />
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </CardContent>
      </Card>
    );
  }

  // 载入失败：可见错误，不静默禁用输入。
  if (!session) {
    return (
      <Card>
        <CardContent className="space-y-3">
          <Alert variant="destructive" role="alert">
            <AlertDescription>{error || "会话没有载入，请稍后重试。"}</AlertDescription>
          </Alert>
          <Button variant="secondary" type="button" onClick={() => void load().catch((e: Error) => setError(productError(e.message)))}>
            重试
          </Button>
        </CardContent>
      </Card>
    );
  }

  const open = session.status === "open";
  const turns = [
    ...messages.map((message) => ({ key: message.id, at: message.created_at ?? "", who: "访客", tone: "human" as const, body: message.body })),
    ...replies.map((reply) => ({
      key: reply.id,
      at: reply.created_at ?? "",
      who: reply.kind === "human" ? "人工" : "草稿",
      tone: reply.kind === "human" ? ("human" as const) : ("ai" as const),
      body: reply.body,
    })),
  ].sort((a, b) => a.at.localeCompare(b.at));

  return (
    <Card aria-label="会话操作">
      <CardHeader>
        <CardTitle>
          会话 <code>{session.id}</code>
        </CardTitle>
        <div className="muted">
          <Badge variant="secondary">{MODE_TEXT[session.mode] ?? session.mode}</Badge>
          {" · "}轮次 {session.epoch} · 版本 {session.version} · {open ? "进行中" : "已结案"}
          {session.owner_member_id ? ` · 负责人 ${session.owner_label || "已接管（负责人待同步）"}` : ""}
          {session.lead_id ? ` · 线索 ${session.lead_id.slice(0, 12)}…` : " · 尚未受权留资"}
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {error ? (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}
        <div className="chat-log" aria-label="会话记录">
          {turns.map((turn) => (
            <div key={turn.key} className="bubble shop" data-tone={turn.tone}>
              <p className="flex items-center gap-2">
                <ToneBadge tone={turn.tone} />
                <strong>{turn.who}</strong>
                {turn.at ? <span className="muted">{humanTime(turn.at)}</span> : null}
              </p>
              <p>{turn.body}</p>
            </div>
          ))}
          {turns.length === 0 ? <p className="muted">还没有对话。</p> : null}
        </div>
        <ul className="desk-list">
          {replies.map((reply) => {
            const stale = reply.epoch !== session.epoch && reply.status !== "sent";
            const replyTone = reply.kind === "human" ? "human" : "ai";
            return (
              <li key={reply.id} data-tone={replyTone}>
                <ToneBadge tone={replyTone} />{" "}
                <span>{REPLY_STATUS_TEXT[reply.status] ?? reply.status}</span>
                <span> · 轮次 {reply.epoch} · 版本 {reply.session_version ?? ""}</span>
                {stale ? <span> · 旧轮次，文本和音频都不能再发送</span> : null}
                <div className="queue-actions">
                  {replyApprovable(session.mode, reply.epoch, session.epoch, reply.status) ? (
                    <Button variant="secondary" size="sm" type="button" disabled={busy} onClick={() => void run(approveCall(session.id, reply.id), "不能批准")}>批准</Button>
                  ) : null}
                  {replySendable(reply.epoch, session.epoch, reply.status) ? (
                    <Button variant="secondary" size="sm" type="button" disabled={busy} onClick={() => void run(sendCall(session.id, reply.id, newStaffToken("rcpt-")), "不能发送")}>发送</Button>
                  ) : null}
                </div>
              </li>
            );
          })}
        </ul>
        <p className="muted">
          {model?.billing_verdict === "recorded" && model.task_id
            ? `任务 ${model.task_id} · 成本 ${model.cost_cents ?? 0} 分`
            : "文本降级，真实模型未完成"}
        </p>
        <div className="queue-actions">
          {open && session.mode === "ai" ? (
            <Button variant="secondary" size="sm" type="button" disabled={busy} onClick={() => void run(assistCall(session.id, session.epoch), "还没有服务端轮次")}>改为协助</Button>
          ) : null}
          {open && session.mode !== "human" ? (
            <Button variant="default" size="sm" type="button" disabled={busy} onClick={() => void run(takeoverCall(session.id, session.epoch), "还没有服务端轮次")}>接管</Button>
          ) : null}
          {open && session.mode === "human" ? (
            <Button variant="secondary" size="sm" type="button" disabled={busy} onClick={() => void run(releaseCall(session.id, session.epoch), "还没有服务端轮次")}>交回 AI</Button>
          ) : null}
          {open ? <Button variant="secondary" size="sm" type="button" disabled={busy} onClick={() => void run(closeCall(session.id), "不能结案")}>结案</Button> : null}
        </div>
        {open && session.mode === "human" ? (
          <form className="stack-form" onSubmit={(event) => { event.preventDefault(); void sendHuman(); }}>
            <Label htmlFor="human-reply">人工回复</Label>
            <Textarea
              id="human-reply"
              aria-label="人工回复"
              value={humanText}
              onChange={(event) => setHumanText(event.target.value)}
              maxLength={2000}
              disabled={busy}
            />
            <Button variant="default" size="sm" type="submit" disabled={busy || humanText.trim() === ""}>发送人工回复</Button>
          </form>
        ) : null}
        {open && !session.lead_id ? (
          <form className="stack-form" onSubmit={(event) => { event.preventDefault(); void run(leadCall(session.id, lead), "售后和一般问答不建线索"); }}>
            <h3>受权留资</h3>
            <p className="muted">售后和一般问答不建线索。销售跟进要由你勾选允许联系，并填写姓名、电话和告知版本。</p>
            <div className="public-form-field">
              <Label htmlFor="lead-purpose">目的</Label>
              <Select
                value={lead.purpose || undefined}
                onValueChange={(value) => setLead({ ...lead, purpose: value })}
              >
                <SelectTrigger id="lead-purpose" aria-label="目的">
                  <SelectValue placeholder="请选择" />
                </SelectTrigger>
                <SelectContent>
                  {PURPOSE_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Label htmlFor="lead-contact" className="flex items-center gap-2 font-normal">
              <Checkbox
                id="lead-contact"
                checked={lead.allowContact}
                onCheckedChange={(v) => setLead({ ...lead, allowContact: v })}
              />
              允许联系
            </Label>
            <div className="public-form-field">
              <Label htmlFor="lead-name">姓名</Label>
              <Input id="lead-name" aria-label="姓名" value={lead.contactName} onChange={(event) => setLead({ ...lead, contactName: event.target.value })} disabled={busy} />
            </div>
            <div className="public-form-field">
              <Label htmlFor="lead-phone">电话</Label>
              <Input id="lead-phone" aria-label="电话" value={lead.phone} onChange={(event) => setLead({ ...lead, phone: event.target.value })} disabled={busy} />
            </div>
            <div className="public-form-field">
              <Label htmlFor="lead-notice">告知版本</Label>
              <Input id="lead-notice" aria-label="告知版本" value={lead.noticeVersion} onChange={(event) => setLead({ ...lead, noticeVersion: event.target.value })} placeholder="例如 reception-notice-v1" disabled={busy} />
            </div>
            <Button variant="default" size="sm" type="submit" disabled={busy || !leadSubmitAllowed(lead)}>建立受权线索</Button>
          </form>
        ) : null}
        {session.lead_id ? (
          <form className="stack-form" onSubmit={async (event) => {
            event.preventDefault();
            const next = nextFollowUpRFC3339(nextAt);
            if (next === null) {
              setError("下一次跟进时间无效");
              return;
            }
            const ok = await run(followUpCall(session.id, session.lead_id, note, next), "跟进要先有受权线索");
            if (ok) {
              setNote("");
              setNextAt("");
            }
          }}>
            <h3>跟进</h3>
            <Label htmlFor="follow-note">跟进备注</Label>
            <Textarea id="follow-note" aria-label="跟进备注" value={note} onChange={(event) => setNote(event.target.value)} disabled={busy} />
            <DateTimeField label="下一次跟进" id="follow-next" value={nextAt} onChange={setNextAt} disabled={busy} />
            <Button variant="default" size="sm" type="submit" disabled={busy || !followUpSubmitAllowed(session.lead_id, note)}>记录跟进</Button>
          </form>
        ) : <p className="muted">跟进要先有受权线索。</p>}
      </CardContent>
    </Card>
  );
}
