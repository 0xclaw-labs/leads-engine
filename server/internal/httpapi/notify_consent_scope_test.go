package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/tenantmap"
)

// Each independently granted submission must retain its own persistent consent
// rows even when contact dedup deliberately resolves to the same customer.
func TestTouchRevocationOnlyStopsOriginalSubmissionPermissions(t *testing.T) {
	for _, mode := range []string{"revoke delivery", "accepted replay reads tombstone"} {
		t.Run(mode, func(t *testing.T) {
			src := newRecordSource(t)
			h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
			tenant, _, _ := h.seed()
			h.api.Cfg.IngestSources = append(h.api.Cfg.IngestSources, config.IngestSource{AppID: "partner-app", Secret: "partner-secret", FetchBase: src.url, FetchToken: "test-fetch-token"})
			phone := "13900000181"
			grants := map[string][]string{}
			bodies := map[string][]byte{}
			contact := ""
			for _, grant := range []struct{ app, ref, event, secret string }{{"touch-engine", "touch-target", "target-submit", "test-ingest-secret"}, {"touch-engine", "touch-independent", "independent-submit", "test-ingest-secret"}, {"partner-app", "partner-independent", "partner-submit", "partner-secret"}} {
				before := consentIDsForScopeTest(t, h)
				body := ingestEventApp(grant.app, tenant, grant.event, grant.ref, 1, "lead.authorized_submitted", "nfc", false, "访客", phone, "")
				src.put(grant.ref, strings.Replace(recordJSON(tenant, grant.ref, "访客", phone, "", "", "granted", "lead_submission", true, true, true), `"channel_reply":false`, `"channel_reply":true`, 1))
				res := postIngest(t, h, body, signBody(grant.secret, body))
				if res.status != 200 {
					t.Fatalf("grant=%d %s", res.status, res.raw)
				}
				if contact != "" && contact != res.json["contact_id"] {
					t.Fatal("test did not exercise one deduplicated contact")
				}
				contact = res.json["contact_id"].(string)
				for id := range consentIDsForScopeTest(t, h) {
					if !before[id] {
						grants[grant.ref] = append(grants[grant.ref], id)
					}
				}
				if len(grants[grant.ref]) != 4 {
					t.Fatalf("independent grant %s collapsed: %+v", grant.ref, grants)
				}
				bodies[grant.ref] = body
			}
			src.put("touch-target", recordJSON(tenant, "touch-target", "", "", "", "", "revoked", "lead_submission", false, false, false))
			body := bodies["touch-target"]
			if mode == "revoke delivery" {
				body = ingestEvent(tenant, "target-revoke", "touch-target", 2, "lead.consent_revoked", "nfc", true, "访客", phone, "")
			}
			for i := 0; i < 2; i++ {
				res := postIngest(t, h, body, signBody("test-ingest-secret", body))
				if res.status != http.StatusOK {
					t.Fatalf("revoke/replay=%d %s", res.status, res.raw)
				}
				assertScopedGrantState(t, h, grants["touch-target"], true)
				assertScopedGrantState(t, h, grants["touch-independent"], false)
				assertScopedGrantState(t, h, grants["partner-independent"], false)
				body = bodies["touch-target"] // old submit cannot resurrect or broaden revocation
			}
		})
	}
}

