"use client";

import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { SurfaceState } from "@/components/workbench/chrome";
import { RendererSelect } from "@/components/workbench/rendererSelect";
import { Button, Input, Textarea } from "@/vendor/painuo/react/v1/src/index";
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, domainFailureText, failureText, listTenantHeader, pagePrimary, productError } from "@/lib/productShell";
import { presentSOP, recordLabel, type SOPCapability } from "@/lib/sopReach";
import {
  consentChoices,
  contactChoices,
  sopSubmit,
  type ScopeConsent,
  type ScopeContact,
} from "@/lib/scopePick";

interface SOPAction {
  id: string;
  kind: string;
  status: string;
  purpose: string;
  channel: string;
  body?: string;
  refusal?: string;
  delivered?: boolean;
}

const EMPTY_VIEW = presentSOP(null);

function consentLabel(item: ScopeConsent): string {
  const purpose = item.purpose || "授权";
  return item.source_channel ? `${purpose} · ${item.source_channel}` : purpose;
}

// ⑧ vendored renderer 控件的固定选项（≤430 触控密度随 rendererDensity 生效）。
const CHANNEL_CHOICES = [
  { value: "sms", label: "短信" },
  { value: "email", label: "邮件" },
  { value: "wecom", label: "企微" },
] as const;

