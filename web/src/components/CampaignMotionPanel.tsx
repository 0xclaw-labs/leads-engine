"use client";

import { useCallback, useEffect, useState } from "react";
import { scopeInit, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { failureText } from "@/lib/productShell";
import { presentCampaignMotion } from "@/lib/campaignMotion";
import { presentCampaignProduction, validateHandoffFacts } from "@/lib/campaignMotionQueue";

interface MotionCapability {
  enabled?: boolean;
  allowed_templates?: string[];
}

interface MotionExport {
  campaign_goal?: string;
  target_audience?: string;
  approved_facts?: Array<{ key?: string; value?: string }>;
  authorized_asset_ids?: string[];
  cta?: string;
  channels?: string[];
  budget_attribution_id?: string;
  motion?: { project_id?: string; revision_id?: string; digest?: string };
}

interface MotionResponse {
  export?: MotionExport | null;
  error?: string;
  message?: string;
  job_queue_view?: unknown;
  human_followup_pending?: boolean;
  model_calls?: number;
  channel_count?: number;
  model_cost_minor?: number | null;
  payer_kind?: string;
}

export function CampaignMotionPanel({ campaignId }: { campaignId: string }) {
  const scope = useCrmScope();
  const [hidden, setHidden] = useState(true);
  const [goal, setGoal] = useState("");
  const [audience, setAudience] = useState("");
  const [fact, setFact] = useState("");
  const [assetID, setAssetID] = useState("");
  const [cta, setCta] = useState("");
  const [channels, setChannels] = useState("");
  const [budgetID, setBudgetID] = useState("");
  const [projectID, setProjectID] = useState("");
  const [revisionID, setRevisionID] = useState("");
  const [digest, setDigest] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [recorded, setRecorded] = useState(false);
  const [queueView, setQueueView] = useState<unknown>(undefined);
  const [humanPending, setHumanPending] = useState(false);
  const [productionMeta, setProductionMeta] = useState({ model_calls: 0, channel_count: 0, model_cost_minor: null as number | null, payer_kind: "" });
  const [allowedTemplates, setAllowedTemplates] = useState<string[]>([]);
  const [preference, setPreference] = useState("");
  const view = presentCampaignMotion({ channels: channels.split(/[\s,，、]+/).filter(Boolean) });
  const production = presentCampaignProduction({
    campaign_id: campaignId,
    job_queue_view: queueView,
    human_followup_pending: humanPending,
    model_calls: productionMeta.model_calls,
    channel_count: productionMeta.channel_count || view.channels.length,
    model_cost_minor: productionMeta.model_cost_minor,
    payer_kind: productionMeta.payer_kind,
  });

  const applyExport = useCallback((doc: MotionExport | null | undefined) => {
    if (!doc) return;
    setRecorded(true);
    setGoal(doc.campaign_goal ?? "");
    setAudience(doc.target_audience ?? "");
    setFact(doc.approved_facts?.[0]?.value ?? "");
    setAssetID(doc.authorized_asset_ids?.[0] ?? "");
    setCta(doc.cta ?? "");
    setChannels((doc.channels ?? []).join("、"));
    setBudgetID(doc.budget_attribution_id ?? "");
    setProjectID(doc.motion?.project_id ?? "");
    setRevisionID(doc.motion?.revision_id ?? "");
    setDigest(doc.motion?.digest ?? "");
  }, []);

  const load = useCallback(async () => {
    if (!campaignId || !scope.tenant) return;
    const init = scopeInit(scope.tenant);
    const capRes = await fetch("/api/campaign-motion/capability", init);
    // HUI-2626 盲评 P2①：capability 关闭时是 200+enabled:false，据此隐藏；
    // 404 兜底保留（旧服务端契约）。不再让探测打进 console。
    if (capRes.status === 404) {
      setHidden(true);
      return;
    }
    const cap = (await capRes.json().catch(() => null)) as MotionCapability | null;
    if (cap && cap.enabled === false) {
      setHidden(true);
      return;
    }
    setHidden(false);
    const C0_TEMPLATES = ["immediate", "night_batch", "free_idle", "paid_elastic"];
    setAllowedTemplates(
      Array.isArray(cap?.allowed_templates)
        ? cap.allowed_templates.filter((item): item is string => typeof item === "string" && C0_TEMPLATES.includes(item))
        : [],
    );
    const res = await fetch(`/api/campaigns/${campaignId}/motion-handoff`, init);
    if (res.status === 404) {
      setMessage("当前线索不是活动。这一步只交出活动事实。");
      return;
    }
    if (!res.ok) {
      const errBody = (await res.json().catch(() => null)) as MotionResponse | null;
      setMessage(failureText(errBody, res.status));
      return;
    }
    const body = (await res.json()) as MotionResponse;
    setQueueView(body.job_queue_view);
    setHumanPending(body.human_followup_pending === true);
    setProductionMeta({
      model_calls: typeof body.model_calls === "number" ? body.model_calls : 0,
      channel_count: typeof body.channel_count === "number" ? body.channel_count : 0,
      model_cost_minor: typeof body.model_cost_minor === "number" ? body.model_cost_minor : null,
      payer_kind: typeof body.payer_kind === "string" ? body.payer_kind : "",
    });
    applyExport(body.export);
  }, [applyExport, campaignId, scope.tenant]);

  useEffect(() => {
    void load();
  }, [load]);

  if (hidden) return null;

  async function save() {
    if (!scope.tenant) return;
    setBusy(true);
    setMessage("");
    const names = channels.split(/[\s,，、]+/).map((item) => item.trim()).filter(Boolean);
    const payload: Record<string, unknown> = {
      campaign_goal: goal.trim(),
      target_audience: audience.trim(),
      cta: cta.trim(),
      channels: names,
      budget_attribution_id: budgetID.trim(),
      motion: {
        project_id: projectID.trim(),
        revision_id: revisionID.trim(),
        campaign_id: campaignId,
        digest: digest.trim(),
      },
    };
    if (fact.trim()) payload.approved_facts = [{ key: "fact", value: fact.trim(), confirmed: true }];
    if (assetID.trim()) payload.assets = [{ id: assetID.trim(), authorized: true }];
    if (preference) payload.execution_preference = preference;
    const facts = validateHandoffFacts(payload);
    if (!facts.ok) {
      setBusy(false);
      setMessage(facts.message);
      return;
    }
    const headers = new Headers(scopeInit(scope.tenant).headers);
    headers.set("content-type", "application/json");
    const res = await fetch(`/api/campaigns/${campaignId}/motion-handoff`, {
      method: "POST",
      headers,
      body: JSON.stringify(payload),
    });
    const body = (await res.json().catch(() => null)) as MotionResponse | null;
    setBusy(false);
    if (!res.ok) {
      setMessage(failureText(body, res.status));
      return;
    }
    applyExport(body?.export);
    setMessage(presentCampaignMotion(null).status);
  }

  return (
    <section className="card stack-form">
      <h2>活动动效交出</h2>
      <p data-testid="campaign-motion-notice">{view.notice}</p>
      {recorded ? <p data-testid="campaign-motion-status">{view.status}</p> : null}
      <p>{view.conversionNote}</p>
      <label>
        活动目标
        <input value={goal} onChange={(e) => setGoal(e.target.value)} />
      </label>
      <label>
        目标受众
        <input value={audience} onChange={(e) => setAudience(e.target.value)} />
      </label>
      <label>
        已确认的品牌或产品事实
        <input value={fact} onChange={(e) => setFact(e.target.value)} />
      </label>
      <label>
        已授权素材 id
        <input value={assetID} onChange={(e) => setAssetID(e.target.value)} />
      </label>
      <label>
        CTA
        <input value={cta} onChange={(e) => setCta(e.target.value)} />
      </label>
      <label>
        渠道名
        <input value={channels} onChange={(e) => setChannels(e.target.value)} placeholder="抖音、视频号" />
      </label>
      <label>
        预算归属 id
        <input value={budgetID} onChange={(e) => setBudgetID(e.target.value)} />
      </label>
      <label>
        Motion project_id
        <input value={projectID} onChange={(e) => setProjectID(e.target.value)} />
      </label>
      <label>
        Motion revision_id
        <input value={revisionID} onChange={(e) => setRevisionID(e.target.value)} />
      </label>
      <label>
        Motion digest
        <input value={digest} onChange={(e) => setDigest(e.target.value)} />
      </label>
      <p>
        渠道：{view.channels.join("、") || "还没有渠道名"}
      </p>
      {allowedTemplates.length > 0 ? (
        <label>
          执行偏好（只从服务器允许的模板选）
          <select value={preference} onChange={(e) => setPreference(e.target.value)} data-field="preference">
            <option value="">按服务器默认</option>
            {allowedTemplates.map((template) => (
              <option key={template} value={template}>{template}</option>
            ))}
          </select>
        </label>
      ) : (
        <p data-field="preference-unknown">服务器未提供允许的执行模板；本页不传数字优先级。</p>
      )}
      <button type="button" disabled={busy || !goal.trim() || !budgetID.trim() || view.channels.length === 0} onClick={() => void save()}>
        记下交出
      </button>
      {message ? <p>{message}</p> : null}
      <section data-campaign-production="true">
        <p data-field="waiting">
          {production.waitLabel}：{production.etaText}
          {production.nextWindowText ? ` · 下一窗口 ${production.nextWindowText}` : ""}
          {production.policyVersion === null ? "" : ` · 策略版本 v${production.policyVersion}`}
        </p>
        {production.deadlineWarning ? <p data-field="deadline-warning">{production.deadlineWarning}</p> : null}
        <p data-field="costs">{production.costs.note}</p>
        <p data-field="payer">
          {production.costs.payerText} · 模型成本 {production.costs.modelCostText} · {production.feeDisclosure}
        </p>
        <p data-field="crm">{production.crmNotice}</p>
        <p data-field="auto" data-auto-outreach={String(production.autoOutreach)} data-auto-conversion={String(production.autoConversion)}>
          {production.notice}
        </p>
      </section>
    </section>
  );
}
