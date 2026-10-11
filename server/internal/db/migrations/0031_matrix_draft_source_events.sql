-- HUI-2391 publish-source backflow landing on the source draft refs.
-- asset_source_ref records where the caller declares the asset hash came from;
-- it is a recorded declaration, not a verification.
-- matrix_reach/likes/leads stay NULL until a real source event supplies them:
-- unknown metrics are unknown, never zero.
-- provider events are idempotent per (tenant, provider_event_id); a repeated
-- event id with different fields is a conflict handled in the store layer.

ALTER TABLE matrix_draft_refs ADD COLUMN asset_source_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE matrix_draft_refs ADD COLUMN matrix_reach INTEGER;
ALTER TABLE matrix_draft_refs ADD COLUMN matrix_likes INTEGER;
ALTER TABLE matrix_draft_refs ADD COLUMN matrix_leads INTEGER;

CREATE TABLE IF NOT EXISTS matrix_draft_source_events (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    draft_ref_id      TEXT NOT NULL REFERENCES matrix_draft_refs(id),
    provider_event_id TEXT NOT NULL,
    kind              TEXT NOT NULL CHECK (kind IN ('result','revoke','replay','edit','withdraw')),
    fingerprint       TEXT NOT NULL,
    created_by        TEXT NOT NULL,
    created_at        TEXT NOT NULL,
    UNIQUE (tenant_id, provider_event_id)
);
CREATE INDEX IF NOT EXISTS idx_matrix_draft_source_events_ref ON matrix_draft_source_events(tenant_id, draft_ref_id);
