-- HUI-2747（蜂群消费者｜获客强化）：
-- 1) campaign_motion_queue_states & campaign_motion_queue_events：
--    MX-OPS 队列/夜间/闲时等待回流投影与幂等重放防重；eta_seconds 可空（NULL≠0），
--    保留授权素材版本，三个自动化副作用列由 CHECK(=0) 锁死。
-- 2) campaign_motion_renders：
--    多渠道 Render 候选回流与完整归因键（campaign_id/creative_id/project_id/revision_id/
--    render_id/variant_id/channel/source_facts_version）及成本/调用统计；
--    不反向写入创作工程，自动外发/自动发布/自动记转化恒为 0。
-- 3) campaign_motion_repeat_methods：
--    Repeat Method 复用配方与模板可见性边界；CHECK 锁死私有客户作品永不可变公共模板或跨客户复用。
-- 4) campaign_motion_revocations：
--    活动动效交接撤权记录，撤权后 fail-closed 阻断后续交接/回流/复用。

CREATE TABLE IF NOT EXISTS campaign_motion_queue_states (
    id                           TEXT PRIMARY KEY,
    tenant_id                    TEXT NOT NULL REFERENCES tenants(id),
    campaign_id                  TEXT NOT NULL,
    handoff_id                   TEXT NOT NULL REFERENCES campaign_motion_handoffs(id),
    job_id                       TEXT NOT NULL,
    creative_id                  TEXT NOT NULL,
    project_id                   TEXT NOT NULL,
    revision_id                  TEXT NOT NULL,
    digest                       TEXT NOT NULL,
    execution_preference         TEXT NOT NULL DEFAULT '',
    policy_version               TEXT NOT NULL,
    phase                        TEXT NOT NULL,
    queue_state                  TEXT NOT NULL CHECK (queue_state IN ('queued','waiting_window','running','succeeded','failed','cancelled')),
    wait_reason                  TEXT NOT NULL DEFAULT '',
    next_window                  TEXT NOT NULL DEFAULT '',
    eta_seconds                  INTEGER CHECK (eta_seconds IS NULL OR eta_seconds >= 0),
    retained_asset_versions_json TEXT NOT NULL DEFAULT '[]',
    last_idempotency_key         TEXT NOT NULL,
    auto_outreach_triggered      INTEGER NOT NULL DEFAULT 0 CHECK (auto_outreach_triggered = 0),
    auto_published               INTEGER NOT NULL DEFAULT 0 CHECK (auto_published = 0),
    auto_conversion_recorded     INTEGER NOT NULL DEFAULT 0 CHECK (auto_conversion_recorded = 0),
    updated_by                   TEXT NOT NULL,
    created_at                   TEXT NOT NULL,
    updated_at                   TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id)
);

CREATE TABLE IF NOT EXISTS campaign_motion_queue_events (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    campaign_id         TEXT NOT NULL,
    idempotency_key     TEXT NOT NULL,
    request_fingerprint TEXT NOT NULL,
    job_id              TEXT NOT NULL,
    creative_id         TEXT NOT NULL,
    phase               TEXT NOT NULL,
    queue_state         TEXT NOT NULL,
    created_by          TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS campaign_motion_renders (
    id                       TEXT PRIMARY KEY,
    tenant_id                TEXT NOT NULL REFERENCES tenants(id),
    campaign_id              TEXT NOT NULL,
    handoff_id               TEXT NOT NULL REFERENCES campaign_motion_handoffs(id),
    idempotency_key          TEXT NOT NULL,
    request_fingerprint      TEXT NOT NULL,
    creative_id              TEXT NOT NULL,
    project_id               TEXT NOT NULL,
    revision_id              TEXT NOT NULL,
    digest                   TEXT NOT NULL,
    render_id                TEXT NOT NULL,
    variant_id               TEXT NOT NULL,
    channel                  TEXT NOT NULL,
    source_facts_version     TEXT NOT NULL,
    render_mode              TEXT NOT NULL CHECK (render_mode IN ('parametric_variant','baseline')),
    high_cost_model_calls    INTEGER NOT NULL DEFAULT 0 CHECK (high_cost_model_calls >= 0),
    billed_generation_count  INTEGER NOT NULL DEFAULT 0 CHECK (billed_generation_count >= 0),
    cost_cents               INTEGER NOT NULL DEFAULT 0 CHECK (cost_cents >= 0),
    billing_receipt_id       TEXT NOT NULL DEFAULT '',
    payer_ref                TEXT NOT NULL DEFAULT '',
    budget_attribution_id    TEXT NOT NULL DEFAULT '',
    status                   TEXT NOT NULL DEFAULT 'candidate' CHECK (status IN ('candidate','accepted','rejected')),
    auto_outreach_triggered  INTEGER NOT NULL DEFAULT 0 CHECK (auto_outreach_triggered = 0),
    auto_published           INTEGER NOT NULL DEFAULT 0 CHECK (auto_published = 0),
    auto_conversion_recorded INTEGER NOT NULL DEFAULT 0 CHECK (auto_conversion_recorded = 0),
    created_by               TEXT NOT NULL,
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL,
    UNIQUE (tenant_id, campaign_id, idempotency_key),
    UNIQUE (tenant_id, campaign_id, creative_id, variant_id)
);

CREATE INDEX IF NOT EXISTS idx_campaign_motion_renders_lookup
    ON campaign_motion_renders(tenant_id, campaign_id, created_at ASC);

CREATE TABLE IF NOT EXISTS campaign_motion_repeat_methods (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id),
    campaign_id           TEXT NOT NULL,
    handoff_id            TEXT NOT NULL REFERENCES campaign_motion_handoffs(id),
    mode                  TEXT NOT NULL CHECK (mode IN ('same_campaign_different_store','same_product_different_channel','cross_customer_template')),
    name                  TEXT NOT NULL,
    template_id           TEXT NOT NULL,
    template_visibility   TEXT NOT NULL CHECK (template_visibility IN ('private_customer_work','authorized_team_template','platform_template')),
    team_grant_id         TEXT NOT NULL DEFAULT '',
    source_project_id     TEXT NOT NULL,
    source_revision_id    TEXT NOT NULL,
    source_facts_version  TEXT NOT NULL DEFAULT '',
    parametric_slots_json TEXT NOT NULL DEFAULT '{}',
    public_template       INTEGER NOT NULL DEFAULT 0 CHECK (public_template = 0 OR template_visibility = 'platform_template'),
    created_by            TEXT NOT NULL,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    CHECK (NOT (mode = 'cross_customer_template' AND template_visibility = 'private_customer_work')),
    UNIQUE (tenant_id, campaign_id, mode, template_id)
);

CREATE TABLE IF NOT EXISTS campaign_motion_revocations (
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    campaign_id TEXT NOT NULL,
    revoked_by  TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    revoked_at  TEXT NOT NULL,
    PRIMARY KEY (tenant_id, campaign_id)
);
