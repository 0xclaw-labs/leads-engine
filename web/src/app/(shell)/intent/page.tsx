"use client";

import Link from "next/link";
import { useCallback, useState } from "react";
import { RecordList, SurfaceState, useShellWidth } from "@/components/workbench/chrome";
import { Checkbox, Input } from "@/vendor/painuo/react/v1/src/index";
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
import { useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { MISSING_SCOPE, domainFailureText, listTenantHeader, productError } from "@/lib/productShell";
import { modelStatusText, originLabel, outreachControls, presentAssessment, type GradeInput, type GradeView } from "@/lib/intentGrade";

// HUI-1684 意向分级页。只打本站 BFF。规则版本由服务端返回。
// 页面只展示下一步建议，不外呼、不发短信、不拉群、不建单。

interface SnapshotBody {
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
  error?: string;
  message?: string;
}

interface ScoreResponse extends SnapshotBody {
  snapshot?: SnapshotBody;
}

interface ReportOutcome {
  id: string;
  kind: string;
  label: string;
  predicted: string;
  match: boolean;
  reason: string;
  suggestion: string;
  origin?: string;
}

const SCREEN_EXAMPLE: GradeInput = {
  grade: "high",
  reason: "出现采购、合同或打款等购买承诺。",
  citations: [{ evidence_id: "sample-strong", excerpt: "我们这周要采购 50 套，请发合同和报价。" }],
  missing_fields: ["buyer"],
  fresh_until: "2026-09-29T08:00:00Z",
  rule_version: "rules-hui-1684-v1",
  model_version: "none",
  calibrated: false,
  disclaimer: "规则评分，不是真人成交预测，也不是校准后的成交概率。",
  suggestion: {
    kind: "suggest_follow_up",
    label: "建议销售人工确认下一步。是否可联系由授权和渠道规则决定。",
  },
  human_locked: false,
  stale: false,
};

function ExampleCard() {
  const view = presentAssessment(SCREEN_EXAMPLE);
  return (
    <div className="bg-card text-card-foreground border border-border rounded-[10px] p-5 mb-4">
      <h2>界面样例 · {view.label}</h2>
      <p className="muted">这是标注样本的展示方式，不是某位客户的成交预测。</p>
      <p>{view.reason}</p>
      <p>{view.suggestion}</p>
      <p className="muted">
        版本 {view.version} · 有效至 {view.freshness}
      </p>
      <p>证据：{view.citations.join("；")}</p>
      <p>缺失：{view.missing.join("、")}</p>
      <p className="muted">{view.disclaimer}</p>
      <p className="muted">成交概率：不提供</p>
      {view.actions.map((action) => (
        <span key={action} className="tab">
          {action}
        </span>
      ))}
    </div>
  );
}

const KIND_LABEL: Record<string, string> = {
  strong_intent: "强意向",
  ordinary_qa: "普通问答",
  after_sales: "售后",
  missing_data: "缺数据",
  refuse_marketing: "拒绝联系",
};

function asInput(snap: SnapshotBody): GradeInput {
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
    suggestion: {
      kind: snap.suggestion?.kind ?? "",
      label: snap.suggestion?.label ?? "",
    },
    human_locked: snap.human_locked === true,
    stale: snap.stale === true,
    stale_reason: snap.stale_reason,
  };
}

