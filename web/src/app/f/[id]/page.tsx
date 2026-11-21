// 公共落地页(HUI-1679 / FEAT-0180 首版固定表单):
//   - 渲染服务端公共描述符的固定字段(name/phone/可选 wechat)+ 单独营销勾选框
//     (缺省不勾:没有同意不进营销池);
//   - 经 BFF /api/public/forms/{id}/submissions 提交(联系方式只进 POST body,
//     绝不进 URL;来源 source/source_ref 只作留痕,不能改变接收租户);
//   - 成功页展示撤销渠道文案;逐项展示服务端 400 details;429/410 显式提示。
"use client";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";

import { useParams, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import {
  buildPayload,
  collectableFields,
  formPublicPath,
  formSubmitPath,
  guardSubmission,
  REVOKE_HINT_FALLBACK,
  type FieldIssue,
  type PublicFormDescriptor,
  type SubmissionSuccess,
} from "@/lib/publicForm";
import { publicFailureText } from "@/lib/productShell";

const FIELD_LABELS: Record<string, string> = {
  name: "称呼",
  phone: "手机号",
  wechat: "微信号(选填)",
};

// 服务端校验 message(英文)不直出：按 field 给游客可读的中文提示。
// 字段白名单与服务端 form_domain.go 对齐(name/phone/wechat)；
// 未映射字段一律「请检查填写内容」。
const FIELD_ISSUE_HINTS: Record<string, string> = {
  name: "请填写姓名",
  phone: "请输入 11 位大陆手机号",
  wechat: "请填写正确的微信号",
};

const SUBMIT_FAILURE_TEXT = "提交没有完成，请稍后重试或联系我们";

function issueText(iss: FieldIssue): string {
  // 本地 guardSubmission 产出的已是中文产品语句,原样使用;
  // 服务端英文校验句(form_domain.go)按字段映射,未映射字段给通用提示。
  if (/[\u4e00-\u9fff]/.test(iss.message ?? "")) return iss.message;
  return FIELD_ISSUE_HINTS[iss.field] ?? "请检查填写内容";
}

function newPageRef(): string {
  // 页面级 source_ref:同一次访问的重试共享(幂等),刷新/新访问产生新引用。
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID().replace(/-/g, "").slice(0, 24);
  }
  return "page-" + Date.now().toString(36);
}

function FormSkeleton() {
  return (
    <main className="public-form-page" role="status" aria-label="表单加载中">
      <Skeleton className="h-7 w-32" />
      <Skeleton className="h-4 w-2/3" />
      <Card>
        <CardContent className="space-y-4 pt-6">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-5 w-1/2" />
          <Skeleton className="h-9 w-24" />
        </CardContent>
      </Card>
    </main>
  );
}

