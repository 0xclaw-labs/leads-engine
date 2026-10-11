package matrixdraftclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func sampleRequest() Request {
	return Request{
		CampaignID: "camp_1",
		Revision:   3,
		AssetID:    "asset_copy_1",
		AssetHash:  strings.Repeat("a", 64),
		GrantRef:   "grant_1",
		TenantID:   "tenant_a",
		BrandID:    "brand_1",
		PayerID:    "payer_1",
		LicenseRef: "license_1",
		ReturnPath: "/campaigns/camp_1",
	}
}

func TestCreateDraftSendsContractShapeAndNothingElse(t *testing.T) {
	var gotHeader, gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotHeader = r.Header.Get("X-Matrix-Tenant")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"draft_id":"d_1","schema_version":"matrix-draft/v1","status":"draft","published":false,"charged":false,"idempotent_replay":false}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.CreateDraft(context.Background(), sampleRequest()); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/matrix-drafts" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if gotHeader != "tenant_a" {
		t.Fatalf("X-Matrix-Tenant = %q", gotHeader)
	}
	keys := make([]string, 0, len(gotBody))
	for k := range gotBody {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	wantKeys := []string{"asset_hash", "asset_id", "brand_id", "campaign_id", "content_type", "grant_ref", "license_expires_at", "license_ref", "payer_id", "return_target", "revision", "schema_version", "source_app", "tenant_id"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("body keys = %v, want %v", keys, wantKeys)
	}
	if gotBody["schema_version"] != "matrix-draft-request/v1" || gotBody["source_app"] != "leads" || gotBody["content_type"] != "copy" {
		t.Fatalf("body = %v", gotBody)
	}
	if gotBody["license_expires_at"] != nil {
		t.Fatalf("license_expires_at = %v, want null when undeclared", gotBody["license_expires_at"])
	}
	rt, _ := gotBody["return_target"].(map[string]any)
	if rt["app"] != "leads" || rt["path"] != "/campaigns/camp_1" {
		t.Fatalf("return_target = %v", rt)
	}
}

func TestCreateDraftDecodesCreateAndReplay(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"draft_id":"d_9","source_app":"leads","campaign_id":"camp_1","revision":3,"asset_id":"asset_copy_1","status":"draft","published":false,"charged":false,"idempotent_replay":false}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"draft_id":"d_9","source_app":"leads","campaign_id":"camp_1","revision":3,"asset_id":"asset_copy_1","status":"draft","published":false,"charged":false,"idempotent_replay":true}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	first, err := c.CreateDraft(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.Replay || first.ID != "d_9" || first.Status != "draft" || first.Published || first.Charged {
		t.Fatalf("first = %+v", first)
	}
	second, err := c.CreateDraft(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !second.Replay || second.ID != "d_9" {
		t.Fatalf("second = %+v, want replay of d_9", second)
	}
}

func TestAPIErrorCarriesStatusAndCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"grant_revoked","message":"grant revoked"}}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.CreateDraft(context.Background(), sampleRequest())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusForbidden || apiErr.Code != "grant_revoked" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("a 403 must not be reported as unavailable")
	}
}

func TestTransportFailureIsUnavailable(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", HTTP: &http.Client{Timeout: 2 * time.Second}}
	_, err := c.CreateDraft(context.Background(), sampleRequest())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestGetReceiptUsesReceiptPathAndTenant(t *testing.T) {
	var gotPath, gotTenant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTenant = r.Header.Get("X-Matrix-Tenant")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"draft_id":"d_9","schema_version":"matrix-draft-receipt/v1","status":"draft","published":false,"charged":false}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	d, err := c.GetReceipt(context.Background(), "tenant_a", "d_9")
	if err != nil {
		t.Fatalf("GetReceipt: %v", err)
	}
	if gotPath != "/v1/matrix-drafts/d_9/receipt" || gotTenant != "tenant_a" {
		t.Fatalf("path=%s tenant=%s", gotPath, gotTenant)
	}
	if d.Published || d.Charged || d.ID != "d_9" {
		t.Fatalf("receipt = %+v", d)
	}
}

func TestRequestRejectsNonLeadsContentTypeLocally(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", HTTP: &http.Client{Timeout: time.Second}}
	r := sampleRequest()
	r.ContentType = "image"
	_, err := c.CreateDraft(context.Background(), r)
	if !errors.Is(err, ErrNotLeadsCopy) {
		t.Fatalf("err = %v, want ErrNotLeadsCopy", err)
	}
}
