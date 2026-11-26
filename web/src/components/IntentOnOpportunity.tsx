"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ToneBadge } from "@/components/workbench/chrome";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { scopeInit } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, failureText } from "@/lib/productShell";
import {
  modelStatusText,
  originLabel,
  outreachButtons,
  outreachGranted,
  presentAssessment,
  salesCorrectionActions,
  type GradeInput,
} from "@/lib/intentGrade";

interface LeadRow {
  id: string;
  contact_id?: string;
}

interface GradeSnapshot {
  id?: string;
  grade?: string;
  reason?: string;
  citations?: { evidence_id: string; excerpt: string }[];
  missing_fields?: string[];
  fresh_until?: string;
  rule_version?: string;
  model_version?: string;
  calibrated?: boolean;
  disclaimer?: string;
  suggestion?: { kind?: string; label?: string };
  human_locked?: boolean;
  stale?: boolean;
  stale_reason?: string;
  grade_origin?: string;
  permissions?: { call?: boolean; direct_message?: boolean; create_order?: boolean };
}

function asInput(snap: GradeSnapshot): GradeInput {
  return {
    grade: snap.grade ?? "",
    reason: snap.reason ?? "",
    citations: snap.citations ?? [],
    missing_fields: snap.missing_fields ?? [],
    fresh_until: snap.fresh_until ?? "",
    rule_version: snap.rule_version ?? "",
    model_version: snap.model_version ?? "",
    calibrated: snap.calibrated === true,
    disclaimer: snap.disclaimer ?? "",
    suggestion: { kind: snap.suggestion?.kind ?? "", label: snap.suggestion?.label ?? "" },
    human_locked: snap.human_locked === true,
    stale: snap.stale === true,
    stale_reason: snap.stale_reason,
  };
}