func TestTouchAndPartnerSameRefKeepIndependentPermissionKeys(t *testing.T) {
	src := newRecordSource(t)
	h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
	tenant, _, _ := h.seed()
	h.api.Cfg.IngestSources = append(h.api.Cfg.IngestSources, config.IngestSource{AppID: "partner-app", Secret: "partner-secret", FetchBase: src.url, FetchToken: "test-fetch-token"})
	ref := "same-opaque-ref"
	phone := "13900000182"
	var targetIDs, partnerIDs []string
	for _, app := range []string{"touch-engine", "partner-app"} {
		before := consentIDsForScopeTest(t, h)
		src.put(ref, recordJSON(tenant, ref, "访客", phone, "", "", "granted", "lead_submission", true, true, true))
		secret := "test-ingest-secret"
		if app == "partner-app" {
			secret = "partner-secret"
		}
		body := ingestEventApp(app, tenant, app+"-same-ref", ref, 1, "lead.authorized_submitted", "nfc", false, "访客", phone, "")
		res := postIngest(t, h, body, signBody(secret, body))
		if res.status != 200 {
			t.Fatalf("grant=%d %s", res.status, res.raw)
		}
		var fresh []string
		for id := range consentIDsForScopeTest(t, h) {
			if !before[id] {
				fresh = append(fresh, id)
			}
		}
		if len(fresh) != 4 {
			t.Fatalf("same raw ref from %s overwrote another source's permissions: new=%d", app, len(fresh))
		}
		if app == "touch-engine" {
			targetIDs = fresh
		} else {
			partnerIDs = fresh
		}
	}
	src.put(ref, recordJSON(tenant, ref, "", "", "", "", "revoked", "lead_submission", false, false, false))
	body := ingestEvent(tenant, "same-ref-revoke", ref, 2, "lead.consent_revoked", "nfc", true, "访客", phone, "")
	res := postIngest(t, h, body, signBody("test-ingest-secret", body))
	if res.status != 200 {
		t.Fatalf("revoke=%d %s", res.status, res.raw)
	}
	assertScopedGrantState(t, h, targetIDs, true)
	assertScopedGrantState(t, h, partnerIDs, false)
}

