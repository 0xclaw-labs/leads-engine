package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/bianjiefilm/leads-engine/server/internal/notifyingest"
)

func TestTouchAckFailureRetriesCommittedOriginalReceipt(t *testing.T) {
	src := newRecordSource(t)
	h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
	tenant, _, _ := h.seed()
	ref := "sub-ack-retry"
	body := ingestEvent(tenant, "original-profile-event", ref, 1, "lead.authorized_submitted", "nfc", false, "", "13900000171", "")
	src.put(ref, recordJSON(tenant, ref, "", "13900000171", "", "", "granted", "lead_submission", true, true, true))
	src.setACKStatus(http.StatusConflict) // Touch has not marked its original outbox forwarded yet.
	src.onACK = func(ack notifyingest.Acknowledgement) {
		var contacts, leads, inbox int
		h.api.St.DB.QueryRow(`SELECT count(*) FROM contacts WHERE tenant_id=? AND phone='13900000171'`, tenant).Scan(&contacts)
		h.api.St.DB.QueryRow(`SELECT count(*) FROM leads WHERE tenant_id=?`, tenant).Scan(&leads)
		h.api.St.DB.QueryRow(`SELECT count(*) FROM notify_inbox WHERE profile_event_id=?`, ack.SourceEventID).Scan(&inbox)
		if contacts != 1 || leads != 1 || inbox != 1 {
			t.Errorf("ACK ran before committed intake: contacts=%d leads=%d inbox=%d", contacts, leads, inbox)
		}
	}
	first := postIngest(t, h, body, signBody("test-ingest-secret", body))
	if first.status != 503 {
		t.Fatalf("ACK refusal must retry after local commit: status=%d %s", first.status, first.raw)
	}
	if tableCount(t, h, "notify_inbox") != 1 {
		t.Fatal("failed ACK lost durable inbox")
	}
	src.setACKStatus(201)
	again := postIngest(t, h, body, signBody("test-ingest-secret", body))
	if again.status != 200 {
		t.Fatalf("retry=%d %s", again.status, again.raw)
	}
	if tableCount(t, h, "notify_inbox") != 1 || countLeads(t, h, tenant) != 1 {
		t.Fatal("retry duplicated intake")
	}
	if len(src.acknowledgements()) != 2 || src.acknowledgements()[0] != src.acknowledgements()[1] || src.acknowledgements()[0].ReceiptID == "" || src.acknowledgements()[0].SourceEventID != "original-profile-event" || src.acknowledgements()[0].ReceiverState != "stored" {
		t.Fatalf("original durable ACK not recovered: %+v", src.acknowledgements())
	}
	var receipt struct {
		SourceACK notifyingest.Acknowledgement `json:"source_ack"`
	}
	var raw string
	h.api.St.DB.QueryRow(`SELECT receipt_json FROM notify_inbox WHERE profile_event_id=?`, "original-profile-event").Scan(&raw)
	json.Unmarshal([]byte(raw), &receipt)
	if receipt.SourceACK != src.acknowledgements()[0] {
		t.Fatalf("ACK basis not durable: %s", raw)
	}
}

func TestTouchTombstoneRevokesWithoutContactHashAndOldReplayCannotResurrect(t *testing.T) {
	src := newRecordSource(t)
	h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
	tenant, _, _ := h.seed()
	ref := "sub-tombstone"
	submit := ingestEvent(tenant, "submit-original", ref, 1, "lead.authorized_submitted", "nfc", false, "访客", "13900000172", "")
	src.put(ref, recordJSON(tenant, ref, "访客", "13900000172", "", "", "granted", "lead_submission", true, true, true))
	accepted := postIngest(t, h, submit, signBody("test-ingest-secret", submit))
	if accepted.status != 200 {
		t.Fatalf("submit=%d %s", accepted.status, accepted.raw)
	}
	src.put(ref, recordJSON(tenant, ref, "", "", "", "", "revoked", "lead_submission", false, false, false))
	revoke := ingestEvent(tenant, "revoke-original", ref, 2, "lead.consent_revoked", "nfc", true, "访客", "13900000172", "")
	revoked := postIngest(t, h, revoke, signBody("test-ingest-secret", revoke))
	if revoked.status != 200 {
		t.Fatalf("zero-contact tombstone rejected: %d %s", revoked.status, revoked.raw)
	}
	cid := accepted.json["contact_id"].(string)
	for _, ch := range []string{"form_authorization", "channel_reply", "marketing_phone", "marketing_sms"} {
		if !consentRevoked(t, h, tenant, cid, ch) {
			t.Errorf("permission %s still live", ch)
		}
	}
	replay := postIngest(t, h, submit, signBody("test-ingest-secret", submit))
	if replay.status != 200 {
		t.Fatalf("old replay=%d %s", replay.status, replay.raw)
	}
	for _, ack := range src.acknowledgements()[1:] {
		if ack.ReceiverState != "revoked" || ack.SourceRevision != 2 {
			t.Errorf("late replay resurrected source: %+v", ack)
		}
	}
}