export default function IntentGradePage() {
  const scope = useCrmScope();
  const width = useShellWidth();
  const tenant = scope.tenant ?? "";
  const [subjectKind, setSubjectKind] = useState("lead");
  const [subjectId, setSubjectId] = useState("");
  const [text, setText] = useState("");
  const [snapshotId, setSnapshotId] = useState("");
  const [view, setView] = useState<GradeView | null>(null);
  const [error, setError] = useState("");
  const [grade, setGrade] = useState("low");
  const [disposition, setDisposition] = useState("rejected");
  const [correctionReason, setCorrectionReason] = useState("");
  const [misjudgment, setMisjudgment] = useState(true);
  const [report, setReport] = useState<ReportOutcome[] | null>(null);
  const [reportNote, setReportNote] = useState("");
  const [reportError, setReportError] = useState(false);

  const headers = useCallback((): HeadersInit => ({ "content-type": "application/json", ...listTenantHeader(tenant) }), [tenant]);

  const loadReport = useCallback(async () => {
    setReportNote("");
    setReportError(false);
    if (!tenant) {
      setReport(null);
      setReportNote("先在顶部选择工作范围，再看标注样本。这些样本不是真人成交记录。");
      return;
    }
    try {
      const res = await fetch("/api/intent-grades/sample-report", { headers: listTenantHeader(tenant) ?? {} });
      // ⑥ flag 域：解析失败（mux text/plain 404）按「功能未开通」指引，不再把
      // 功能关闭说成记录不存在。
      const body = (await res.json().catch(() => null)) as { outcomes?: ReportOutcome[]; disclaimer?: string } | null;
      if (!res.ok) {
        setReport(null);
        setReportError(true);
        setReportNote(domainFailureText(res.status, body));
        return;
      }
      setReport((body?.outcomes ?? []) as ReportOutcome[]);
      setReportNote(typeof body?.disclaimer === "string" ? body.disclaimer : "");
    } catch (e) {
      setReport(null);
      setReportError(true);
      setReportNote(productError(e instanceof Error ? e.message : "样本报告读取失败"));
    }
  }, [tenant]);

  const score = async () => {
    setError("");
    setView(null);
    if (!tenant || !subjectId.trim()) {
      setError(tenant ? "先填写线索或会话编号" : MISSING_SCOPE);
      return;
    }
    try {
      const res = await fetch("/api/intent-grades", {
        method: "POST",
        headers: headers(),
        body: JSON.stringify({
          subject_kind: subjectKind,
          subject_id: subjectId.trim(),
          evidence: text.trim() ? [{ id: "ev-page", tenant_id: tenant, text: text.trim() }] : [],
        }),
      });
      const body = (await res.json().catch(() => null)) as ScoreResponse | null;
      if (!res.ok) {
        setError(domainFailureText(res.status, body));
        return;
      }
      const snap = body?.snapshot ?? body ?? ({} as SnapshotBody);
      setSnapshotId(snap?.id ?? "");
      setView(presentAssessment(asInput(snap)));
    } catch (e) {
      setError(productError((e as Error).message));
    }
  };

  const correct = async () => {
    setError("");
    if (!snapshotId || !correctionReason.trim()) {
      setError("修正要写下采纳或驳回的原因");
      return;
    }
    try {
      const res = await fetch(`/api/intent-grades/${snapshotId}/corrections`, {
        method: "POST",
        headers: headers(),
        body: JSON.stringify({
          grade,
          disposition,
          misjudgment,
          reason: correctionReason.trim(),
          facts: [{ field: "intent", value: grade }],
        }),
      });
      const body = (await res.json().catch(() => null)) as ScoreResponse | null;
      if (!res.ok) {
        setError(domainFailureText(res.status, body));
        return;
      }
      const snap = body?.snapshot ?? body ?? ({} as SnapshotBody);
      setSnapshotId(snap?.id ?? snapshotId);
      setView(presentAssessment(asInput(snap)));
    } catch (e) {
      setError(productError((e as Error).message));
    }
  };

  const shown = view;
  const outreach = shown ? outreachControls(shown) : [];

  return (
    <main data-page="intent">
      <header className="page-head">
        <h1>意向分级</h1>
        <Button variant="outline" size="sm" asChild>
          <Link href="/">返回工作台</Link>
        </Button>
      </header>
      {!tenant ? <SurfaceState kind="recovery" title="还没有工作范围" detail={MISSING_SCOPE} /> : null}
      <div className="bg-card text-card-foreground border border-border rounded-[10px] p-5 mb-4">
        <p data-tone="ai">规则评分，不是真人成交预测，也不是校准后的成交概率。下一步只给建议，不自动外呼、短信、拉群或创建订单。</p>
        <p data-tone="ai">{modelStatusText()}</p>
        <div className="queue-actions">
          <div className="public-form-field">
            <Label htmlFor="intent-subject-kind">对象</Label>
            <Select value={subjectKind} onValueChange={setSubjectKind}>
              <SelectTrigger id="intent-subject-kind" className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="lead">线索</SelectItem>
                <SelectItem value="session">会话</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <Input label="编号" value={subjectId} onChange={(e) => setSubjectId(e.target.value)} placeholder="线索或会话 id" />
        </div>
        <div className="public-form-field">
          <Label htmlFor="intent-evidence">本租户可见的原文</Label>
          <Textarea id="intent-evidence" value={text} onChange={(e) => setText(e.target.value)} rows={4} />
        </div>
        <Button type="button" size="sm" data-page-primary="true" onClick={score}>
          用规则重新评估
        </Button>
        <p className="muted">重算沿用同一条用量记录，不重复扣费。人工确认过的事实不会被这次评估盖掉。</p>
      </div>
      <ExampleCard />
      {error ? <SurfaceState kind="error" title="评估没有完成" detail={productError(error)} /> : null}
      {shown ? (
        <div className="bg-card text-card-foreground border border-border rounded-[10px] p-5 mb-4">
          <h2>
            {shown.label}
            {shown.humanLocked ? " · 人工已确认" : ""}
          </h2>
          <p>{shown.reason}</p>
          <p>{shown.suggestion}</p>
          <p className="muted">
            版本 {shown.version} · 有效至 {shown.freshness || "未给出"}
          </p>
          {shown.staleText ? <p>{shown.staleText}</p> : null}
          <p>证据：{shown.citations.length ? shown.citations.join("；") : "没有可引用的证据"}</p>
          <p>缺失：{shown.missing.length ? shown.missing.join("、") : "没有列出缺失字段"}</p>
          <p className="muted">{shown.disclaimer}</p>
          <p className="muted">成交概率：不提供</p>
          {shown.actions.map((action) => (
            <Button
              variant="secondary"
              key={action}
              type="button"
              size="sm"
              onClick={() => document.getElementById("intent-correction")?.scrollIntoView()}
            >
              {action}
            </Button>
          ))}
          {outreach.length > 0 ? <p>触达：{outreach.join("、")}</p> : null}
          <form
            id="intent-correction"
            onSubmit={(e) => {
              e.preventDefault();
              void correct();
            }}
          >
            <h3>人工修正</h3>
            <div className="queue-actions">
              <div className="public-form-field">
                <Label htmlFor="intent-grade">分级</Label>
                <Select value={grade} onValueChange={setGrade}>
                  <SelectTrigger id="intent-grade" className="w-40">
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
                <Label htmlFor="intent-disposition">结论</Label>
                <Select value={disposition} onValueChange={setDisposition}>
                  <SelectTrigger id="intent-disposition" className="w-40">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="rejected">驳回</SelectItem>
                    <SelectItem value="adopted">采纳</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <Checkbox label="标记误判" checked={misjudgment} onCheckedChange={(checked) => setMisjudgment(checked)} />
            </div>
            <div className="public-form-field">
              <Label htmlFor="intent-reason">原因</Label>
              <Textarea id="intent-reason" value={correctionReason} onChange={(e) => setCorrectionReason(e.target.value)} rows={3} />
            </div>
            <Button variant="secondary" type="submit" size="sm">保存修正</Button>
          </form>
        </div>
      ) : null}
      <div className="bg-card text-card-foreground border border-border rounded-[10px] p-5 mb-4">
        <h2>标注样本</h2>
        <Button variant="secondary" type="button" size="sm" onClick={() => void loadReport()}>
          查看样本报告
        </Button>
        {reportNote ? (
          <p role={reportError ? "alert" : "status"} className={reportError ? "error" : "muted"}>{reportNote}</p>
        ) : null}
        {report ? (
          <RecordList
            width={width}
            rows={report.map((row) => ({
              id: row.id,
              title: `${KIND_LABEL[row.kind] ?? row.kind} · ${row.label}`,
              facts: [
                { label: "规则结果", value: row.predicted },
                { label: "理由", value: row.reason },
                { label: "建议", value: row.suggestion },
                { label: "来源", value: originLabel(row.origin || "fixture") },
                { label: "一致性", value: row.match ? "一致" : "误判" },
              ],
            }))}
          />
        ) : null}
      </div>
    </main>
  );
}
