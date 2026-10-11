-- HUI-1682（2026-10-11）：平台任务成为轻文案的真实文本接缝。
-- origin 可以是 'model'，generated 可为 1，live_charge 记录真实记账分值。
-- sent/published/marketing_permitted 仍锁死为 0：生成不等于发送或发布。
-- 旧行全部是 manual/0/0，原样迁移，不改任何正文。

CREATE TABLE light_copy_drafts_model_provenance (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id),
    subject_kind          TEXT NOT NULL CHECK (subject_kind IN ('opportunity','campaign')),
    subject_id            TEXT NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('reply','email','marketing_brief')),
    body                  TEXT NOT NULL,
    origin                TEXT NOT NULL CHECK (origin IN ('manual','model')),
    content_version       INTEGER NOT NULL,
    generated             INTEGER NOT NULL DEFAULT 0 CHECK (generated IN (0,1)),
    sent                  INTEGER NOT NULL DEFAULT 0 CHECK (sent = 0),
    published             INTEGER NOT NULL DEFAULT 0 CHECK (published = 0),
    marketing_permitted   INTEGER NOT NULL DEFAULT 0 CHECK (marketing_permitted = 0),
    user_confirmed        INTEGER NOT NULL DEFAULT 0 CHECK (user_confirmed IN (0,1)),
    live_charge           INTEGER NOT NULL DEFAULT 0 CHECK (live_charge >= 0),
    updated_by            TEXT NOT NULL,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    UNIQUE (tenant_id, subject_kind, subject_id, kind)
);

INSERT INTO light_copy_drafts_model_provenance
    SELECT id, tenant_id, subject_kind, subject_id, kind, body, origin, content_version,
           generated, sent, published, marketing_permitted, user_confirmed, live_charge,
           updated_by, created_at, updated_at
    FROM light_copy_drafts;

DROP TABLE light_copy_drafts;
ALTER TABLE light_copy_drafts_model_provenance RENAME TO light_copy_drafts;

CREATE INDEX IF NOT EXISTS idx_light_copy_drafts_subject
    ON light_copy_drafts(tenant_id, subject_kind, subject_id);
