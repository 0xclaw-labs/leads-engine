package notifyingest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTouchRecordUsesRegisteredReceiverContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/internal/lead-records/sub-1" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Service-ID") != "leads-receiver" || r.Header.Get("X-Service-Token") != "dedicated-token" || r.Header.Get("X-Leads-Tenant") != "source-tenant" || r.Header.Get("X-Internal-Token") != "" {
			http.Error(w, "identity", 401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"submission_ref": "sub-1", "tenant_id": "source-tenant", "purpose": "lead_submission", "authorization": "granted", "source_revision": 1, "consent_revision": "n1", "phone": "13900000001", "consents": map[string]any{"form_authorized": true, "notice_version": "n1"}})
	}))
	defer srv.Close()
	// The registered service identity must select Touch's v2 protocol.
	_, err := FetchRecord(context.Background(), srv.URL, "dedicated-token", "source-tenant", "sub-1", "leads-receiver")
	if err != nil {
		t.Fatalf("real Touch contract rejected: %v", err)
	}
}

func TestTouchFetchRefusesWrongIdentityTenantRefAndRedirect(t *testing.T) {
	for _, tc := range []struct {
		name, token, tenant, replyTenant, replyRef string
		redirect                                   bool
	}{
		{name: "wrong service token", token: "wrong", tenant: "t1", replyTenant: "t1", replyRef: "r1"},
		{name: "wrong declared source tenant", token: "receiver-token", tenant: "other", replyTenant: "t1", replyRef: "r1"},
		{name: "wrong response ref", token: "receiver-token", tenant: "t1", replyTenant: "t1", replyRef: "different"},
		{name: "redirect", token: "receiver-token", tenant: "t1", redirect: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redirected := false
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true; w.WriteHeader(200) }))
			defer target.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Service-Token") != "receiver-token" || r.Header.Get("X-Service-ID") != "receiver" {
					w.WriteHeader(401)
					return
				}
				if tc.redirect {
					http.Redirect(w, r, target.URL, 302)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"tenant_id": tc.replyTenant, "submission_ref": tc.replyRef})
			}))
			defer srv.Close()
			_, err := FetchRecord(context.Background(), srv.URL, tc.token, tc.tenant, "r1", "receiver")
			if err == nil {
				t.Fatal("invalid source response accepted")
			}
			if redirected {
				t.Fatal("service credentials followed redirect")
			}
		})
	}
}

func TestTouchMetadataAndZeroContactTombstone(t *testing.T) {
	var rec Record
	json.Unmarshal([]byte(`{"source_revision":2,"consent_revision":"n1","tombstone":true,"authorization":"revoked","purpose":"lead_submission","consents":{"notice_version":"n1"}}`), &rec)
	if err := ValidateTouchRecord(rec, 1); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Record){
		func(r *Record) { r.SourceRevision = 0 }, func(r *Record) { r.ConsentRevision = "" }, func(r *Record) { r.Purpose = "browse" }, func(r *Record) { r.Authorization = "granted" }, func(r *Record) { r.Phone = "13900000001" }, func(r *Record) { r.Consents.FormAuthorized = true }, func(r *Record) { r.Consents.ChannelReply = true }, func(r *Record) { r.Consents.NoticeVersion = "wrong" },
	} {
		bad := rec
		mutate(&bad)
		if ValidateTouchRecord(bad, 1) == nil {
			t.Fatalf("invalid metadata accepted: %+v", bad)
		}
	}
	if ValidateTouchRecord(rec, 3) == nil {
		t.Fatal("source below event revision accepted")
	}
}

func TestAcknowledgementUsesStrictFiveTupleAndRefusesRedirect(t *testing.T) {
	ack := Acknowledgement{ReceiptID: "durable-receipt", SourceEventID: "original-profile-id", SubmissionID: "r1", SourceRevision: 2, ReceiverState: "revoked"}
	for _, status := range []int{200, 201, 409, 503, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			redirected := false
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
			defer target.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v1/internal/lead-records/r1/acknowledgements" || r.Header.Get("X-Service-ID") != "receiver" || r.Header.Get("X-Service-Token") != "receiver-token" || r.Header.Get("X-Leads-Tenant") != "source-tenant" || r.Header.Get("X-Internal-Token") != "" {
					t.Error("incorrect ACK HTTP contract")
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 5 {
					t.Errorf("ACK schema: %v %+v", err, body)
				}
				var got Acknowledgement
				b, _ := json.Marshal(body)
				json.Unmarshal(b, &got)
				if got != ack {
					t.Errorf("ACK=%+v", got)
				}
				if status == 302 {
					http.Redirect(w, r, target.URL, 302)
					return
				}
				w.WriteHeader(status)
			}))
			defer srv.Close()
			err := AcknowledgeRecord(context.Background(), srv.URL, "receiver-token", "receiver", "source-tenant", ack)
			if (err == nil) != (status == 200 || status == 201) {
				t.Fatalf("status=%d err=%v", status, err)
			}
			if redirected {
				t.Fatal("ACK followed redirect")
			}
		})
	}
}

func TestLegacyRegisteredSourceKeepsOriginalFetchProtocol(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/lead-records/legacy" || r.Header.Get("X-Internal-Token") != "legacy-token" || r.Header.Get("X-Service-ID") != "" {
			t.Error("legacy source protocol changed")
		}
		json.NewEncoder(w).Encode(map[string]any{"tenant_id": "tenant", "submission_ref": "legacy"})
	}))
	defer srv.Close()
	if _, err := FetchRecord(context.Background(), srv.URL, "legacy-token", "tenant", "legacy"); err != nil {
		t.Fatal(err)
	}
}
