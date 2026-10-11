"use client";

// HUI-2391 矩阵草稿源端面板。送矩阵 = 在真实 AG10 建一条文案草稿并落一条
// 本地引用；回查 = 经 BFF 读真实服务的 receipt。草稿不是发布：面板对没拿到
// 的事实显示"—"，绝不显示完成态。返回路径固定回本活动页。

import { useCallback, useEffect, useState } from "react";
import { scopeInit, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { failureText } from "@/lib/productShell";
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
    // No local shortcut: the matrix service decides every create, including a replay.
    const res = await fetch(`/api/campaigns/${campaignId}/matrix-drafts`, {
      method: "POST",
      headers,
      body: JSON.stringify(payload),
    });
    const body = (await res.json().catch(() => null)) as (DraftResponse & { error?: unknown }) | null;
    setBusy(false);
    if (!res.ok) {
      setMessage(failureText(body, res.status));
      return;
    }
    applyRow(body ?? {});
    setMessage(body?.idempotent_replay === true ? "同样的内容已经记录过，仍是同一条草稿引用。" : MATRIX_DRAFT_NOTICE);
  }

  async function recheck() {
    if (!scope.tenant || !refID || busy) return;
    setBusy(true);
    setMessage("");
    const res = await fetch(`/api/campaigns/${campaignId}/matrix-drafts/${refID}/receipt`, scopeInit(scope.tenant));
    const body = (await res.json().catch(() => null)) as (DraftResponse & { error?: unknown }) | null;
    setBusy(false);
    if (!res.ok) {
      setMessage(failureText(body, res.status));
      return;
    }
    applyRow(body ?? {});
    setMessage("回查完成：这是矩阵服务当前保存的草稿事实；未知字段仍是未知。");
  }

  return (
    <section className="card stack-form">
      <h2>获客内容送矩阵</h2>
      <p data-testid="matrix-draft-notice">{MATRIX_DRAFT_NOTICE}</p>
      <label>
        内容资产 id
        <input value={assetID} onChange={(e) => setAssetID(e.target.value)} placeholder="asset_copy_1" />
      </label>
      <label>
        内容哈希（64 位十六进制）
        <input value={assetHash} onChange={(e) => setAssetHash(e.target.value)} placeholder="ab12…（64 位）" />
      </label>
      <label>
        内容版本
        <input value={revision} onChange={(e) => setRevision(e.target.value)} />
      </label>
      <label>
        授权引用 grant_ref
        <input value={grantRef} onChange={(e) => setGrantRef(e.target.value)} />
      </label>
      <label>
        素材许可 license_ref
        <input value={licenseRef} onChange={(e) => setLicenseRef(e.target.value)} />
      </label>
      <label>
        品牌 id
        <input value={brandID} onChange={(e) => setBrandID(e.target.value)} />
      </label>
      <label>
        付款方 id
        <input value={payerID} onChange={(e) => setPayerID(e.target.value)} />
      </label>
      <label>
        资产来源声明（可选）
        <input value={assetSourceRef} onChange={(e) => setAssetSourceRef(e.target.value)} placeholder="touch/landing/camp-matrix" />
      </label>
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        <button
          type="button"
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
        </button>
        <button type="button" data-testid="matrix-draft-recheck" disabled={busy || !refID} onClick={() => void recheck()}>
          回查草稿事实
        </button>
      </div>
      {view ? (
        <table data-testid="matrix-draft-facts">
          <tbody>
            <tr>
              <th>草稿 id</th>
              <td>{view.draftId || "—"}</td>
            </tr>
            <tr>
              <th>状态</th>
              <td>{view.status || "—"}</td>
            </tr>
            <tr>
              <th>矩阵 plan / item / 外发 id</th>
              <td>
                {view.matrixPlanID} / {view.matrixItemID} / {view.externalPostID}
              </td>
            </tr>
            <tr>
              <th>触达 / 点赞 / 线索</th>
              <td>
                {view.matrixReach} / {view.matrixLikes} / {view.matrixLeads}
              </td>
            </tr>
            <tr>
              <th>发布标记</th>
              <td>{view.published ? "true" : "false"}</td>
            </tr>
            <tr>
              <th>扣费标记</th>
              <td>{view.charged ? "true" : "false"}</td>
            </tr>
            <tr>
              <th>资产来源声明</th>
              <td>{view.assetSourceRef}</td>
            </tr>
            <tr>
              <th>返回锚点（服务端 return_path）</th>
              <td>{view.returnPath}</td>
            </tr>
            <tr>
              <th>回原位</th>
              <td>
                <a href={`/leads/${campaignId}`} data-testid="matrix-draft-return">回本活动页</a>
              </td>
            </tr>
          </tbody>
        </table>
      ) : null}
      {message ? <p data-testid="matrix-draft-message">{message}</p> : null}
    </section>
  );
}
