package matrixhandoff_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/matrixhandoff"
)

func TestClassifyPublishStageStratification(t *testing.T) {
	cases := []struct {
		raw          string
		officialAuth bool
		wantStage    string
		wantPub      bool
	}{
		{"draft", false, matrixhandoff.StageDraftSaved, false},
		{"draft_saved", false, matrixhandoff.StageDraftSaved, false},
		{"draft_recorded", false, matrixhandoff.StageDraftSaved, false},
		{"pending_approval", false, matrixhandoff.StagePendingApproval, false},
		{"awaiting_approval", false, matrixhandoff.StagePendingApproval, false},
		{"scheduled", false, matrixhandoff.StageScheduled, false},
		{"published", false, matrixhandoff.StageDraftSaved, false},
		{"live", false, matrixhandoff.StageDraftSaved, false},
		{"published", true, matrixhandoff.StagePublished, true},
		{"revoked", false, matrixhandoff.StageUnknown, false},
		{"发布成功", false, matrixhandoff.StageUnknown, false},
		{"白标经营链完成", true, matrixhandoff.StageUnknown, false},
		{"", false, matrixhandoff.StageUnknown, false},
	}
	for _, tc := range cases {
		stage, pub := matrixhandoff.ClassifyPublishStage(tc.raw, tc.officialAuth)
		if stage != tc.wantStage || pub != tc.wantPub {
			t.Fatalf("ClassifyPublishStage(%q, %v) = (%q, %v), want (%q, %v)",
				tc.raw, tc.officialAuth, stage, pub, tc.wantStage, tc.wantPub)
		}
	}

	if !matrixhandoff.CanAdvanceStage(matrixhandoff.StageDraftSaved, matrixhandoff.StagePendingApproval) {
		t.Fatal("draft_saved -> pending_approval must be allowed")
	}
	if !matrixhandoff.CanAdvanceStage(matrixhandoff.StagePendingApproval, matrixhandoff.StageScheduled) {
		t.Fatal("pending_approval -> scheduled must be allowed")
	}
	if matrixhandoff.CanAdvanceStage(matrixhandoff.StageScheduled, matrixhandoff.StageDraftSaved) {
		t.Fatal("scheduled -> draft_saved downgrade must be rejected")
	}
	if matrixhandoff.CanAdvanceStage(matrixhandoff.StageDraftSaved, matrixhandoff.StageUnknown) {
		t.Fatal("draft_saved -> unknown must not overwrite a known stage")
	}
}

func TestContentTypeAndExpiredLicenseFailClosed(t *testing.T) {
	svc, fx := newSvc(t)

	for _, badType := range []string{"image", "video", "needs_video", "audio"} {
		sel := validSelection()
		sel.ContentType = badType
		_, err := svc.Select(sel)
		if !errors.Is(err, matrixhandoff.ErrWrongContentType) {
			t.Fatalf("content_type=%q err=%v, want ErrWrongContentType", badType, err)
		}
	}

	expiredAt := time.Now().UTC().Add(-1 * time.Minute)
	expired := validSelection()
	expired.LicenseExpiresAt = &expiredAt
	if _, err := svc.Select(expired); !errors.Is(err, matrixhandoff.ErrLicenseExpired) {
		t.Fatalf("expired license err=%v, want ErrLicenseExpired", err)
	}

	if fx.Calls != 0 || len(svc.Drafts()) != 0 {
		t.Fatalf("rejected selections called fixture or stored drafts: calls=%d drafts=%d", fx.Calls, len(svc.Drafts()))
	}
	if svc.UGCRewardWrites() != 0 {
		t.Fatalf("ugc_reward_writes=%d, want 0", svc.UGCRewardWrites())
	}
	assertEffects(t, svc)
}