export default function SOPPage() {
  const scope = useCrmScope();
  const tenant = scope.tenant ?? "";
  const [view, setView] = useState(EMPTY_VIEW);
  const [items, setItems] = useState<SOPAction[] | null>(null);
  const [error, setError] = useState("");
  const [contacts, setContacts] = useState<ScopeContact[] | null>(null);
  const [contactId, setContactId] = useState("");
  const [consents, setConsents] = useState<ScopeConsent[] | null>(null);
  const [consentId, setConsentId] = useState("");
  const [channel, setChannel] = useState("sms");
  const [recipient, setRecipient] = useState("");
  const [purpose, setPurpose] = useState("follow_up");
  const [body, setBody] = useState("");
  const [draftId, setDraftId] = useState("");
  const [busy, setBusy] = useState(false);
  const loadSeq = useRef(0);
  const consentSeq = useRef(0);
  const tenantRef = useRef(tenant);
  tenantRef.current = tenant;

  const headers = useCallback(
    () => ({ "content-type": "application/json", ...listTenantHeader(tenant) }),
    [tenant],
  );

  const load = useCallback(async (tenant: string, seq: number) => {
    setError("");
    setItems(null);
    setView(EMPTY_VIEW);
    if (!tenant.trim()) {
      setItems([]);
      setContacts([]);
      return;
    }
    const h = listTenantHeader(tenant.trim()) ?? {};
    try {
      const [capRes, listRes, contactRes] = await Promise.all([
        fetch("/api/sop/capability", { headers: h }),
        fetch("/api/sop/actions", { headers: h }),
        fetch("/api/contacts", { headers: h }),
      ]);
      if (loadSeq.current !== seq) return;
      // ⑥ flag 域初次加载：解析失败（mux text/plain 404）按「功能未开通」指引，
      // 记录级 404（fail JSON）保持原「记录不存在」语义。
      const capBody = (await capRes.json().catch(() => null)) as (SOPCapability & { message?: string; error?: string }) | null;
      const listBody = await listRes.json().catch(() => null);
      setView(presentSOP(capBody));
      let nextContacts: ScopeContact[] = [];
      if (contactRes.ok) {
        const contactBody = await contactRes.json().catch(() => null);
        nextContacts = contactChoices(contactBody?.items);
      }
      setContacts(nextContacts);
      if (!listRes.ok) {
        setError(domainFailureText(listRes.status, listBody));
        setItems([]);
        return;
      }
      if (!capRes.ok) setError(domainFailureText(capRes.status, capBody));
      setItems(((listBody?.items as SOPAction[]) ?? []));
    } catch (e) {
      if (loadSeq.current !== seq) return;
      setError(e instanceof Error ? productError(e.message) : "加载失败");
      setItems([]);
      setContacts([]);
    }
  }, []);

  useEffect(() => {
    const seq = ++loadSeq.current;
    consentSeq.current += 1;
    setContactId("");
    setConsentId("");
    setConsents(null);
    setContacts(null);
    setDraftId("");
    setBusy(false);
    void load(scope.tenant ?? "", seq);
  }, [scope.epoch, scope.tenant, load]);

  async function loadConsents(tenant: string, id: string) {
    const seq = ++consentSeq.current;
    setConsentId("");
    setConsents(null);
    if (!tenant.trim() || !id) {
      setPurpose("follow_up");
      return;
    }
    try {
      const res = await fetch(`/api/contacts/${encodeURIComponent(id)}/consents`, {
        headers: listTenantHeader(tenant.trim()) ?? {},
      });
      const body = await res.json().catch(() => null);
      if (consentSeq.current !== seq) return;
      const choices = res.ok ? consentChoices(body?.items) : [];
      setConsents(choices);
      if (choices.length === 0) setPurpose("follow_up");
    } catch {
      if (consentSeq.current !== seq) return;
      setConsents([]);
      setPurpose("follow_up");
    }
  }

  async function post(path: string, payload: object) {
    const tenantAtSubmit = tenant;
    const seqAtSubmit = loadSeq.current;
    setBusy(true);
    setError("");
    try {
      const res = await fetch(path, { method: "POST", headers: headers(), body: JSON.stringify(payload) });
      const data = await res.json();
      if (tenantRef.current !== tenantAtSubmit || loadSeq.current !== seqAtSubmit) return data as { id?: string };
      if (!res.ok) {
        setError(failureText(data, res.status));
        return data as { id?: string };
      }
      if (data.kind === "pending_draft" && typeof data.id === "string" && data.id.trim()) setDraftId(data.id);
      await load(tenantAtSubmit, seqAtSubmit);
      return data as { id?: string; kind?: string };
    } catch (e) {
      if (tenantRef.current === tenantAtSubmit) setError(e instanceof Error ? productError(e.message) : "提交失败");
      return {};
    } finally {
      if (tenantRef.current === tenantAtSubmit) setBusy(false);
    }
  }

  const selected = contacts?.find((item) => item.id === contactId) ?? null;
  const selectedConsent = consents?.find((item) => item.id === consentId) ?? null;
  const payload = sopSubmit({
    contact: selected,
    purpose,
    consent: selectedConsent,
    channel,
    recipient,
    body,
  });
  const marketingOpen = (consents?.length ?? 0) > 0;

  return (
    <main data-page="sop">
      <header className="page-head">
        <h1>下一次跟进</h1>
        <Link className="btn" href="/">我的工作</Link>
      </header>
      {!tenant ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      <div className="card">
        <p data-tone="automation">{view.note}</p>
        <p className="muted">
          <span data-tone="automation">自动化状态</span> 渠道状态：{view.verified ? "已接通" : "未验证"}。无人值守：{view.unattended ? "已明确开启" : "关闭"}。
          这里只做内部提醒、回复草稿和人工确认，不会自动发短信、邮件或企微，也不会扣费。
        </p>
      </div>
      <div className="card">
        <h2>提醒和草稿</h2>
        {/* ⑧ 原生 select/textarea 换 vendored renderer 控件：交互语义与选项不变，
            ≤430 触控密度（rendererDensity）继续生效。 */}
        <RendererSelect
          label="客户"
          value={contactId}
          onValueChange={(id) => {
            setContactId(id);
            void loadConsents(tenant, id);
          }}
          choices={[
            { value: "", label: "选择客户" },
            ...(contacts ?? []).map((item) => ({ value: item.id, label: item.name })),
          ]}
        />
        {tenant && contacts && contacts.length === 0 ? <p className="muted">还没有可选的客户</p> : null}
        <RendererSelect
          label="授权"
          value={consentId}
          onValueChange={setConsentId}
          disabled={!marketingOpen}
          choices={[
            { value: "", label: "不选择授权" },
            ...(consents ?? []).map((item) => ({ value: item.id, label: consentLabel(item) })),
          ]}
        />
        {selected && consents && consents.length === 0 ? <p className="muted">还没有可选择的授权</p> : null}
        <RendererSelect label="渠道" value={channel} onValueChange={setChannel} choices={CHANNEL_CHOICES} />
        <RendererSelect
          label="用途"
          value={purpose}
          onValueChange={(next) => {
            if (next === "marketing" && !marketingOpen) return;
            setPurpose(next);
          }}
          choices={[
            { value: "follow_up", label: "跟进" },
            { value: "marketing", label: "营销", disabled: !marketingOpen, disabledReason: "还没有可选择的授权" },
          ]}
        />
        <Input label="收件人" value={recipient} onChange={(e) => setRecipient(e.target.value)} />
        <Textarea label="内容" value={body} onChange={(e) => setBody(e.target.value)} rows={3} />
        {/* HUI-2626 fix2（gate-r2 #6 主行动收敛）：主行动只在可执行时出现；
            不可执行时给下一步指引，不再渲染一排等权灰按钮。次级动作保持 secondary。 */}
        {payload ? (
          <>
            <Button
              variant="primary" size="sm"
              type="button"
              data-page-primary="true"
              disabled={busy}
              onClick={() => {
                void post("/api/sop/reminders", payload);
              }}
            >
              {pagePrimary("sop")}
            </Button>{" "}
            <Button
              variant="secondary" size="sm"
              type="button"
              disabled={busy}
              onClick={() => {
                void post("/api/sop/drafts", payload);
              }}
            >
              保存回复草稿
            </Button>
          </>
        ) : (
          <p className="muted">选择客户并填写内容后，这里可以记下内部提醒。</p>
        )}
        {error ? <SurfaceState kind="error" title="跟进没有记下" detail={error} /> : null}
      </div>
      <div className="card">
        <h2>人工确认</h2>
        <p className="muted">确认后停在待发送或未送达。没有渠道回执时不会写成已送达。</p>
        <p className="muted">{draftId ? "已选用一条草稿，可以人工确认。" : "先保存草稿，或在下面的记录里选用。"}</p>
        {draftId ? (
          <Button
            variant="secondary" size="sm"
            type="button"
            disabled={busy}
            onClick={() => void post(`/api/sop/drafts/${encodeURIComponent(draftId.trim())}/confirm`, {})}
          >
            人工确认
          </Button>
        ) : null}
      </div>
      <div className="card">
        <h2>记录</h2>
        {items === null ? <SurfaceState kind="loading" title="正在读取提醒" detail="草稿和人工确认马上就位。" /> : null}
        {items?.length === 0 ? <SurfaceState kind="empty" title="还没有提醒或草稿" detail="记下内部提醒后会出现在这里。" /> : null}
        <ul>
          {items?.map((item) => (
            <li key={item.id}>
              {recordLabel(item.kind, item.delivered ? "delivered" : item.status)} · {item.channel} · {item.purpose}
              {item.body ? ` · ${item.body}` : ""}
              {item.kind === "pending_draft" ? (
                <>
                  {" "}
                  <Button variant="secondary" size="sm" type="button" disabled={busy} onClick={() => setDraftId(item.id)}>
                    选用
                  </Button>
                </>
              ) : null}
            </li>
          ))}
        </ul>
      </div>
    </main>
  );
}
