package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func matrixRefIn(draftID string) MatrixDraftRef {
	return MatrixDraftRef{
		TenantID:    "tnt_1",
		CampaignID:  "lead_camp_1",
		Revision:    3,
		AssetID:     "asset_copy_1",
		AssetHash:   strings.Repeat("a", 64),
		ContentType: "copy",
		GrantRef:    "grant_1",
		LicenseRef:  "license_1",
		BrandID:     "brand_1",
		PayerID:     "payer_1",
		ReturnPath:  "/campaigns/lead_camp_1",
		DraftID:     draftID,
		Status:      "draft",
		CreatedBy:   "mem_1",
	}
}

func TestMatrixDraftRefSaveReturnsExistingRowForSameKey(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	first, existed, err := s.SaveMatrixDraftRef(matrixRefIn("d_first"))
	if err != nil || existed {
		t.Fatalf("first save: existed=%v err=%v", existed, err)
	}
	if first.DraftID != "d_first" || first.Published || first.Charged {
		t.Fatalf("first row = %+v", first)
	}
	// A timeout retry from the browser sends the same fields with a different
	// draft id candidate. The stored row wins and nothing is overwritten.
	second, existed, err := s.SaveMatrixDraftRef(matrixRefIn("d_retry"))
	if err != nil || !existed {
		t.Fatalf("retry: existed=%v err=%v", existed, err)
	}
	if second.DraftID != "d_first" || second.ID != first.ID {
		t.Fatalf("retry replaced the stored draft: %+v", second)
	}
}

func TestMatrixDraftRefIsTenantScoped(t *testing.T) {
	d := intakeTestDB(t)
	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_2','B','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("tenant b: %v", err)
	}
	s := New(d)
	if _, _, err := s.SaveMatrixDraftRef(matrixRefIn("d_a")); err != nil {
		t.Fatalf("save a: %v", err)
	}
	other := matrixRefIn("d_b")
	other.TenantID = "tnt_2"
	if _, existed, err := s.SaveMatrixDraftRef(other); err != nil || existed {
		t.Fatalf("tenant b same key: existed=%v err=%v", existed, err)
	}
	if _, err := s.GetMatrixDraftRef("tnt_1", "d_b"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("tenant a read tenant b draft: err=%v", err)
	}
	rows, err := s.ListMatrixDraftRefs("tnt_1", "lead_camp_1")
	if err != nil || len(rows) != 1 || rows[0].DraftID != "d_a" {
		t.Fatalf("list tnt_1 = %+v err=%v", rows, err)
	}
}

func TestMatrixDraftRefCheckConstraintsRefusePublishAndCharge(t *testing.T) {
	d := intakeTestDB(t)
	_, err := d.Exec(`INSERT INTO matrix_draft_refs(
		id, tenant_id, campaign_id, revision, asset_id, asset_hash, content_type,
		grant_ref, license_ref, brand_id, payer_id, return_path, draft_id, status,
		published, charged, created_by, created_at, updated_at)
		VALUES('r1','tnt_1','c',1,'a',?, 'copy','g','l','b','p','/x','d_x','draft',1,0,'m','t','t')`,
		strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("published=1 was stored")
	}
	_, err = d.Exec(`INSERT INTO matrix_draft_refs(
		id, tenant_id, campaign_id, revision, asset_id, asset_hash, content_type,
		grant_ref, license_ref, brand_id, payer_id, return_path, draft_id, status,
		published, charged, created_by, created_at, updated_at)
		VALUES('r2','tnt_1','c',1,'a',?, 'image','g','l','b','p','/x','d_y','draft',0,0,'m','t','t')`,
		strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("non-copy content type was stored")
	}
}

func TestMatrixDraftRefReceiptUpdateKeepsPublishFalse(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	if _, _, err := s.SaveMatrixDraftRef(matrixRefIn("d_r")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.UpdateMatrixDraftReceipt("tnt_1", "d_r", "draft"); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	got, err := s.GetMatrixDraftRef("tnt_1", "d_r")
	if err != nil || got.Published || got.Charged || got.MatrixPlanID != "" || got.MatrixItemID != "" || got.ExternalPostID != "" {
		t.Fatalf("after receipt = %+v err=%v", got, err)
	}
}

func TestMatrixDraftRefFindByKeyReturnsStoredRowOrNotFound(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	in := matrixRefIn("d_k")
	if _, found, err := s.FindMatrixDraftRefByKey("tnt_1", in.CampaignID, in.Revision, in.AssetHash); err != nil || found {
		t.Fatalf("before save: found=%v err=%v", found, err)
	}
	if _, _, err := s.SaveMatrixDraftRef(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, found, err := s.FindMatrixDraftRefByKey("tnt_1", in.CampaignID, in.Revision, in.AssetHash)
	if err != nil || !found || got.DraftID != "d_k" {
		t.Fatalf("after save: got=%+v found=%v err=%v", got, found, err)
	}
	if _, found, _ := s.FindMatrixDraftRefByKey("tnt_2", in.CampaignID, in.Revision, in.AssetHash); found {
		t.Fatal("tenant b found tenant a row")
	}
}
