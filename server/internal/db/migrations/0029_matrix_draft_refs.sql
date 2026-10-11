-- HUI-2391 source-side refs for AG10 persistent copy drafts.
-- One row per (tenant, campaign, revision, asset_hash), the same key AG10 uses.
-- published and charged are CHECKed to 0: this table never records a publish or a charge.
-- Plan, item, and external post ids stay NULL until AG10 or AG01 supplies them.
-- Reach, likes, and leads are not columns here, so unknown metrics stay unknown.

CREATE TABLE IF NOT EXISTS matrix_draft_refs (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    campaign_id         TEXT NOT NULL,
    revision            INTEGER NOT NULL CHECK (revision >= 1),
    asset_id            TEXT NOT NULL,
    asset_hash          TEXT NOT NULL CHECK (length(asset_hash) = 64),
    content_type        TEXT NOT NULL CHECK (content_type = 'copy'),
    grant_ref           TEXT NOT NULL,
    license_ref         TEXT NOT NULL,
    license_expires_at  TEXT,
    brand_id            TEXT NOT NULL,
    payer_id            TEXT NOT NULL,
    return_path         TEXT NOT NULL,
    draft_id            TEXT NOT NULL UNIQUE,
    status              TEXT NOT NULL,
    published           INTEGER NOT NULL DEFAULT 0 CHECK (published = 0),
    charged             INTEGER NOT NULL DEFAULT 0 CHECK (charged = 0),
    matrix_plan_id      TEXT,
    matrix_item_id      TEXT,
    external_post_id    TEXT,
    created_by          TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id, revision, asset_hash)
);
CREATE INDEX IF NOT EXISTS idx_matrix_draft_refs_campaign ON matrix_draft_refs(tenant_id, campaign_id);
