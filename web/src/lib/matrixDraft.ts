// HUI-2391 获客侧矩阵草稿展示。草稿 ≠ 发布 ≠ 扣费 ≠ 触达：
// 服务端没给的 plan/item/外发 id 与未知指标恒显示"—"（null 保持），
// 页面文案永不宣称发布、送出、扣费或转化完成。

export const MATRIX_DRAFT_NOTICE =
  "已记下矩阵草稿引用。草稿不等于发布、扣费或触达；审批、排期与发布以矩阵为准。";

const UNKNOWN = "—";

export interface MatrixDraftInput {
  draft_id?: unknown;
  status?: unknown;
  revision?: unknown;
  asset_id?: unknown;
  content_type?: unknown;
  license_expires_at?: unknown;
  asset_source_ref?: unknown;
  return_path?: unknown;
  published?: unknown;
  charged?: unknown;
  matrix_plan_id?: unknown;
  matrix_item_id?: unknown;
  external_post_id?: unknown;
  matrix_reach?: unknown;
  matrix_likes?: unknown;
  matrix_leads?: unknown;
  idempotent_replay?: unknown;
}

export interface MatrixDraftView {
  draftId: string;
  status: string;
  revision: string;
  assetID: string;
  contentType: string;
  licenseExpiresAt: string;
  assetSourceRef: string;
  returnPath: string;
  published: boolean;
  charged: boolean;
  matrixPlanID: string;
  matrixItemID: string;
  externalPostID: string;
  matrixReach: string;
  matrixLikes: string;
  matrixLeads: string;
  idempotentReplay: boolean;
  notice: string;
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function known(value: unknown): string {
  if (value === null || value === undefined || value === "") return UNKNOWN;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  if (typeof value === "string") return value;
  return UNKNOWN;
}

function flag(value: unknown): boolean {
  return value === true;
}

export function presentMatrixDraft(input?: MatrixDraftInput | null): MatrixDraftView {
  return presentMatrixDraftRow(input ?? {});
}

export function presentMatrixDraftRow(input: MatrixDraftInput): MatrixDraftView {
  const revision = typeof input.revision === "number" ? input.revision : 0;
  return {
    draftId: text(input.draft_id),
    status: text(input.status),
    revision: revision > 0 ? String(revision) : UNKNOWN,
    assetID: text(input.asset_id),
    contentType: text(input.content_type),
    licenseExpiresAt: known(input.license_expires_at),
    assetSourceRef: known(input.asset_source_ref),
    returnPath: known(input.return_path),
    published: flag(input.published),
    charged: flag(input.charged),
    matrixPlanID: known(input.matrix_plan_id),
    matrixItemID: known(input.matrix_item_id),
    externalPostID: known(input.external_post_id),
    matrixReach: known(input.matrix_reach),
    matrixLikes: known(input.matrix_likes),
    matrixLeads: known(input.matrix_leads),
    idempotentReplay: flag(input.idempotent_replay),
    notice: MATRIX_DRAFT_NOTICE,
  };
}