function Landing() {
  const params = useParams<{ id: string }>();
  const search = useSearchParams();
  const formId = typeof params?.id === "string" ? params.id : "";

  const [desc, setDesc] = useState<PublicFormDescriptor | null>(null);
  const [loadError, setLoadError] = useState<{ status: number; message: string } | null>(null);
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [wechat, setWechat] = useState("");
  const [marketing, setMarketing] = useState(false); // 缺省不勾
  const [issues, setIssues] = useState<FieldIssue[]>([]);
  const [serverError, setServerError] = useState<string>("");
  const [submitting, setSubmitting] = useState(false);
  const [done, setDone] = useState<SubmissionSuccess | null>(null);

  const source = useMemo(() => (search.get("source") ?? "landing").slice(0, 64), [search]);
  const pageRef = useMemo(newPageRef, []); // eslint-disable-line react-hooks/exhaustive-deps
  const sourceRef = useMemo(() => {
    const q = search.get("ref");
    if (q && /^[A-Za-z0-9._:@-]{1,128}$/.test(q)) return q;
    return pageRef;
  }, [search, pageRef]);

  useEffect(() => {
    if (!formId) {
      setLoadError({ status: 0, message: "表单链接无效" });
      return;
    }
    fetch(formPublicPath(formId))
      .then(async (res) => {
        const body = await res.json().catch(() => ({}));
        if (!res.ok) {
          setLoadError({ status: res.status, message: body.message ?? body.error ?? `HTTP ${res.status}` });
          return;
        }
        setDesc(body as PublicFormDescriptor);
      })
      .catch((e: Error) => setLoadError({ status: 0, message: e.message }));
  }, [formId]);

  const submit = useCallback(async () => {
    setServerError("");
    const local = guardSubmission(desc, { name, phone, wechat });
    setIssues(local);
    if (local.length > 0) return;
    setSubmitting(true);
    try {
      const res = await fetch(formSubmitPath(formId), {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(buildPayload(desc, { name, phone, wechat }, marketing, { source, sourceRef })),
      });
      const body = await res.json().catch(() => ({}));
      if (res.ok) {
        setDone(body as SubmissionSuccess);
        return;
      }
      if (body.details && Array.isArray(body.details)) {
        setIssues(body.details as FieldIssue[]);
        return;
      }
      setServerError(publicFailureText(body, SUBMIT_FAILURE_TEXT));
    } catch {
      // 网络层异常(如 fetch 失败)不向访客透出浏览器英文 message。
      setServerError(SUBMIT_FAILURE_TEXT);
    } finally {
      setSubmitting(false);
    }
  }, [desc, formId, name, phone, wechat, marketing, source, sourceRef]);

  if (loadError) {
    const gone = loadError.status === 410;
    return (
      <main className="public-form-page">
        <h1>表单不可用</h1>
        <Alert variant="destructive" role="alert">
          <AlertTitle>{gone ? "已停止收集" : "暂不可用"}</AlertTitle>
          <AlertDescription>
            {gone
              ? "该表单已停止收集或已过期,感谢您的关注。"
              : "表单不存在或暂不可用,请核对链接后重试。"}
          </AlertDescription>
        </Alert>
      </main>
    );
  }
  if (!desc) {
    return <FormSkeleton />;
  }
  if (done) {
    return (
      <main className="public-form-page" role="status" aria-live="polite">
        <h1>提交成功</h1>
        <p>
          感谢您的留言,我们会尽快与您联系。
          {done.duplicate ? "(检测到重复提交,已保留您此前的一次提交)" : ""}
        </p>
        <Card className="public-form-card">
          <CardHeader>
            <CardTitle>关于您的授权</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            <p>告知版本:{done.notice_version || desc.notice_version}</p>
            <p>
              营销信息:{done.marketing_allowed ? "已同意接收,可随时撤销" : "未开通(您未勾选营销同意,我们不会将您用于营销触达)"}
            </p>
            <Badge variant="secondary">{done.marketing_allowed ? "已授权营销" : "未授权营销"}</Badge>
            <p className="muted">{done.revoke_hint || REVOKE_HINT_FALLBACK}</p>
          </CardContent>
        </Card>
      </main>
    );
  }

  const fields = collectableFields(desc);
  const fieldValue = (nameKey: string) => (nameKey === "name" ? name : nameKey === "phone" ? phone : wechat);
  const setFieldValue = (nameKey: string, v: string) => {
    if (nameKey === "name") setName(v);
    else if (nameKey === "phone") setPhone(v);
    else setWechat(v);
  };

  return (
    <main className="public-form-page">
      <h1>咨询留言</h1>
      <p className="muted">
        用途:{desc.purpose || "处理您的咨询"} · 告知版本:{desc.notice_version}
      </p>
      <Card className="public-form-card">
        <CardContent className="space-y-4">
          {fields.map((f) => {
            const invalid = issues.some((issue) => issue.field === f.name);
            return (
              <div key={f.name} className="public-form-field">
                <Label htmlFor={`f-${f.name}`}>{FIELD_LABELS[f.name] ?? "其他信息"}</Label>
                <Input
                  id={`f-${f.name}`}
                  type="text"
                  value={fieldValue(f.name)}
                  onChange={(e) => setFieldValue(f.name, e.target.value)}
                  maxLength={f.name === "name" ? 100 : f.name === "phone" ? 32 : 64}
                  aria-invalid={invalid}
                  aria-describedby={invalid ? `error-${f.name}` : undefined}
                  disabled={submitting}
                />
              </div>
            );
          })}
          <Label htmlFor="form-marketing" className="flex items-center gap-2 font-normal">
            <Checkbox
              id="form-marketing"
              checked={marketing}
              disabled={submitting}
              onCheckedChange={setMarketing}
            />
            {desc.marketing_prompt || "我同意接收该商家的营销信息(可随时撤销)"}
          </Label>
          {issues.map((iss) => (
            <p key={`${iss.field}-${iss.message}`} id={`error-${iss.field}`} className="error" role="alert">
              {FIELD_LABELS[iss.field] ?? "其他信息"}：{issueText(iss)}
            </p>
          ))}
          {serverError ? (
            <Alert variant="destructive" role="alert">
              <AlertDescription>{serverError}</AlertDescription>
            </Alert>
          ) : null}
          <Button type="button" onClick={submit} disabled={submitting} className="w-full sm:w-auto">
            {submitting ? "提交中…" : "提交"}
          </Button>
          <p className="muted">{REVOKE_HINT_FALLBACK}</p>
        </CardContent>
      </Card>
    </main>
  );
}

export default function FormLandingPage() {
  return (
    <Suspense fallback={<FormSkeleton />}>
      <Landing />
    </Suspense>
  );
}