func TestTouchLegacyAcceptedInboxBackfillsOriginalAckAndConvergesRevocation(t *testing.T) {
	src := newRecordSource(t)
	h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
	tenant, _, _ := h.seed()
	ref := "legacy-accepted"
	body := ingestEvent(tenant, "legacy-original", ref, 1, "lead.authorized_submitted", "nfc", false, "访客", "13900000173", "")
	src.put(ref, recordJSON(tenant, ref, "访客", "13900000173", "", "", "granted", "lead_submission", true, true, true))
	first := postIngest(t, h, body, signBody("test-ingest-secret", body))
	if first.status != 200 {
		t.Fatalf("first=%d %s", first.status, first.raw)
	}
	// Model a pre-upgrade accepted inbox, not a Touch state change: its original
	// local business transaction exists but no source ACK intent was implemented.
	var receipt map[string]any
	json.Unmarshal([]byte(first.raw), &receipt)
	delete(receipt, "source_ack")
	raw, _ := json.Marshal(receipt)
	if _, err := h.api.St.DB.Exec(`UPDATE notify_inbox SET receipt_json=? WHERE profile_event_id=?`, string(raw), "legacy-original"); err != nil {
		t.Fatal(err)
	}
	src.clearACKs()
	replay := postIngest(t, h, body, signBody("test-ingest-secret", body))
	if replay.status != 200 || len(src.acknowledgements()) != 1 || src.acknowledgements()[0].SourceEventID != "legacy-original" {
		t.Fatalf("backfill=%d %s acks=%+v", replay.status, replay.raw, src.acknowledgements())
	}
	src.put(ref, recordJSON(tenant, ref, "", "", "", "", "revoked", "lead_submission", false, false, false))
	// No revocation Notify delivery is required to suppress a stale stored ACK.
	replay = postIngest(t, h, body, signBody("test-ingest-secret", body))
	if replay.status != 200 || src.acknowledgements()[len(src.acknowledgements())-1].ReceiverState != "revoked" {
		t.Fatalf("tombstone replay=%d %s", replay.status, replay.raw)
	}
	cid := first.json["contact_id"].(string)
	for _, ch := range []string{"form_authorization", "channel_reply", "marketing_phone", "marketing_sms"} {
		if !consentRevoked(t, h, tenant, cid, ch) {
			t.Errorf("replay failed to revoke %s", ch)
		}
	}
	if countLeads(t, h, tenant) != 1 {
		t.Fatal("legacy replay created another lead")
	}
	// An inconsistent live source cannot override the persisted waterline.
	src.put(ref, recordJSON(tenant, ref, "访客", "13900000173", "", "", "granted", "lead_submission", true, true, true))
	before := len(src.acknowledgements())
	replay = postIngest(t, h, body, signBody("test-ingest-secret", body))
	if replay.status != 503 || len(src.acknowledgements()) != before {
		t.Fatalf("stored ACK escaped after revoke: %d %+v", replay.status, src.acknowledgements())
	}
}

