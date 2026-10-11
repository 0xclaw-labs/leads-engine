package store

import (
	"errors"
	"strings"
	"testing"
)

func ptrInt(v int) *int { return &v }

func savedRefWithSource(t *testing.T, s *Store, draftID string) MatrixDraftRef {
	t.Helper()
	in := matrixRefIn(draftID)
	in.AssetSourceRef = "touch/landing/camp-matrix"
	got, _, err := s.SaveMatrixDraftRef(in)
	if err != nil {
		t.Fatalf("save ref: %v", err)
	}
	return got
}

// The source pin records where the caller says the asset hash came from.
// It is a recorded declaration, not a verification.
func TestMatrixDraftRefStoresDeclaredAssetSource(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	got := savedRefWithSource(t, s, "d_src")
	if got.AssetSourceRef != "touch/landing/camp-matrix" {
		t.Fatalf("asset source ref = %q", got.AssetSourceRef)
	}
	again, err := s.GetMatrixDraftRef("tnt_1", "d_src")
	if err != nil || again.AssetSourceRef != "touch/landing/camp-matrix" {
		t.Fatalf("read back = %+v err=%v", again, err)
	}
	blank := matrixRefIn("d_blank")
	blank.Revision = 4 // a different idempotency key: same key would replay the stored row
	if _, _, err := s.SaveMatrixDraftRef(blank); err != nil {
		t.Fatalf("save blank: %v", err)
	}
	row, err := s.GetMatrixDraftRef("tnt_1", "d_blank")
	if err != nil || row.AssetSourceRef != "" {
		t.Fatalf("blank source = %+v err=%v", row, err)
	}
}

func resultFacts() MatrixDraftSourceFacts {
	return MatrixDraftSourceFacts{
		Kind:           "result",
		MatrixPlanID:   "plan_1",
		MatrixItemID:   "item_1",
		ExternalPostID: "post_1",
		Reach:          ptrInt(120),
	}
}

// A result event lands the plan/item/post refs the source supplied. Metrics the
// source did not send stay NULL columns, never zero.
func TestMatrixDraftSourceEventLandsRefsAndKeepsUnknownMetricsNull(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	savedRefWithSource(t, s, "d_e1")
	ref, err := s.GetMatrixDraftRef("tnt_1", "d_e1")
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	applied, idempotent, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_1", "mem_1", resultFacts())
	if err != nil || !applied || idempotent {
		t.Fatalf("first event: applied=%v idem=%v err=%v", applied, idempotent, err)
	}
	got, err := s.GetMatrixDraftRef("tnt_1", "d_e1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.MatrixPlanID != "plan_1" || got.MatrixItemID != "item_1" || got.ExternalPostID != "post_1" {
		t.Fatalf("refs not landed: %+v", got)
	}
	if got.MatrixReach == nil || *got.MatrixReach != 120 {
		t.Fatalf("reach = %v, want 120", got.MatrixReach)
	}
	if got.MatrixLikes != nil || got.MatrixLeads != nil {
		t.Fatalf("unknown metrics must stay null: likes=%v leads=%v", got.MatrixLikes, got.MatrixLeads)
	}
	if got.Published || got.Charged {
		t.Fatalf("event stored a publish or charge: %+v", got)
	}
}

// The same provider event with the same fields is idempotent: nothing is applied twice.
func TestMatrixDraftSourceEventSameEventSameFactsIsIdempotent(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	savedRefWithSource(t, s, "d_e2")
	ref, _ := s.GetMatrixDraftRef("tnt_1", "d_e2")
	if _, _, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_dup", "mem_1", resultFacts()); err != nil {
		t.Fatalf("first: %v", err)
	}
	applied, idempotent, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_dup", "mem_1", resultFacts())
	if err != nil || applied || !idempotent {
		t.Fatalf("repeat: applied=%v idem=%v err=%v", applied, idempotent, err)
	}
	got, _ := s.GetMatrixDraftRef("tnt_1", "d_e2")
	if got.MatrixReach == nil || *got.MatrixReach != 120 {
		t.Fatalf("idempotent repeat changed facts: %+v", got)
	}
}