func consentIDsForScopeTest(t *testing.T, h *harness) map[string]bool {
	t.Helper()
	rows, err := h.api.St.DB.Query(`SELECT id FROM contact_consents`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
func assertScopedGrantState(t *testing.T, h *harness, ids []string, revoked bool) {
	t.Helper()
	for _, id := range ids {
		var got bool
		if err := h.api.St.DB.QueryRow(`SELECT revoked_at IS NOT NULL FROM contact_consents WHERE id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != revoked {
			t.Errorf("permission %s revoked=%t want %t", id, got, revoked)
		}
	}
}

func TestTouchLegacyBareConsentRequiresUniquePersistedOwnership(t *testing.T) {
	for _, mode := range []string{"revoke delivery", "accepted replay reads tombstone"} {
		t.Run(mode, func(t *testing.T) {
			src := newRecordSource(t)
			h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
			tenant, _, _ := h.seed()
			ref := "legacy-unique"
			phone := "13900000183"
			src.put(ref, recordJSON(tenant, ref, "访客", phone, "", "", "granted", "lead_submission", true, true, true))
			submit := ingestEvent(tenant, "legacy-unique-submit", ref, 1, "lead.authorized_submitted", "nfc", false, "访客", phone, "")
			before := consentIDsForScopeTest(t, h)
			res := postIngest(t, h, submit, signBody("test-ingest-secret", submit))
			if res.status != 200 {
				t.Fatalf("submit=%d %s", res.status, res.raw)
			}
			var ids []string
			for id := range consentIDsForScopeTest(t, h) {
				if !before[id] {
					ids = append(ids, id)
				}
			}
			// Faithful pre-upgrade storage shape: old notify wrote raw refs, with its
			// original accepted inbox and all consent IDs/revocation markers retained.
			for _, id := range ids {
				if _, err := h.api.St.DB.Exec(`UPDATE contact_consents SET source_submission_ref=? WHERE id=?`, ref, id); err != nil {
					t.Fatal(err)
				}
			}
			src.clearACKs()
			src.put(ref, recordJSON(tenant, ref, "", "", "", "", "revoked", "lead_submission", false, false, false))
			body := submit
			if mode == "revoke delivery" {
				body = ingestEvent(tenant, "legacy-unique-revoke", ref, 2, "lead.consent_revoked", "nfc", true, "访客", phone, "")
			}
			res = postIngest(t, h, body, signBody("test-ingest-secret", body))
			if res.status != 200 {
				t.Fatalf("unique legacy revoke=%d %s", res.status, res.raw)
			}
			assertScopedGrantState(t, h, ids, true)
			var bare int
			h.api.St.DB.QueryRow(`SELECT count(*) FROM contact_consents WHERE source_submission_ref=?`, ref).Scan(&bare)
			if bare != 0 || len(ids) != 4 || len(src.acknowledgements()) != 1 || src.acknowledgements()[0].ReceiverState != "revoked" {
				t.Fatalf("unique provenance was not claimed precisely: bare=%d acks=%+v", bare, src.acknowledgements())
			}
		})
	}
}

func TestTouchLegacyScopeConflictKeepsWaterlineAndNeverAcknowledgesRevoked(t *testing.T) {
	for _, evidence := range []string{"two source identities", "incomplete source identity"} {
		t.Run(evidence, func(t *testing.T) {
			src := newRecordSource(t)
			h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
			tenant, _, _ := h.seed()
			ref := "legacy-conflict"
			phone := "13900000184"
			h.api.Cfg.IngestSources = append(h.api.Cfg.IngestSources, config.IngestSource{AppID: "partner-app", Secret: "partner-secret", FetchBase: src.url, FetchToken: "test-fetch-token"})
			src.put(ref, recordJSON(tenant, ref, "访客", phone, "", "", "granted", "lead_submission", true, true, true))
			submit := ingestEvent(tenant, "legacy-conflict-submit", ref, 1, "lead.authorized_submitted", "nfc", false, "访客", phone, "")
			before := consentIDsForScopeTest(t, h)
			res := postIngest(t, h, submit, signBody("test-ingest-secret", submit))
			if res.status != 200 {
				t.Fatalf("submit=%d %s", res.status, res.raw)
			}
			var targetIDs []string
			for id := range consentIDsForScopeTest(t, h) {
				if !before[id] {
					targetIDs = append(targetIDs, id)
				}
			}
			if evidence == "two source identities" {
				before = consentIDsForScopeTest(t, h)
				partner := ingestEventApp("partner-app", tenant, "legacy-partner-same-ref", ref, 1, "lead.authorized_submitted", "nfc", false, "访客", phone, "")
				p := postIngest(t, h, partner, signBody("partner-secret", partner))
				if p.status != 200 {
					t.Fatalf("partner=%d %s", p.status, p.raw)
				}
				// Model the actual historical collision: the old key merged two source
				// facts into four bare rows. Both original accepted inbox facts remain.
				for id := range consentIDsForScopeTest(t, h) {
					if !before[id] {
						if _, err := h.api.St.DB.Exec(`DELETE FROM contact_consents WHERE id=?`, id); err != nil {
							t.Fatal(err)
						}
					}
				}
			} else {
				if _, err := h.api.St.DB.Exec(`UPDATE notify_inbox SET source_tenant_id='',map_basis='legacy_stored_tenant' WHERE profile_event_id='legacy-conflict-submit'`); err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range targetIDs {
				if _, err := h.api.St.DB.Exec(`UPDATE contact_consents SET source_submission_ref=? WHERE id=?`, ref, id); err != nil {
					t.Fatal(err)
				}
			}
			src.clearACKs()
			src.put(ref, recordJSON(tenant, ref, "", "", "", "", "revoked", "lead_submission", false, false, false))
			revoke := ingestEvent(tenant, "legacy-conflict-revoke", ref, 2, "lead.consent_revoked", "nfc", true, "访客", phone, "")
			for _, body := range [][]byte{revoke, submit, revoke} {
				pending := postIngest(t, h, body, signBody("test-ingest-secret", body))
				if pending.status != 503 || pending.json["error"] != "consent_scope_conflict" {
					t.Fatalf("conflict=%d %s", pending.status, pending.raw)
				}
				assertScopedGrantState(t, h, targetIDs, false)
				var waterline int
				if err := h.api.St.DB.QueryRow(`SELECT source_version FROM notify_revocations WHERE source_ref=?`, ref).Scan(&waterline); err != nil || waterline != 2 {
					t.Fatalf("pending tombstone lost waterline: %d %v", waterline, err)
				}
			}
			// Observing a later tombstone advances the same durable source waterline,
			// while a later replay of an older source record cannot lower it.
			src.put(ref, strings.Replace(recordJSON(tenant, ref, "", "", "", "", "revoked", "lead_submission", false, false, false), `"source_revision":2`, `"source_revision":3`, 1))
			pending := postIngest(t, h, submit, signBody("test-ingest-secret", submit))
			if pending.status != 503 {
				t.Fatalf("later tombstone=%d", pending.status)
			}
			src.put(ref, recordJSON(tenant, ref, "", "", "", "", "revoked", "lead_submission", false, false, false))
			pending = postIngest(t, h, submit, signBody("test-ingest-secret", submit))
			if pending.status != 503 {
				t.Fatalf("older tombstone=%d", pending.status)
			}
			var waterline int
			h.api.St.DB.QueryRow(`SELECT source_version FROM notify_revocations WHERE source_ref=?`, ref).Scan(&waterline)
			if waterline != 3 || len(src.acknowledgements()) != 0 {
				t.Fatalf("scope conflict acknowledged or lowered waterline: %d %+v", waterline, src.acknowledgements())
			}
			assertScopedGrantState(t, h, targetIDs, false)
			var receipt string
			h.api.St.DB.QueryRow(`SELECT receipt_json FROM notify_inbox WHERE profile_event_id='legacy-conflict-revoke'`).Scan(&receipt)
			if !strings.Contains(receipt, `"scope_error":"consent_scope_conflict"`) || !strings.Contains(receipt, `"ingestion":"revocation_pending"`) {
				t.Fatalf("pending result not durable: %s", receipt)
			}
		})
	}
}

func TestTouchSameRawRefAcrossSourceTenantsCannotReuseLead(t *testing.T) {
	src := newRecordSource(t)
	h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
	target, _, _ := h.seed()
	sourceA, sourceB, ref := "source-scope-a", "source-scope-b", "same-source-ref-scope"
	h.api.Cfg.TenantBindings = []tenantmap.Binding{scopedTenantBinding("touch-engine", sourceA, target, 1), scopedTenantBinding("touch-engine", sourceB, target, 1)}
	src.put(ref, recordJSON(sourceA, ref, "访客", "13900000185", "", "", "granted", "lead_submission", true, true, true))
	a := ingestEvent(sourceA, "origin-a-submit", ref, 1, "lead.authorized_submitted", "nfc", false, "访客", "13900000185", "")
	first := postIngest(t, h, a, signBody("test-ingest-secret", a))
	if first.status != 200 {
		t.Fatalf("a=%d %s", first.status, first.raw)
	}
	ids := consentIDsForScopeTest(t, h)
	var own []string
	for id := range ids {
		own = append(own, id)
	}
	src.clearACKs()
	beforeContacts := tableCount(t, h, "contacts")
	src.put(ref, recordJSON(sourceB, ref, "另一访客", "13900000186", "", "", "granted", "lead_submission", true, true, true))
	b := ingestEvent(sourceB, "origin-b-submit", ref, 1, "lead.authorized_submitted", "nfc", false, "另一访客", "13900000186", "")
	second := postIngest(t, h, b, signBody("test-ingest-secret", b))
	if second.status != 409 || second.json["error"] != "source_scope_conflict" {
		t.Fatalf("different source tenant reused original lead: %d %s", second.status, second.raw)
	}
	if tableCount(t, h, "contacts") != beforeContacts || countLeads(t, h, target) != 1 || len(consentIDsForScopeTest(t, h)) != 4 || len(src.acknowledgements()) != 0 {
		t.Fatal("source scope conflict changed original business facts")
	}
	src.put(ref, recordJSON(sourceB, ref, "", "", "", "", "revoked", "lead_submission", false, false, false))
	revoke := ingestEvent(sourceB, "origin-b-revoke", ref, 2, "lead.consent_revoked", "nfc", true, "另一访客", "13900000186", "")
	pending := postIngest(t, h, revoke, signBody("test-ingest-secret", revoke))
	if pending.status != 503 || pending.json["error"] != "source_scope_conflict" {
		t.Fatalf("foreign source revoke=%d %s", pending.status, pending.raw)
	}
	assertScopedGrantState(t, h, own, false)
	if tableCount(t, h, "notify_revocations") != 0 || tableCount(t, h, "notify_inbox") != 1 {
		t.Fatal("foreign source occupied the original owner's legacy keys")
	}
	if len(src.acknowledgements()) != 0 {
		t.Fatal("source conflict emitted an ACK")
	}
	src.put(ref, recordJSON(sourceA, ref, "", "", "", "", "revoked", "lead_submission", false, false, false))
	ownRevoke := ingestEvent(sourceA, "origin-a-revoke", ref, 2, "lead.consent_revoked", "nfc", true, "访客", "13900000185", "")
	accepted := postIngest(t, h, ownRevoke, signBody("test-ingest-secret", ownRevoke))
	if accepted.status != 200 {
		t.Fatalf("foreign conflict blocked legitimate owner's revoke: %d %s", accepted.status, accepted.raw)
	}
	assertScopedGrantState(t, h, own, true)
	var waterline int
	if err := h.api.St.DB.QueryRow(`SELECT source_version FROM notify_revocations WHERE source_tenant_id=? AND source_ref=?`, sourceA, ref).Scan(&waterline); err != nil || waterline != 2 {
		t.Fatalf("original owner's waterline=%d %v", waterline, err)
	}
	if len(src.acknowledgements()) != 1 || src.acknowledgements()[0].SourceEventID != "origin-a-revoke" || src.acknowledgements()[0].ReceiverState != "revoked" {
		t.Fatalf("legitimate source ACK=%+v", src.acknowledgements())
	}
}

func TestTouchStoredSourceSnapshotNeedsEveryAuthorityField(t *testing.T) {
	for _, field := range []string{"source_app", "source_ns", "source_tenant_id"} {
		t.Run(field, func(t *testing.T) {
			src := newRecordSource(t)
			h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
			tenant, _, _ := h.seed()
			ref := "legacy-source-snapshot"
			phone := "13900000187"
			src.put(ref, recordJSON(tenant, ref, "访客", phone, "", "", "granted", "lead_submission", true, true, true))
			submit := ingestEvent(tenant, "snapshot-submit", ref, 1, "lead.authorized_submitted", "nfc", false, "访客", phone, "")
			res := postIngest(t, h, submit, signBody("test-ingest-secret", submit))
			if res.status != 200 {
				t.Fatalf("submit=%d %s", res.status, res.raw)
			}
			var ids []string
			for id := range consentIDsForScopeTest(t, h) {
				ids = append(ids, id)
			}
			var raw string
			if err := h.api.St.DB.QueryRow(`SELECT auth_scope_snapshot FROM source_refs WHERE source_ref=?`, ref).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var snapshot map[string]any
			if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
				t.Fatal(err)
			}
			delete(snapshot, field)
			b, _ := json.Marshal(snapshot)
			if _, err := h.api.St.DB.Exec(`UPDATE source_refs SET auth_scope_snapshot=? WHERE source_ref=?`, string(b), ref); err != nil {
				t.Fatal(err)
			}
			src.clearACKs()
			src.put(ref, recordJSON(tenant, ref, "", "", "", "", "revoked", "lead_submission", false, false, false))
			pending := postIngest(t, h, submit, signBody("test-ingest-secret", submit))
			if pending.status != 503 || pending.json["error"] != "source_scope_conflict" {
				t.Fatalf("missing %s guessed source authority: %d %s", field, pending.status, pending.raw)
			}
			assertScopedGrantState(t, h, ids, false)
			var waterline int
			if err := h.api.St.DB.QueryRow(`SELECT source_version FROM notify_revocations WHERE source_ref=?`, ref).Scan(&waterline); err != nil || waterline != 2 {
				t.Fatalf("source scope pending lost tombstone: %d %v", waterline, err)
			}
			if len(src.acknowledgements()) != 0 {
				t.Fatal("unknown source authority emitted an ACK")
			}
		})
	}
}