func TestStaleReceiptNeverOverwritesAdoptedNewerVersion(t *testing.T) {
	svc, _ := newSvc(t)

	v3Sel := validSelection()
	v3Sel.ContentVersion = 3
	v3, err := svc.Select(v3Sel)
	if err != nil {
		t.Fatal(err)
	}

	// Land newer source_version=2 on v3 first.
	r2 := 200
	if _, err := svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_v3_sv2",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         v3.ID,
		Kind:            "result",
		SourceVersion:   2,
		MatrixPlan:      "plan_sv2",
		Reach:           &r2,
	}); err != nil {
		t.Fatal(err)
	}

	// Late older source_version=1 must be rejected and never overwrite sv2.
	r1 := 10
	if _, err := svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_v3_sv1_late",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         v3.ID,
		Kind:            "result",
		SourceVersion:   1,
		MatrixPlan:      "plan_sv1",
		Reach:           &r1,
	}); !errors.Is(err, matrixhandoff.ErrStaleReceipt) {
		t.Fatalf("stale source_version err=%v, want ErrStaleReceipt", err)
	}

	docV3 := docMap(t, svc, v3.ID)
	if docV3["matrix_plan"] != "plan_sv2" || docV3["reach"] != float64(200) {
		t.Fatalf("stale receipt overwrote adopted sv2: %v", docV3)
	}

	// Adopt newer content version v4 on the same campaign.
	v4Sel := validSelection()
	v4Sel.ContentVersion = 4
	v4, err := svc.Select(v4Sel)
	if err != nil {
		t.Fatal(err)
	}

	// Late receipt against superseded v3 must fail closed.
	rLate := 999
	if _, err := svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_v3_superseded",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         v3.ID,
		Kind:            "result",
		SourceVersion:   3,
		MatrixPlan:      "plan_v3_late",
		Reach:           &rLate,
	}); !errors.Is(err, matrixhandoff.ErrStaleReceipt) {
		t.Fatalf("superseded v3 receipt err=%v, want ErrStaleReceipt", err)
	}

	// Receipt with lower revision targeting v4 must also fail closed.
	if _, err := svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_v4_old_rev",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         v4.ID,
		Kind:            "result",
		Revision:        3,
		MatrixPlan:      "plan_old_rev",
	}); !errors.Is(err, matrixhandoff.ErrStaleReceipt) {
		t.Fatalf("old revision on v4 err=%v, want ErrStaleReceipt", err)
	}

	docV4 := docMap(t, svc, v4.ID)
	if docV4["matrix_plan"] != "" || docV4["reach"] != nil {
		t.Fatalf("v4 polluted by stale receipt: %v", docV4)
	}
}

func TestRestrictedDeclarationCRMNotesAndNullMetricsJSON(t *testing.T) {
	svc, _ := newSvc(t)

	badNotes := validSelection()
	badNotes.CRMNotes = "客户预算十万-内部备注"
	if _, err := svc.Select(badNotes); !errors.Is(err, matrixhandoff.ErrForbidden) {
		t.Fatalf("crm notes err=%v, want ErrForbidden", err)
	}

	smuggledPhone := validSelection()
	smuggledPhone.UsageRestriction = "contact-13800138000"
	if _, err := svc.Select(smuggledPhone); !errors.Is(err, matrixhandoff.ErrForbidden) {
		t.Fatalf("smuggled phone err=%v, want ErrForbidden", err)
	}

	valid := validSelection()
	valid.BrandID = "brand_real_1"
	valid.MemberID = "mem_real_1"
	valid.CommercialDisclosure = "merchant_commercial_ad"
	valid.UsageRestriction = "original_fidelity_only"
	draft, err := svc.Select(valid)
	if err != nil {
		t.Fatal(err)
	}

	neg := -1
	if _, err := svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_neg",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         draft.ID,
		Kind:            "result",
		Reach:           &neg,
	}); !errors.Is(err, matrixhandoff.ErrInvalidMetric) {
		t.Fatalf("negative metric err=%v, want ErrInvalidMetric", err)
	}

	raw, err := svc.Document(draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, wantSub := range []string{
		`"publish_stage":"draft_saved"`,
		`"ugc_reward_triggered":false`,
		`"conversion_roi":null`,
		`"reach":null`,
		`"likes":null`,
		`"leads":null`,
	} {
		if !strings.Contains(raw, wantSub) {
			t.Fatalf("document missing %s: %s", wantSub, raw)
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["reach"] != nil || parsed["likes"] != nil || parsed["leads"] != nil || parsed["conversion_roi"] != nil {
		t.Fatalf("unknown metrics or ROI must be JSON null: %v", parsed)
	}
	if svc.UGCRewardWrites() != 0 {
		t.Fatalf("merchant publish triggered UGC rewards: %d", svc.UGCRewardWrites())
	}
}

func TestConcurrentSelectAndEventsRaceSafe(t *testing.T) {
	svc, fx := newSvc(t)
	const workers = 12
	var wg sync.WaitGroup
	ids := make([]string, workers)
	errs := make([]error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			d, err := svc.Select(validSelection())
			ids[idx] = d.ID
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d err=%v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("worker %d draft id=%s, want %s", i, ids[i], ids[0])
		}
	}
	if fx.Calls != 1 {
		t.Fatalf("concurrent idempotent select called fixture %d times, want 1", fx.Calls)
	}
}
