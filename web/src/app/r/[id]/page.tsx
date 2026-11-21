"use client";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { Separator } from "@/components/ui/separator";

import { useParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { MODE_TEXT, PENDING_TEXT, RECEPTION_GREETING, newVisitorKey } from "@/lib/reception";
import { publicFailureText } from "@/lib/productShell";

// 自有 H5 接待（HUI-1688）。只打本站 BFF。访客钥匙留在本机，不注册账号。

// 访客页错误收口：服务端英文 message/HTTP 码不直出（BFF 未配置映射「服务暂不可用」，
// 见 publicFailureText），其余失败统一这句。
const ACTION_FAILURE_TEXT = "操作没有完成，请稍后重试";

interface PublicWidget {
  id: string;
  language: string;
  greeting: string;
}

interface PublicSession {
  id: string;
  mode: string;
  status: string;
  pending_reason?: string;
  language: string;
}

interface TranscriptReply {
  id: string;
  body: string;
  status: string;
  created_at?: string;
  citations?: Array<{ source_id?: string; version?: number; kind?: string }>;
  gaps?: Array<{ code?: string }>;
}

interface TranscriptMessage {
  id: string;
  body: string;
  created_at?: string;
}

const GAP_TEXT: Record<string, string> = {
  fact_missing: "没有实时依据",
  fact_expired: "依据已过期",
  fact_timeout: "实时接口超时",
  unknown: "知识不足",
  untrusted_input: "已忽略注入指令",
  unapproved_tool: "不能执行写入",
  model_unavailable: "模型不可用",
};

// 初始化骨架：拿到会话前不渲染任何业务主体（默认接待模式、空对话提示、禁用输入都不出现）。
function ReceptionSkeleton() {
  return (
    <main className="chat-shell" role="status" aria-label="正在连接接待">
      <Skeleton className="h-6 w-28" />
      <Skeleton className="h-4 w-3/4" />
      <div className="chat-meta">
        <Skeleton className="h-5 w-16 rounded-md" />
        <Skeleton className="h-5 w-16 rounded-md" />
      </div>
      <Card className="chat-log card">
        <CardContent className="space-y-3">
          <Skeleton className="h-14 w-3/4" />
          <Skeleton className="h-14 w-2/3" />
          <Skeleton className="h-14 w-4/5" />
        </CardContent>
      </Card>
      <div className="composer">
        <Skeleton className="h-9 flex-1" />
        <Skeleton className="h-9 w-20" />
      </div>
    </main>
  );
}

export default function ReceptionH5Page() {
  const params = useParams<{ id: string }>();
  const widgetID = typeof params?.id === "string" ? params.id : "";
  const [widget, setWidget] = useState<PublicWidget | null>(null);
  const [session, setSession] = useState<PublicSession | null>(null);
  const [messages, setMessages] = useState<TranscriptMessage[]>([]);
  const [replies, setReplies] = useState<TranscriptReply[]>([]);
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [refreshError, setRefreshError] = useState("");
  const [sending, setSending] = useState(false);
  const [loading, setLoading] = useState(true);
  const [bootReload, setBootReload] = useState(0);

  const visitorKey = useCallback(() => {
    const storageKey = "reception-visitor:" + widgetID;
    const existing = window.sessionStorage.getItem(storageKey);
    if (existing) return existing;
    const created = newVisitorKey();
    window.sessionStorage.setItem(storageKey, created);
    return created;
  }, [widgetID]);

  const loadTranscript = useCallback(async (sessionID: string, key: string, quiet = false) => {
    const res = await fetch(`/api/public/reception/sessions/${sessionID}?visitor_key=${encodeURIComponent(key)}`);
    const body = await res.json();
    if (!res.ok) {
      const message = publicFailureText(body, ACTION_FAILURE_TEXT);
      if (quiet) {
        // 后台轮询失败不打断对话，但要让访客看到刷新未成功，避免一直看到过期内容。
        setRefreshError(message);
      } else {
        setError(message);
      }
      return;
    }
    setRefreshError("");
    setSession(body.session);
    setMessages(body.messages ?? []);
    setReplies(body.replies ?? []);
  }, []);

  useEffect(() => {
    if (!session?.id) return;
    const timer = window.setInterval(() => {
      void loadTranscript(session.id, visitorKey(), true).catch(() => setRefreshError(ACTION_FAILURE_TEXT));
    }, 2500);
    return () => window.clearInterval(timer);
  }, [session?.id, visitorKey, loadTranscript]);

  useEffect(() => {
    if (!widgetID) {
      setLoading(false);
      setError("接待链接无效，请核对后重试。");
      return;
    }
    let cancelled = false;
    setLoading(true);
    (async () => {
      setError("");
      const widgetRes = await fetch(`/api/public/reception/widgets/${widgetID}`);
      const widgetBody = await widgetRes.json();
      if (!widgetRes.ok) {
        if (!cancelled) setError(publicFailureText(widgetBody, ACTION_FAILURE_TEXT));
        return;
      }
      if (cancelled) return;
      setWidget(widgetBody);
      const key = visitorKey();
      const openRes = await fetch(`/api/public/reception/widgets/${widgetID}/sessions`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ visitor_key: key }),
      });
      const openBody = await openRes.json();
      if (!openRes.ok) {
        if (!cancelled) setError(publicFailureText(openBody, ACTION_FAILURE_TEXT));
        return;
      }
      if (cancelled) return;
      setSession(openBody.session);
      await loadTranscript(openBody.session.id, key);
    })().catch(() => { if (!cancelled) setError(ACTION_FAILURE_TEXT); }).finally(() => { if (!cancelled) setLoading(false); });
    return () => {
      cancelled = true;
    };
  }, [widgetID, visitorKey, loadTranscript, bootReload]);

  const send = async () => {
    if (!session || session.status === "closed" || !text.trim()) return;
    setSending(true);
    setError("");
    try {
      const key = visitorKey();
      const res = await fetch(`/api/public/reception/sessions/${session.id}/messages`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          visitor_key: key,
          client_msg_id: "web-" + Date.now().toString(36) + "-" + Math.random().toString(16).slice(2, 8),
          text: text.trim(),
        }),
      });
      const body = await res.json();
      if (!res.ok) {
        setError(publicFailureText(body, ACTION_FAILURE_TEXT));
        return;
      }
      setText("");
      if (body.session) setSession(body.session);
      await loadTranscript(session.id, key);
    } catch {
      // 网络层异常(如 fetch 失败)不向访客透出浏览器英文 message。
      setError(ACTION_FAILURE_TEXT);
    } finally {
      setSending(false);
    }
  };

  // 初始化加载完成前：只出骨架，不渲染业务主体。
  if (loading) return <ReceptionSkeleton />;

  // 初始化失败（未取得会话）：可见错误 + 重试，不放接待模式/对话/输入。
  if (!session) {
    return (
      <main className="chat-shell">
        <p className="muted"><a href="/">返回商家主页</a></p>
        <h1>在线接待</h1>
        <Alert variant="destructive" role="alert">
          <AlertTitle>接待没有载入</AlertTitle>
          <AlertDescription>{error || ACTION_FAILURE_TEXT}</AlertDescription>
        </Alert>
        <div className="queue-actions">
          <Button variant="secondary" type="button" onClick={() => setBootReload((n) => n + 1)}>
            重试
          </Button>
        </div>
      </main>
    );
  }

  const mode = session.mode;
  const closed = session.status === "closed";
  const turns = [
    ...messages.map((message) => ({ key: message.id, side: "visitor" as const, at: message.created_at ?? "", body: message.body, reply: undefined as TranscriptReply | undefined })),
    ...replies.filter((reply) => reply.status === "sent" || reply.status === "generated").map((reply) => ({ key: reply.id, side: "shop" as const, at: reply.created_at ?? "", body: reply.body, reply })),
  ].sort((a, b) => a.at.localeCompare(b.at));
  const isEmpty = messages.length === 0 && replies.filter((reply) => reply.status === "sent" || reply.status === "generated").length === 0;

  return (
    <main className="chat-shell">
      <p className="muted"><a href="/">返回商家主页</a></p>
      <h1>在线接待</h1>
      <p className="muted">{widget?.greeting || RECEPTION_GREETING}</p>
      <div className="chat-meta">
        <Badge variant="secondary">{MODE_TEXT[mode] ?? mode}</Badge>
        {closed ? <Badge variant="outline">已结案</Badge> : null}
        {session?.pending_reason ? <Badge variant="destructive">{PENDING_TEXT[session.pending_reason] ?? session.pending_reason}</Badge> : null}
      </div>
      {error ? (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : refreshError ? (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{refreshError}</AlertDescription>
        </Alert>
      ) : null}
      <Card className="chat-log card" aria-live="polite" aria-label="对话记录">
        <CardContent className="space-y-3">
          {turns.map((turn) => (
            <div key={turn.key} className={turn.side === "visitor" ? "bubble visitor" : "bubble shop"}>
              <p>{turn.body}</p>
              {turn.reply?.citations?.length ? (
                <p className="fine">已依据店内知识（{turn.reply.citations.length} 条）</p>
              ) : null}
              {turn.reply?.gaps?.length ? (
                <p className="fine">{turn.reply.gaps.map((gap) => GAP_TEXT[gap.code ?? ""] ?? gap.code).join(" · ")}</p>
              ) : null}
            </div>
          ))}
          {isEmpty ? <p className="muted">可以直接提问。价格和订单不会凭文档编造。</p> : null}
        </CardContent>
      </Card>
      <form className="composer" onSubmit={(event) => { event.preventDefault(); void send(); }}>
        <Input
          value={text}
          onChange={(event) => setText(event.target.value)}
          placeholder="输入问题"
          aria-label="输入问题"
          maxLength={2000}
          disabled={closed || sending}
        />
        <Button type="submit" disabled={sending || closed || !text.trim()}>
          {sending ? "发送中" : "发送"}
        </Button>
        <Button
          type="button"
          variant="outline"
          onClick={() => {
            void loadTranscript(session.id, visitorKey()).catch(() => setError(ACTION_FAILURE_TEXT));
          }}
        >
          刷新对话
        </Button>
      </form>
      <Separator className="my-3" />
      <p className="muted">{closed ? "该会话已结案，如有新问题请重新发起咨询。" : "价格和订单以实时核对为准。"}</p>
    </main>
  );
}
