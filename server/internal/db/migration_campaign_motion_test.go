package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCampaignMotionSwarmMigrationConstraints(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "cm.db"))
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
		"campaign_motion_handoffs",
		"campaign_motion_queue_states",
		"campaign_motion_queue_events",
		"campaign_motion_renders",
		"campaign_motion_repeat_methods",
		"campaign_motion_revocations",
	} {
		if !have[want] {
			t.Fatalf("missing table %s", want)
		}
	}

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_cm','CM','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO campaign_motion_handoffs(id,tenant_id,campaign_id,export_json,local_conversion_json,updated_by,created_at,updated_at) VALUES('cmh_1','tnt_cm','camp_1','{}','{}','mem_1','2026-01-01','2026-01-01')`); err != nil {
		t.Fatal(err)
	}

	// CHECK(=0) blocks automatic outreach/publish/conversion in queue states.
	if _, err := d.Exec(`
		INSERT INTO campaign_motion_queue_states(
			id,tenant_id,campaign_id,handoff_id,job_id,creative_id,project_id,revision_id,digest,
			policy_version,phase,queue_state,last_idempotency_key,auto_outreach_triggered,updated_by,created_at,updated_at)
		VALUES('cmq_1','tnt_cm','camp_1','cmh_1','job_1','crv_1','prj_1','rev_1','sha256:00','v1','queued','queued','k1',1,'mem_1','2026-01-01','2026-01-01')`); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("auto_outreach_triggered CHECK must reject 1, got %v", err)
	}

	// Nullable eta_seconds allows NULL (unknown != 0) and rejects negative values.
	if _, err := d.Exec(`
		INSERT INTO campaign_motion_queue_states(
			id,tenant_id,campaign_id,handoff_id,job_id,creative_id,project_id,revision_id,digest,
			policy_version,phase,queue_state,eta_seconds,last_idempotency_key,updated_by,created_at,updated_at)
		VALUES('cmq_ok','tnt_cm','camp_1','cmh_1','job_1','crv_1','prj_1','rev_1','sha256:00','v1','waiting','waiting_window',NULL,'k1','mem_1','2026-01-01','2026-01-01')`); err != nil {
		t.Fatalf("NULL eta_seconds must be allowed: %v", err)
	}
	if _, err := d.Exec(`UPDATE campaign_motion_queue_states SET eta_seconds=-5 WHERE id='cmq_ok'`); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("negative eta_seconds must fail CHECK, got %v", err)
	}

	// CHECK blocks private_customer_work from becoming public_template=1 or cross_customer_template.
	if _, err := d.Exec(`
		INSERT INTO campaign_motion_repeat_methods(
			id,tenant_id,campaign_id,handoff_id,mode,name,template_id,template_visibility,
			source_project_id,source_revision_id,public_template,created_by,created_at,updated_at)
		VALUES('rm_1','tnt_cm','camp_1','cmh_1','same_campaign_different_store','bad','tpl_1','private_customer_work','prj_1','rev_1',1,'mem_1','2026-01-01','2026-01-01')`); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("private_customer_work public_template=1 must fail CHECK, got %v", err)
	}
	if _, err := d.Exec(`
		INSERT INTO campaign_motion_repeat_methods(
			id,tenant_id,campaign_id,handoff_id,mode,name,template_id,template_visibility,
			source_project_id,source_revision_id,public_template,created_by,created_at,updated_at)
		VALUES('rm_2','tnt_cm','camp_1','cmh_1','cross_customer_template','bad','tpl_2','private_customer_work','prj_1','rev_1',0,'mem_1','2026-01-01','2026-01-01')`); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("private_customer_work cross_customer_template must fail CHECK, got %v", err)
	}
}
