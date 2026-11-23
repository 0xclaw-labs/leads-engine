"use client";

// HUI-2391 矩阵草稿源端面板。送矩阵 = 在真实 AG10 建一条文案草稿并落一条
// 本地引用；回查 = 经 BFF 读真实服务的 receipt。草稿不是发布：面板对没拿到
// 的事实显示"—"，绝不显示完成态。返回路径固定回本活动页。

import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { scopeInit, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { failureText, productError } from "@/lib/productShell";
import { MATRIX_DRAFT_NOTICE, presentMatrixDraftRow, type MatrixDraftView } from "@/lib/matrixDraft";

interface DraftResponse {
  draft_id?: string;
  ref_id?: string;
  [key: string]: unknown;
}

interface ListResponse {
  drafts?: DraftResponse[];
  notice?: string;
}

export function MatrixDraftPanel({ campaignId }: { campaignId: string }) {
  const scope = useCrmScope();
  const [hidden, setHidden] = useState(true);
  const [assetID, setAssetID] = useState("");
  const [assetHash, setAssetHash] = useState("");
  const [revision, setRevision] = useState("3");
  const [grantRef, setGrantRef] = useState("");
  const [licenseRef, setLicenseRef] = useState("");
  const [brandID, setBrandID] = useState("");
  const [payerID, setPayerID] = useState("");
  const [assetSourceRef, setAssetSourceRef] = useState("");
  const [view, setView] = useState<MatrixDraftView | null>(null);
  const [refID, setRefID] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  const applyRow = useCallback((row: DraftResponse) => {
    setView(presentMatrixDraftRow(row));
    setRefID(typeof row.ref_id === "string" ? row.ref_id : "");
  }, []);

  const load = useCallback(async () => {
    if (!campaignId || !scope.tenant) return;
    try {
      const res = await fetch(`/api/campaigns/${campaignId}/matrix-drafts`, scopeInit(scope.tenant));
      if (res.status === 404) {
        setHidden(true);
        return;
      }
      if (!res.ok) {
        setHidden(true);
        return;
      }
      setHidden(false);
      const body = (await res.json().catch(() => null)) as ListResponse | null;
      const row = body?.drafts?.[0];
      if (row) applyRow(row);
    } catch {
      setHidden(true);
    }
  }, [applyRow, campaignId, scope.tenant]);

  useEffect(() => {
    void load();
  }, [load]);

  if (hidden) return null;

  async function submit() {
    if (!scope.tenant || busy) return;
    setBusy(true);
    setMessage("");
    const headers = new Headers(scopeInit(scope.tenant).headers);
    headers.set("content-type", "application/json");
    const payload: Record<string, unknown> = {
      asset_id: assetID.trim(),
      asset_hash: assetHash.trim(),
      revision: Number.parseInt(revision, 10) || 0,
      content_type: "copy",
      grant_ref: grantRef.trim(),
      license_ref: licenseRef.trim(),
      brand_id: brandID.trim(),
      payer_id: payerID.trim(),
    };
    if (assetSourceRef.trim()) payload.asset_source_ref = assetSourceRef.trim();
    try {
      // No local shortcut: the matrix service decides every create, including a replay.
      const res = await fetch(`/api/campaigns/${campaignId}/matrix-drafts`, {
        method: "POST",
        headers,
        body: JSON.stringify(payload),
      });
      const body = (await res.json().catch(() => null)) as (DraftResponse & { error?: unknown }) | null;
      if (!res.ok) {
        setMessage(failureText(body, res.status));
        return;
      }
      applyRow(body ?? {});
      setMessage(body?.idempotent_replay === true ? "同样的内容已经记录过，仍是同一条草稿引用。" : MATRIX_DRAFT_NOTICE);
    } catch (e) {
      setMessage(productError(e instanceof Error ? e.message : "送矩阵失败"));
    } finally {
      setBusy(false);
    }
  }

  async function recheck() {
    if (!scope.tenant || !refID || busy) return;
    setBusy(true);
    setMessage("");
    try {
      const res = await fetch(`/api/campaigns/${campaignId}/matrix-drafts/${refID}/receipt`, scopeInit(scope.tenant));
      const body = (await res.json().catch(() => null)) as (DraftResponse & { error?: unknown }) | null;
      if (!res.ok) {
        setMessage(failureText(body, res.status));
        return;
      }
      applyRow(body ?? {});
      setMessage("回查完成：这是矩阵服务当前保存的草稿事实；未知字段仍是未知。");
    } catch (e) {
      setMessage(productError(e instanceof Error ? e.message : "回查失败"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="bg-card text-card-foreground border border-border rounded-[10px] p-5 mb-4 stack-form">
      <h2>获客内容送矩阵</h2>
      <p data-testid="matrix-draft-notice">{MATRIX_DRAFT_NOTICE}</p>
      <div className="public-form-field">
        <Label htmlFor="matrix-asset-id">内容资产 id</Label>
        <Input id="matrix-asset-id" value={assetID} onChange={(e) => setAssetID(e.target.value)} placeholder="asset_copy_1" />
      </div>
      <div className="public-form-field">
        <Label htmlFor="matrix-asset-hash">内容哈希（64 位十六进制）</Label>
        <Input id="matrix-asset-hash" value={assetHash} onChange={(e) => setAssetHash(e.target.value)} placeholder="ab12…（64 位）" />
      </div>
      <div className="public-form-field">
        <Label htmlFor="matrix-revision">内容版本</Label>
        <Input id="matrix-revision" value={revision} onChange={(e) => setRevision(e.target.value)} />
      </div>
      <div className="public-form-field">
        <Label htmlFor="matrix-grant-ref">授权引用 grant_ref</Label>
        <Input id="matrix-grant-ref" value={grantRef} onChange={(e) => setGrantRef(e.target.value)} />
      </div>
      <div className="public-form-field">
        <Label htmlFor="matrix-license-ref">素材许可 license_ref</Label>
        <Input id="matrix-license-ref" value={licenseRef} onChange={(e) => setLicenseRef(e.target.value)} />
      </div>
      <div className="public-form-field">
        <Label htmlFor="matrix-brand-id">品牌 id</Label>
        <Input id="matrix-brand-id" value={brandID} onChange={(e) => setBrandID(e.target.value)} />
      </div>
      <div className="public-form-field">
        <Label htmlFor="matrix-payer-id">付款方 id</Label>
        <Input id="matrix-payer-id" value={payerID} onChange={(e) => setPayerID(e.target.value)} />
      </div>
      <div className="public-form-field">
        <Label htmlFor="matrix-source-ref">资产来源声明（可选）</Label>
        <Input id="matrix-source-ref" value={assetSourceRef} onChange={(e) => setAssetSourceRef(e.target.value)} placeholder="touch/landing/camp-matrix" />
      </div>
      <div className="queue-actions">
        <Button
          type="button"
          size="sm"
          data-testid="matrix-draft-submit"
          disabled={
            busy ||
            !assetID.trim() ||
            !/^[0-9a-f]{64}$/.test(assetHash.trim()) ||
            !(Number.parseInt(revision, 10) >= 1) ||
            !grantRef.trim() ||
            !licenseRef.trim() ||
            !brandID.trim() ||
            !payerID.trim()
          }
          onClick={() => void submit()}
        >
          送矩阵
        </Button>
        <Button type="button" variant="secondary" size="sm" data-testid="matrix-draft-recheck" disabled={busy || !refID} onClick={() => void recheck()}>
          回查草稿事实
        </Button>
      </div>
      {view ? (
        <Table data-testid="matrix-draft-facts">
          <TableBody>
            <TableRow>
              <TableHead>草稿 id</TableHead>
              <TableCell>{view.draftId || "—"}</TableCell>
            </TableRow>
            <TableRow>
              <TableHead>状态</TableHead>
              <TableCell>{view.status || "—"}</TableCell>
            </TableRow>
            <TableRow>
              <TableHead>矩阵 plan / item / 外发 id</TableHead>
              <TableCell>
                {view.matrixPlanID} / {view.matrixItemID} / {view.externalPostID}
              </TableCell>
            </TableRow>
            <TableRow>
              <TableHead>触达 / 点赞 / 线索</TableHead>
              <TableCell>
                {view.matrixReach} / {view.matrixLikes} / {view.matrixLeads}
              </TableCell>
            </TableRow>
            <TableRow>
              <TableHead>发布标记</TableHead>
              <TableCell>{view.published ? "true" : "false"}</TableCell>
            </TableRow>
            <TableRow>
              <TableHead>扣费标记</TableHead>
              <TableCell>{view.charged ? "true" : "false"}</TableCell>
            </TableRow>
            <TableRow>
              <TableHead>资产来源声明</TableHead>
              <TableCell>{view.assetSourceRef}</TableCell>
            </TableRow>
            <TableRow>
              <TableHead>返回锚点（服务端 return_path）</TableHead>
              <TableCell>{view.returnPath}</TableCell>
            </TableRow>
            <TableRow>
              <TableHead>回原位</TableHead>
              <TableCell>
                <a href={`/leads/${campaignId}`} data-testid="matrix-draft-return">回本活动页</a>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      ) : null}
      {message ? <p role="alert" data-testid="matrix-draft-message">{message}</p> : null}
    </section>
  );
}
