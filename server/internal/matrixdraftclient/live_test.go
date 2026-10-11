package matrixdraftclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveAG10Loopback drives a real AG10 draft process over loopback.
// It runs only when MATRIX_DRAFT_LIVE_URL is set, for example by a developer who
// started the AG10 binary on 127.0.0.1 with a file sqlite path and a test grant file.
// It is local evidence only. It is not production, not a publish, and not a receipt from a platform.
func TestLiveAG10Loopback(t *testing.T) {
	base := strings.TrimSpace(os.Getenv("MATRIX_DRAFT_LIVE_URL"))
	if base == "" {
		t.Skip("MATRIX_DRAFT_LIVE_URL not set; live AG10 loopback check skipped")
	}
	c := &Client{BaseURL: base, HTTP: &http.Client{Timeout: 10 * time.Second}}
	ctx := context.Background()
	const tenant = "tnt_live_a"
	// A fresh campaign per run keeps the first create a real create on a reused database.
	campaign := fmt.Sprintf("camp_live_%d", time.Now().UnixNano())
	liveReq := Request{
		CampaignID: campaign,
		Revision:   1,
		AssetID:    "asset_live_copy_1",
		AssetHash:  strings.Repeat("c", 64),
		GrantRef:   "grant_live_1",
		TenantID:   tenant,
		BrandID:    "brand_live_1",
		PayerID:    "payer_live_1",
		LicenseRef: "license_live_1",
		ReturnPath: "/campaigns/" + campaign,
	}

	first, err := c.CreateDraft(ctx, liveReq)
	if err != nil {
		t.Fatalf("live create: %v", err)
	}
	if first.Replay || first.ID == "" || first.Status != "draft" || first.Published || first.Charged {
		t.Fatalf("live first = %+v", first)
	}
	t.Logf("LIVE create draft_id=%s status=%s published=%v charged=%v replay=%v", first.ID, first.Status, first.Published, first.Charged, first.Replay)

	again, err := c.CreateDraft(ctx, liveReq)
	if err != nil {
		t.Fatalf("live replay: %v", err)
	}
	if !again.Replay || again.ID != first.ID {
		t.Fatalf("live replay = %+v, want replay of %s", again, first.ID)
	}
	t.Logf("LIVE replay draft_id=%s replay=%v", again.ID, again.Replay)

	receipt, err := c.GetReceipt(ctx, tenant, first.ID)
	if err != nil {
		t.Fatalf("live receipt: %v", err)
	}
	if receipt.ID != first.ID || receipt.Published || receipt.Charged {
		t.Fatalf("live receipt = %+v", receipt)
	}
	t.Logf("LIVE receipt draft_id=%s published=%v charged=%v", receipt.ID, receipt.Published, receipt.Charged)

	conflict := liveReq
	conflict.AssetID = "asset_live_other"
	_, err = c.CreateDraft(ctx, conflict)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != "idempotency_conflict" {
		t.Fatalf("live same key different asset: err=%v, want 409 idempotency_conflict", err)
	}
	t.Logf("LIVE idempotency conflict status=%d code=%s", apiErr.Status, apiErr.Code)

	revoked := liveReq
	revoked.GrantRef = "grant_live_revoked"
	revoked.Revision = 2
	_, err = c.CreateDraft(ctx, revoked)
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden || apiErr.Code != "grant_revoked" {
		t.Fatalf("live revoked grant: err=%v, want 403 grant_revoked", err)
	}
	t.Logf("LIVE revoked grant status=%d code=%s", apiErr.Status, apiErr.Code)

	_, err = c.GetReceipt(ctx, "tnt_live_b", first.ID)
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("live cross tenant receipt: err=%v, want 403", err)
	}
	t.Logf("LIVE cross-tenant receipt status=%d code=%s", apiErr.Status, apiErr.Code)
}
