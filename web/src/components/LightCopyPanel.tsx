"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ToneBadge } from "@/components/workbench/chrome";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { scopeInit, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { failureText, productError } from "@/lib/productShell";
import {
  draftStatus,
  factLabel,
  formatLightCopyFacts,
  presentLightCopy,
  type FormattedLightFact,
  type LightCapability,
} from "@/lib/lightCopy";

interface DraftRow {
  id: string;
  kind: string;
  body: string;
  content_version: number;
  user_confirmed?: boolean;
}

const KINDS = [
  { id: "reply", label: "回复" },
  { id: "email", label: "邮件" },
  { id: "marketing_brief", label: "营销简介" },
] as const;

const TOOLS = [
  { id: "goboost", label: "GoBoost" },
  { id: "product_image", label: "产品图" },
  { id: "digital_human", label: "数字人" },
  { id: "aicut", label: "AiCut" },
] as const;

export function LightCopyPanel({
  subjectKind,
  subjectId,
}: {
  subjectKind: "opportunity" | "campaign";
  subjectId: string;
}) {
  const scope = useCrmScope();
  const [kind, setKind] = useState<(typeof KINDS)[number]["id"]>("reply");
  const [body, setBody] = useState("");
  const [drafts, setDrafts] = useState<DraftRow[]>([]);
  const [note, setNote] = useState(presentLightCopy(null).note);
  const [statusLabel, setStatusLabel] = useState("草稿，未发送");
  const [confirmedState, setConfirmedState] = useState(false);
  const [message, setMessage] = useState("");
  const [facts, setFacts] = useState<FormattedLightFact[]>([]);
  const [selected, setSelected] = useState<string[]>(subjectKind === "campaign" ? ["activity_ref"] : ["title"]);
  const [tool, setTool] = useState<(typeof TOOLS)[number]["id"]>("goboost");
  const [decline, setDecline] = useState(false);
  const [busy, setBusy] = useState(false);
  const [hidden, setHidden] = useState(false);
  const loadGen = useRef({ seq: 0, tenant: "" });

  const base = subjectKind === "campaign" ? `/api/campaigns/${subjectId}/light-copy` : `/api/opportunities/${subjectId}/light-copy`;

  const applyDraft = useCallback((row: DraftRow | undefined) => {
    if (!row) {
      setBody("");
      setStatusLabel(draftStatus(null).label);
      setConfirmedState(false);
      return;
    }
    setBody(row.body);
    setStatusLabel(draftStatus(row).label);
    setConfirmedState(row.user_confirmed === true);
  }, []);

  const load = useCallback(async () => {
    const tenant = scope.tenant ?? "";
    const ticket = { seq: loadGen.current.seq + 1, tenant };
    loadGen.current = ticket;
    if (!subjectId || !tenant) return;
    const init = scopeInit(tenant);
    try {
      const capRes = await fetch("/api/light-copy/capability", init);
      if (loadGen.current !== ticket) return;
      // HUI-2626 盲评 P2①：capability 关闭时是 200+enabled:false，面板据此隐藏；
      // 404 兜底保留（旧服务端契约）。不再让探测打进 console。
      if (capRes.status === 404) {
        setHidden(true);
        return;
      }
      const cap = (await capRes.json().catch(() => null)) as LightCapability | null;
      if (loadGen.current !== ticket) return;
      if (cap && cap.enabled === false) {
        setHidden(true);
        return;
      }
      setHidden(false);
      setNote(presentLightCopy(cap).note);
      const listRes = await fetch(base, init);
      if (loadGen.current !== ticket) return;
      if (listRes.status === 404) {
        setMessage(subjectKind === "campaign" ? "当前线索不是活动，轻文案只使用商机或碰一碰活动。" : "找不到这条商机。");
        setDrafts([]);
        return;
      }
      if (!listRes.ok) {
        const errBody = await listRes.json().catch(() => null);
        if (loadGen.current !== ticket) return;
        setMessage(failureText(errBody, listRes.status));
        return;
      }
      const list = await listRes.json();
      if (loadGen.current !== ticket) return;
      const items = (list.items ?? []) as DraftRow[];
      setDrafts(items);
      applyDraft(items.find((item) => item.kind === kind));
    } catch (e) {
      if (loadGen.current !== ticket) return;
      setMessage(productError(e instanceof Error ? e.message : "轻文案加载失败"));
      setDrafts([]);
    }
  }, [applyDraft, base, kind, scope.tenant, subjectId, subjectKind]);

  useEffect(() => {
    setDrafts([]);
    setBody("");
    setFacts([]);
    setMessage("");
    setConfirmedState(false);
    setStatusLabel("草稿，未发送");
    void load();
  }, [load, scope.epoch]);

  function toggleFact(key: string) {
    setSelected((cur) => (cur.includes(key) ? cur.filter((item) => item !== key) : [...cur, key]));
  }

  async function call(path: string, method: string, payload: Record<string, unknown>) {
    const tenant = scope.tenant ?? "";
    const ticket = loadGen.current;
    setBusy(true);
    setMessage("");
    try {
      const res = await fetch(path, scopeInit(tenant, {
        method,
        headers: { "content-type": "application/json" },
        body: JSON.stringify(payload),
      }));
      const data = await res.json().catch(() => null);
      if (loadGen.current !== ticket) return null;
      if (!res.ok) {
        setMessage(failureText(data, res.status));
        return null;
      }
      return data;
    } catch (err) {
      if (loadGen.current === ticket) {
        setMessage(productError(err instanceof Error ? err.message : "请求失败"));
      }
      return null;
    } finally {
      if (loadGen.current === ticket) setBusy(false);
    }
  }

  if (hidden) return null;

  const factChoices = subjectKind === "campaign" ? ["activity_ref"] : ["title", "price_cents"];

  return (
    <section className="bg-card text-card-foreground border border-border rounded-[10px] p-5 mb-4">
      <h2>轻文案</h2>
      <p className="muted">{note}</p>
      <p className="flex items-center gap-2">
        <ToneBadge tone={confirmedState ? "human" : "ai"} />
        <span>{statusLabel}</span>
      </p>
      <div className="queue-actions">
        {KINDS.map((item) => (
          <Button
            key={item.id}
            type="button"
            variant="secondary"
            size="sm"
            disabled={busy}
            onClick={() => {
              setKind(item.id);
              applyDraft(drafts.find((row) => row.kind === item.id));
            }}
          >
            {item.label}
          </Button>
        ))}
      </div>
      <div className="public-form-field">
        <Label htmlFor="light-copy-body">草稿</Label>
        <Textarea id="light-copy-body" value={body} rows={5} onChange={(e) => setBody(e.target.value)} />
      </div>
      <div className="queue-actions">
        <Button
          type="button"
          size="sm"
          disabled={busy || !body.trim()}
          onClick={async () => {
            const saved = await call(`${base}/${kind}`, "PUT", { body });
            if (saved?.body) {
              setStatusLabel(draftStatus(saved).label);
              setMessage("已保存。尚未发送。");
              void load();
            }
          }}
        >
          保存草稿
        </Button>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          disabled={busy}
          onClick={async () => {
            await call(`${base}/${kind}/generate`, "POST", { selected });
          }}
        >
          生成
        </Button>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          disabled={busy}
          onClick={async () => {
            const confirmed = await call(`${base}/${kind}/confirm`, "POST", {});
            if (confirmed?.user_confirmed) {
              setStatusLabel(draftStatus(confirmed).label);
              setConfirmedState(true);
            }
          }}
        >
          确认草稿（不会发送）
        </Button>
      </div>
      <div className="public-form-field">
        <span className="muted">交给后续工具的事实：</span>
        {factChoices.map((key) => (
          <Label key={key} htmlFor={`fact-${key}`} className="flex items-center gap-2 font-normal">
            <Checkbox
              id={`fact-${key}`}
              checked={selected.includes(key)}
              onCheckedChange={() => toggleFact(key)}
            />
            {factLabel(key)}
          </Label>
        ))}
        <Button
          type="button"
          variant="secondary"
          size="sm"
          disabled={busy}
          onClick={async () => {
            const data = await call(`${base}/${kind}/facts`, "POST", { selected });
            if (data?.facts) setFacts(formatLightCopyFacts(data.facts));
            else setFacts([]);
          }}
        >
          预览事实
        </Button>
      </div>
      {facts.length > 0 ? (
        <ul data-facts="chips" aria-label="已选事实预览">
          {facts.map((item) => (
            <li key={item.key}>
              <span>{item.label}</span>
              <strong>{item.value}</strong>
            </li>
          ))}
        </ul>
      ) : null}
      <div className="public-form-field">
        <Label htmlFor="light-copy-tool">专业工具</Label>
        <Select value={tool} onValueChange={(v) => setTool(v as (typeof TOOLS)[number]["id"])}>
          <SelectTrigger id="light-copy-tool" className="w-full sm:w-64">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {TOOLS.map((item) => (
              <SelectItem key={item.id} value={item.id}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Label htmlFor="light-copy-decline" className="flex items-center gap-2 font-normal">
          <Checkbox id="light-copy-decline" checked={decline} onCheckedChange={setDecline} />
          不使用专业工具
        </Label>
        <Button
          type="button"
          size="sm"
          disabled={busy}
          onClick={async () => {
            const data = await call(`${base}/${kind}/handoff`, "POST", {
              tool,
              idempotency_key: `${subjectId}:${kind}`,
              decline,
              selected,
            });
            if (data?.status) {
              setMessage(
                data.status === "declined"
                  ? "已拒绝专业工具。草稿仍可保存和手改。"
                  : `${data.reason ?? data.status}。没有新建制作工程，也没有重新生成。`,
              );
            }
          }}
        >
          交接
        </Button>
      </div>
      {message ? <p role="alert" className="muted">{message}</p> : null}
    </section>
  );
}