// The same provider event id with different fields is a conflict, not a silent overwrite.
func TestMatrixDraftSourceEventSameEventDifferentFactsConflicts(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	savedRefWithSource(t, s, "d_e3")
	ref, _ := s.GetMatrixDraftRef("tnt_1", "d_e3")
	if _, _, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_x", "mem_1", resultFacts()); err != nil {
		t.Fatalf("first: %v", err)
	}
	other := resultFacts()
	other.MatrixPlanID = "plan_other"
	_, _, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_x", "mem_1", other)
	if !errors.Is(err, ErrEventConflict) {
		t.Fatalf("conflicting replay err=%v, want ErrEventConflict", err)
	}
	got, _ := s.GetMatrixDraftRef("tnt_1", "d_e3")
	if got.MatrixPlanID != "plan_1" {
		t.Fatalf("conflict overwrote the stored ref: %+v", got)
	}
}

// After a revoke the publish-source refs stop landing; the row keeps its facts.
func TestMatrixDraftSourceEventRevokedRowRefusesResult(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	savedRefWithSource(t, s, "d_e4")
	ref, _ := s.GetMatrixDraftRef("tnt_1", "d_e4")
	if _, _, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_rev", "mem_1", MatrixDraftSourceFacts{Kind: "revoke"}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, _, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_after", "mem_1", resultFacts())
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("result after revoke err=%v, want ErrRevoked", err)
	}
	got, _ := s.GetMatrixDraftRef("tnt_1", "d_e4")
	if got.Status != "revoked" {
		t.Fatalf("status = %q, want revoked", got.Status)
	}
	if got.MatrixPlanID != "" {
		t.Fatalf("refuse landed anyway: %+v", got)
	}
}

// Lifecycle signals without refs are recorded; they change no stored facts.
func TestMatrixDraftSourceEventLifecycleKindsRecordWithoutFacts(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	savedRefWithSource(t, s, "d_e5")
	ref, _ := s.GetMatrixDraftRef("tnt_1", "d_e5")
	for i, kind := range []string{"replay", "edit", "withdraw"} {
		applied, _, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_lc_"+kind, "mem_1", MatrixDraftSourceFacts{Kind: kind})
		if err != nil || !applied {
			t.Fatalf("%s: applied=%v err=%v", kind, applied, err)
		}
		if i == 0 {
			continue
		}
	}
	got, _ := s.GetMatrixDraftRef("tnt_1", "d_e5")
	if got.MatrixPlanID != "" || got.MatrixReach != nil || got.Status != "draft" {
		t.Fatalf("lifecycle event changed facts: %+v", got)
	}
}

// An unknown kind is refused at the store boundary too.
func TestMatrixDraftSourceEventUnknownKindRefused(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	savedRefWithSource(t, s, "d_e6")
	ref, _ := s.GetMatrixDraftRef("tnt_1", "d_e6")
	_, _, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_bad", "mem_1", MatrixDraftSourceFacts{Kind: "publish"})
	if !errors.Is(err, ErrBadEvent) {
		t.Fatalf("unknown kind err=%v, want ErrBadEvent", err)
	}
}

// A cross-tenant event cannot reach another tenant's row.
func TestMatrixDraftSourceEventCrossTenantRefused(t *testing.T) {
	d := intakeTestDB(t)
	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_2','B','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("tenant b: %v", err)
	}
	s := New(d)
	savedRefWithSource(t, s, "d_e7")
	ref, _ := s.GetMatrixDraftRef("tnt_1", "d_e7")
	_, _, err := s.AppendMatrixDraftSourceEvent("tnt_2", ref.ID, "evt_ct", "mem_2", resultFacts())
	if !errors.Is(err, ErrRevoked) && err == nil {
		t.Fatalf("cross-tenant event landed: err=%v", err)
	}
	if err == nil {
		got, _ := s.GetMatrixDraftRef("tnt_1", "d_e7")
		if got.MatrixPlanID != "" {
			t.Fatalf("cross-tenant event landed refs: %+v", got)
		}
	}
}

// The events table records one row per applied provider event id.
func TestMatrixDraftSourceEventsAreRecorded(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	savedRefWithSource(t, s, "d_e8")
	ref, _ := s.GetMatrixDraftRef("tnt_1", "d_e8")
	if _, _, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_rec", "mem_1", resultFacts()); err != nil {
		t.Fatalf("append: %v", err)
	}
	events, err := s.ListMatrixDraftSourceEvents("tnt_1", ref.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v err=%v", events, err)
	}
	if events[0].ProviderEventID != "evt_rec" || events[0].Kind != "result" || events[0].Fingerprint == "" {
		t.Fatalf("event row = %+v", events[0])
	}
	if strings.Contains(events[0].Fingerprint, "\x00") {
		t.Fatalf("fingerprint must be a digest, got %q", events[0].Fingerprint)
	}
}