export function IntentOnOpportunity({
  contactId,
  tenant,
}: {
  contactId?: string;
  tenant?: string | null;
}) {
  const [leads, setLeads] = useState<LeadRow[]>([]);
  const [note, setNote] = useState("");
  const [rows, setRows] = useState<Record<string, GradeSnapshot | null>>({});
  const [text, setText] = useState<Record<string, string>>({});
  const [grade, setGrade] = useState("low");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const loadGen = useRef({ seq: 0, tenant: "" });

  const load = useCallback(async () => {
    const activeTenant = tenant ?? "";
    const ticket = { seq: loadGen.current.seq + 1, tenant: activeTenant };
    loadGen.current = ticket;
    if (!contactId) {
      setNote("这个商机没有关联联系人，不编造级别。");
      setLeads([]);
      setRows({});
      return;
    }
    if (!activeTenant) {
      setNote(MISSING_SCOPE);
      setLeads([]);
      setRows({});
      return;
    }
    try {
      const res = await fetch("/api/leads", scopeInit(activeTenant));
      const body = await res.json().catch(() => ({}));
      if (loadGen.current !== ticket) return;
      if (!res.ok) {
        setNote(failureText(body, res.status));
        setLeads([]);
        setRows({});
        return;
      }
      const matched = ((body.items ?? []) as LeadRow[]).filter((item) => item.contact_id === contactId);
      setLeads(matched);
      if (matched.length === 0) {
        setNote("这个联系人还没有线索，不编造级别。");
        setRows({});
        return;
      }
      setNote("");
      const next: Record<string, GradeSnapshot | null> = {};
      for (const lead of matched) {
        const current = await fetch(
          `/api/intent-grades/current?subject_kind=lead&subject_id=${encodeURIComponent(lead.id)}`,
          scopeInit(activeTenant),
        );
        if (loadGen.current !== ticket) return;
        next[lead.id] = current.ok ? ((await current.json()) as GradeSnapshot) : null;
      }
      if (loadGen.current !== ticket) return;
      setRows(next);
    } catch {
      if (loadGen.current !== ticket) return;
      setNote("线索列表没有读到，请检查网络后重试。");
      setLeads([]);
    }
  }, [contactId, tenant]);

  useEffect(() => {
    setLeads([]);
    setRows({});
    setNote("");
    void load();
  }, [load]);

  const score = async (leadId: string) => {
    if (!tenant) return;
    const ticket = loadGen.current;
    setBusy(true);
    try {
      const evidenceText = (text[leadId] ?? "").trim();
      const res = await fetch(
        "/api/intent-grades",
        scopeInit(tenant, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({
            subject_kind: "lead",
            subject_id: leadId,
            evidence: evidenceText ? [{ id: "ev-opportunity", tenant_id: tenant, text: evidenceText }] : [],
          }),
        }),
      );
      const body = await res.json().catch(() => ({}));
      if (loadGen.current !== ticket) return;
      if (!res.ok) {
        setNote(failureText(body, res.status));
        return;
      }
      setNote("");
      await load();
    } catch {
      if (loadGen.current === ticket) setNote("评估没有完成，请检查网络后重试。");
    } finally {
      if (loadGen.current === ticket) setBusy(false);
    }
  };

  const correct = async (leadId: string, disposition: "adopted" | "rejected") => {
    if (!tenant) return;
    const snap = rows[leadId];
    if (!snap?.id || !reason.trim()) {
      setNote("修正要写下原因");
      return;
    }
    const ticket = loadGen.current;
    setBusy(true);
    try {
      const res = await fetch(
        `/api/intent-grades/${snap.id}/corrections`,
        scopeInit(tenant, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({
            grade,
            disposition,
            misjudgment: disposition === "rejected",
            reason: reason.trim(),
            facts: [{ field: "intent", value: grade }],
          }),
        }),
      );
      const body = await res.json().catch(() => ({}));
      if (loadGen.current !== ticket) return;
      if (!res.ok) {
        setNote(failureText(body, res.status));
        return;
      }
      setNote("");
      await load();
    } catch {
      if (loadGen.current === ticket) setNote("修正没有保存，请检查网络后重试。");
    } finally {
      if (loadGen.current === ticket) setBusy(false);
    }
  };

  return (
    <section>
      <h2>意向分级</h2>
      <p data-tone="ai">{modelStatusText()}</p>
      <p className="muted">级别只有高、中、低、信息不足。下一步只是建议。</p>
      {note ? <p className="muted">{note}</p> : null}
      {leads.map((lead, index) => {
        const snap = rows[lead.id];
        const view = snap?.grade ? presentAssessment(asInput(snap)) : null;
        const buttons = outreachButtons(snap?.permissions);
        return (
          <div key={lead.id} className="bg-card text-card-foreground border border-border rounded-[10px] p-5 mb-4">
            <p className="flex items-center gap-2">
              <ToneBadge tone={view?.humanLocked ? "human" : "ai"} />
              <strong>关联线索 #{index + 1}</strong>
            </p>
            {view ? (
              <>
                <p>
                  {view.label} · 来源 {originLabel(snap?.grade_origin ?? "rules")}
                  {view.humanLocked ? " · 人工已确认" : ""}
                </p>
                <p>{view.reason}</p>
                <p>{view.suggestion}</p>
                <p className="muted">
                  版本 {view.version} · 有效至 {view.freshness || "未给出"}
                </p>
                {view.staleText ? <p>{view.staleText}</p> : null}
                <p>证据：{view.citations.length ? view.citations.join("；") : "没有可引用的证据"}</p>
                <p>缺失：{view.missing.length ? view.missing.join("、") : "没有列出缺失字段"}</p>
                <p className="muted">{view.disclaimer}</p>
              </>
            ) : (
              <p className="muted">还没有分级。</p>
            )}
            {outreachGranted(snap?.permissions) ? <p>返回了触达许可，此页只保留下一步建议。</p> : null}
            {buttons.length > 0 ? <p>{buttons.join("、")}</p> : null}
            <div className="public-form-field">
              <Label htmlFor={`ev-${lead.id}`}>本租户可见的原文</Label>
              <Textarea
                id={`ev-${lead.id}`}
                value={text[lead.id] ?? ""}
                onChange={(e) => setText((prev) => ({ ...prev, [lead.id]: e.target.value }))}
                rows={3}
              />
            </div>
            <Button variant="secondary" size="sm" type="button" disabled={busy} onClick={() => void score(lead.id)}>
              用规则重新评估
            </Button>
            {snap?.id ? (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                }}
              >
                <div className="public-form-field">
                  <Label htmlFor={`grade-${lead.id}`}>分级</Label>
                  <Select value={grade} onValueChange={setGrade}>
                    <SelectTrigger id={`grade-${lead.id}`} aria-label="分级">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="high">高</SelectItem>
                      <SelectItem value="medium">中</SelectItem>
                      <SelectItem value="low">低</SelectItem>
                      <SelectItem value="insufficient">信息不足</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="public-form-field">
                  <Label htmlFor={`reason-${lead.id}`}>原因</Label>
                  <Textarea
                    id={`reason-${lead.id}`}
                    value={reason}
                    onChange={(e) => setReason(e.target.value)}
                    rows={2}
                  />
                </div>
                {salesCorrectionActions().map((action) => (
                  <Button
                    key={action}
                    variant="secondary"
                    size="sm"
                    type="button"
                    disabled={busy}
                    onClick={() => void correct(lead.id, action === "驳回" ? "rejected" : "adopted")}
                  >
                    {action}
                  </Button>
                ))}
              </form>
            ) : null}
          </div>
        );
      })}
    </section>
  );
}