func TestTouchSameRefInDifferentSourceTenantsKeepsReceiptsIsolated(t *testing.T) {
	src := newRecordSource(t)
	h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
	tenantA, tenantB, _ := h.seed()
	ref := "same-source-ref"
	for i, tenant := range []string{tenantA, tenantB} {
		phone := []string{"13900000174", "13900000175"}[i]
		// Even identical profile event IDs are scoped by original source tenant.
		body := ingestEvent(tenant, "same-event", ref, 1, "lead.authorized_submitted", "nfc", false, "访客", phone, "")
		src.put(ref, recordJSON(tenant, ref, "访客", phone, "", "", "granted", "lead_submission", true, true, true))
		for j := 0; j < 2; j++ {
			res := postIngest(t, h, body, signBody("test-ingest-secret", body))
			if res.status != 200 {
				t.Fatalf("tenant=%s retry=%d: %d %s", tenant, j, res.status, res.raw)
			}
		}
	}
	if tableCount(t, h, "notify_inbox") != 2 || countLeads(t, h, tenantA) != 1 || countLeads(t, h, tenantB) != 1 {
		t.Fatal("source tenants collided")
	}
	if len(src.acknowledgements()) != 4 || src.acknowledgements()[0].ReceiptID == src.acknowledgements()[2].ReceiptID || src.acknowledgements()[0] != src.acknowledgements()[1] || src.acknowledgements()[2] != src.acknowledgements()[3] {
		t.Fatalf("source tenant receipt identity: %+v", src.acknowledgements())
	}
}

func TestTouchInvalidMetadataOrRolledBackIntakeNeverSendsAck(t *testing.T) {
	for _, mode := range []string{"missing metadata", "wrong tenant", "missing receiver identity", "transaction rollback"} {
		t.Run(mode, func(t *testing.T) {
			src := newRecordSource(t)
			h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
			tenant, _, _ := h.seed()
			ref := "invalid-record"
			body := ingestEvent(tenant, "bad-record", ref, 1, "lead.authorized_submitted", "nfc", false, "访客", "13900000176", "")
			raw := recordJSON(tenant, ref, "访客", "13900000176", "", "", "granted", "lead_submission", true, false, false)
			var rec map[string]any
			json.Unmarshal([]byte(raw), &rec)
			switch mode {
			case "missing metadata":
				delete(rec, "source_revision")
			case "wrong tenant":
				rec["tenant_id"] = "another-source-tenant"
			case "missing receiver identity":
				h.api.Cfg.IngestSources[0].FetchServiceID = ""
			case "transaction rollback":
				if _, err := h.api.St.DB.Exec(`CREATE TRIGGER deny_inbox BEFORE INSERT ON notify_inbox BEGIN SELECT RAISE(ABORT,'reject inbox'); END`); err != nil {
					t.Fatal(err)
				}
			}
			b, _ := json.Marshal(rec)
			src.put(ref, string(b))
			before := tableCount(t, h, "contacts")
			res := postIngest(t, h, body, signBody("test-ingest-secret", body))
			if res.status == 200 {
				t.Fatalf("invalid record accepted: %s", res.raw)
			}
			if len(src.acknowledgements()) != 0 || tableCount(t, h, "notify_inbox") != 0 || tableCount(t, h, "contacts") != before || countLeads(t, h, tenant) != 0 {
				t.Fatalf("rejected intake leaked writes or ACK, status=%d", res.status)
			}
		})
	}
}

func TestTouchLostAckResponseRetriesIdenticalDurableIntent(t *testing.T) {
	src := newRecordSource(t)
	h := newHarnessOpts(t, harnessOpts{featureNotifyIngest: true, ingestFetchBase: src.url})
	tenant, _, _ := h.seed()
	ref := "ack-response-lost"
	body := ingestEvent(tenant, "ack-lost-original", ref, 1, "lead.authorized_submitted", "nfc", false, "访客", "13900000177", "")
	src.put(ref, recordJSON(tenant, ref, "访客", "13900000177", "", "", "granted", "lead_submission", true, false, false))
	src.setACKStatus(-1) // Source receives the original ACK; the TCP response is lost.
	res := postIngest(t, h, body, signBody("test-ingest-secret", body))
	if res.status != 503 {
		t.Fatalf("lost response=%d %s", res.status, res.raw)
	}
	src.setACKStatus(200)
	res = postIngest(t, h, body, signBody("test-ingest-secret", body))
	if res.status != 200 || len(src.acknowledgements()) != 2 || src.acknowledgements()[0] != src.acknowledgements()[1] || countLeads(t, h, tenant) != 1 || tableCount(t, h, "notify_inbox") != 1 {
		t.Fatalf("response loss changed intent or intake: status=%d acks=%+v", res.status, src.acknowledgements())
	}
}
