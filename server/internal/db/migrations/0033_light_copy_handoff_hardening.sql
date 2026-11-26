-- HUI-1682（ECO-CLEANUP-R3-20261125 / J4 加固）：
-- 1) light_copy_drafts 增补输入/输出版本追溯、task_id、账务结论、用量引用与未知字段标注；
--    sent/published/marketing_permitted 仍由既有 CHECK=0 锁死。
-- 2) 新增 light_copy_draft_revisions 保留历史版本快照与每次模型/人工追溯，人工改稿后不丢失历史账务。
-- 3) light_copy_handoffs 扩展保留来源版本、授权素材、权限引用、付款方引用、报价有效期、
--    外部原生工程/任务引用及 unknown/revoked 状态，同时保持 project_created=0（不在 CRM 建引擎工程）。
-- 4) 新增 light_copy_subject_revocations 与 light_copy_handoff_returns 支持撤权 fail-closed 与受控回流候选。

ALTER TABLE light_copy_drafts ADD COLUMN input_facts_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE light_copy_drafts ADD COLUMN input_facts_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE light_copy_drafts ADD COLUMN unknown_fields_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE light_copy_drafts ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
ALTER TABLE light_copy_drafts ADD COLUMN billing_verdict TEXT NOT NULL DEFAULT '';
ALTER TABLE light_copy_drafts ADD COLUMN usage_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE light_copy_drafts ADD COLUMN sample_category TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS light_copy_draft_revisions (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id),
    draft_id              TEXT NOT NULL,
    subject_kind          TEXT NOT NULL CHECK (subject_kind IN ('opportunity','campaign')),
    subject_id            TEXT NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('reply','email','marketing_brief')),
    content_version       INTEGER NOT NULL,
    origin                TEXT NOT NULL CHECK (origin IN ('manual','model')),
    body                  TEXT NOT NULL,
    input_facts_json      TEXT NOT NULL DEFAULT '{}',
    input_facts_hash      TEXT NOT NULL DEFAULT '',
    unknown_fields_json   TEXT NOT NULL DEFAULT '[]',
    task_id               TEXT NOT NULL DEFAULT '',
    billing_verdict       TEXT NOT NULL DEFAULT '',
    usage_ref             TEXT NOT NULL DEFAULT '',
    sample_category       TEXT NOT NULL DEFAULT '',
    live_charge           INTEGER NOT NULL DEFAULT 0 CHECK (live_charge >= 0),
    user_confirmed        INTEGER NOT NULL DEFAULT 0 CHECK (user_confirmed IN (0,1)),
    sent                  INTEGER NOT NULL DEFAULT 0 CHECK (sent = 0),
    published             INTEGER NOT NULL DEFAULT 0 CHECK (published = 0),
    marketing_permitted   INTEGER NOT NULL DEFAULT 0 CHECK (marketing_permitted = 0),
    updated_by            TEXT NOT NULL,
    created_at            TEXT NOT NULL,
    UNIQUE (tenant_id, subject_kind, subject_id, kind, content_version)
);

CREATE INDEX IF NOT EXISTS idx_light_copy_draft_revisions_lookup
    ON light_copy_draft_revisions(tenant_id, subject_kind, subject_id, kind, content_version DESC);

CREATE TABLE light_copy_handoffs_v2 (
    id                      TEXT PRIMARY KEY,
    tenant_id               TEXT NOT NULL REFERENCES tenants(id),
    idempotency_key         TEXT NOT NULL,
    draft_id                TEXT NOT NULL DEFAULT '',
    tool                    TEXT NOT NULL,
    source_kind             TEXT NOT NULL,
    source_id               TEXT NOT NULL,
    content_version         INTEGER NOT NULL DEFAULT 0,
    fact_json               TEXT NOT NULL,
    authorized_assets_json  TEXT NOT NULL DEFAULT '[]',
    permission_ref          TEXT NOT NULL DEFAULT '',
    payer_ref               TEXT NOT NULL DEFAULT '',
    quote_expires_at        TEXT NOT NULL DEFAULT '',
    target_project_ref      TEXT NOT NULL DEFAULT '',
    external_task_ref       TEXT NOT NULL DEFAULT '',
    request_fingerprint     TEXT NOT NULL DEFAULT '',
    status                  TEXT NOT NULL CHECK (status IN ('failed','recorded','declined','refused','unknown','revoked')),
    reason                  TEXT NOT NULL,
    project_created         INTEGER NOT NULL DEFAULT 0 CHECK (project_created = 0),
    regenerated             INTEGER NOT NULL DEFAULT 0 CHECK (regenerated = 0),
    revoked_at              TEXT NOT NULL DEFAULT '',
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL DEFAULT '',
    UNIQUE (tenant_id, idempotency_key)
);

INSERT INTO light_copy_handoffs_v2 (
    id, tenant_id, idempotency_key, draft_id, tool, source_kind, source_id,
    content_version, fact_json, status, reason, project_created, regenerated, created_at, updated_at
)
SELECT id, tenant_id, idempotency_key, draft_id, tool, source_kind, source_id,
       content_version, fact_json, status, reason, project_created, regenerated, created_at, created_at
FROM light_copy_handoffs;

DROP TABLE light_copy_handoffs;
ALTER TABLE light_copy_handoffs_v2 RENAME TO light_copy_handoffs;

CREATE INDEX IF NOT EXISTS idx_light_copy_handoffs_subject
    ON light_copy_handoffs(tenant_id, source_kind, source_id, created_at DESC);

CREATE TABLE IF NOT EXISTS light_copy_subject_revocations (
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('opportunity','campaign')),
    subject_id   TEXT NOT NULL,
    revoked_by   TEXT NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    revoked_at   TEXT NOT NULL,
    PRIMARY KEY (tenant_id, subject_kind, subject_id)
);

CREATE TABLE IF NOT EXISTS light_copy_handoff_returns (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    handoff_id          TEXT NOT NULL REFERENCES light_copy_handoffs(id),
    idempotency_key     TEXT NOT NULL,
    source_kind         TEXT NOT NULL CHECK (source_kind IN ('opportunity','campaign')),
    source_id           TEXT NOT NULL,
    tool                TEXT NOT NULL,
    asset_ref           TEXT NOT NULL DEFAULT '',
    task_ref            TEXT NOT NULL DEFAULT '',
    candidate_kind      TEXT NOT NULL DEFAULT 'asset',
    summary             TEXT NOT NULL DEFAULT '',
    content_version     INTEGER NOT NULL DEFAULT 0,
    permission_ref      TEXT NOT NULL DEFAULT '',
    payer_ref           TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'candidate' CHECK (status IN ('candidate','selected','rejected')),
    sent                INTEGER NOT NULL DEFAULT 0 CHECK (sent = 0),
    published           INTEGER NOT NULL DEFAULT 0 CHECK (published = 0),
    marketing_permitted INTEGER NOT NULL DEFAULT 0 CHECK (marketing_permitted = 0),
    created_by          TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    UNIQUE (tenant_id, handoff_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_light_copy_handoff_returns_handoff
    ON light_copy_handoff_returns(tenant_id, handoff_id, created_at DESC);
