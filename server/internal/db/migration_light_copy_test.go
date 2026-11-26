package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLightCopyHardeningMigrationConstraints(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "light_copy.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	tables, err := TableNames(d)
	if err != nil {
		t.Fatalf("TableNames: %v", err)
	}
	have := map[string]bool{}
	for _, tb := range tables {
		have[tb] = true
	}
	for _, want := range []string{
		"light_copy_drafts",
		"light_copy_draft_revisions",
		"light_copy_handoffs",
		"light_copy_subject_revocations",
		"light_copy_handoff_returns",
	} {
		if !have[want] {
			t.Fatalf("missing table %s (have %v)", want, tables)
		}
	}

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_lc','T','2026-11-26T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// Drafts must keep sent/published/marketing_permitted locked at 0.
	_, err = d.Exec(`
		INSERT INTO light_copy_drafts(
			id, tenant_id, subject_kind, subject_id, kind, body, origin, content_version,
			generated, sent, published, marketing_permitted, user_confirmed, live_charge,
			updated_by, created_at, updated_at
		) VALUES('lcd_1','tnt_lc','opportunity','opp_1','reply','hi','manual',1,0,1,0,0,0,0,'m1','2026-11-26','2026-11-26')`)
	if err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("sent=1 should violate CHECK constraint, got %v", err)
	}

	// Handoffs allow unknown/revoked status while still forbidding project_created=1.
	_, err = d.Exec(`
		INSERT INTO light_copy_handoffs(
			id, tenant_id, idempotency_key, draft_id, tool, source_kind, source_id,
			content_version, fact_json, status, reason, project_created, regenerated, created_at, updated_at
		) VALUES('lch_1','tnt_lc','k1','lcd_1','goboost','opportunity','opp_1',1,'{}','unknown','handoff_status_unknown',0,0,'2026-11-26','2026-11-26')`)
	if err != nil {
		t.Fatalf("insert unknown handoff: %v", err)
	}

	_, err = d.Exec(`
		INSERT INTO light_copy_handoffs(
			id, tenant_id, idempotency_key, draft_id, tool, source_kind, source_id,
			content_version, fact_json, status, reason, project_created, regenerated, created_at, updated_at
		) VALUES('lch_2','tnt_lc','k2','lcd_1','goboost','opportunity','opp_1',1,'{}','recorded','open_existing_entry',1,0,'2026-11-26','2026-11-26')`)
	if err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("project_created=1 should violate CHECK constraint, got %v", err)
	}

	// Returns must also lock sent/published/marketing_permitted at 0.
	_, err = d.Exec(`
		INSERT INTO light_copy_handoff_returns(
			id, tenant_id, handoff_id, idempotency_key, source_kind, source_id, tool,
			asset_ref, task_ref, candidate_kind, summary, content_version, status,
			sent, published, marketing_permitted, created_by, created_at
		) VALUES('lcr_1','tnt_lc','lch_1','rk1','opportunity','opp_1','goboost','ast_1','tsk_1','asset','ok',1,'candidate',0,1,0,'m1','2026-11-26')`)
	if err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("return published=1 should violate CHECK constraint, got %v", err)
	}
}
