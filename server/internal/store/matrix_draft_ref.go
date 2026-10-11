package store

import (
	"database/sql"
	"errors"
)

// MatrixDraftRef is the source-side reference to one AG10 copy draft.
// Published and Charged stay false: the table CHECKs them to 0.
// Plan, item, and external post ids are empty until a real source supplies them.
type MatrixDraftRef struct {
	ID               string
	TenantID         string
	CampaignID       string
	Revision         int
	AssetID          string
	AssetHash        string
	ContentType      string
	GrantRef         string
	LicenseRef       string
	LicenseExpiresAt string
	BrandID          string
	PayerID          string
	ReturnPath       string
	DraftID          string
	Status           string
	Published        bool
	Charged          bool
	MatrixPlanID     string
	MatrixItemID     string
	ExternalPostID   string
	CreatedBy        string
	CreatedAt        string
	UpdatedAt        string
}

const matrixDraftRefColumns = `id, tenant_id, campaign_id, revision, asset_id, asset_hash, content_type,
	grant_ref, license_ref, COALESCE(license_expires_at,''), brand_id, payer_id, return_path,
	draft_id, status, published, charged,
	COALESCE(matrix_plan_id,''), COALESCE(matrix_item_id,''), COALESCE(external_post_id,''),
	created_by, created_at, updated_at`

// SaveMatrixDraftRef inserts one reference. A row with the same
// (tenant, campaign, revision, asset_hash) is returned unchanged with existed=true,
// so a retry never replaces the stored draft.
func (s *Store) SaveMatrixDraftRef(in MatrixDraftRef) (MatrixDraftRef, bool, error) {
	if in.ID == "" {
		in.ID = newID("mdr_")
	}
	ts := now()
	res, err := s.DB.Exec(`
		INSERT INTO matrix_draft_refs(
			id, tenant_id, campaign_id, revision, asset_id, asset_hash, content_type,
			grant_ref, license_ref, license_expires_at, brand_id, payer_id, return_path,
			draft_id, status, published, charged, created_by, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,NULLIF(?,''),?,?,?,?,?,0,0,?,?,?)
		ON CONFLICT(tenant_id, campaign_id, revision, asset_hash) DO NOTHING`,
		in.ID, in.TenantID, in.CampaignID, in.Revision, in.AssetID, in.AssetHash, in.ContentType,
		in.GrantRef, in.LicenseRef, in.LicenseExpiresAt, in.BrandID, in.PayerID, in.ReturnPath,
		in.DraftID, in.Status, in.CreatedBy, ts, ts)
	if err != nil {
		return MatrixDraftRef{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return MatrixDraftRef{}, false, err
	}
	stored, found, err := s.FindMatrixDraftRefByKey(in.TenantID, in.CampaignID, in.Revision, in.AssetHash)
	if err != nil {
		return MatrixDraftRef{}, false, err
	}
	if !found {
		return MatrixDraftRef{}, false, sql.ErrNoRows
	}
	return stored, n == 0, nil
}

// FindMatrixDraftRefByKey returns the row for one idempotency key in one tenant.
func (s *Store) FindMatrixDraftRefByKey(tenantID, campaignID string, revision int, assetHash string) (MatrixDraftRef, bool, error) {
	row := s.DB.QueryRow(`SELECT `+matrixDraftRefColumns+`
		FROM matrix_draft_refs WHERE tenant_id=? AND campaign_id=? AND revision=? AND asset_hash=?`,
		tenantID, campaignID, revision, assetHash)
	ref, err := scanMatrixDraftRef(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MatrixDraftRef{}, false, nil
	}
	if err != nil {
		return MatrixDraftRef{}, false, err
	}
	return ref, true, nil
}

// GetMatrixDraftRef returns one row by draft id, only inside its tenant.
func (s *Store) GetMatrixDraftRef(tenantID, draftID string) (MatrixDraftRef, error) {
	row := s.DB.QueryRow(`SELECT `+matrixDraftRefColumns+`
		FROM matrix_draft_refs WHERE tenant_id=? AND draft_id=?`, tenantID, draftID)
	return scanMatrixDraftRef(row)
}

// ListMatrixDraftRefs returns the rows for one campaign, oldest first.
func (s *Store) ListMatrixDraftRefs(tenantID, campaignID string) ([]MatrixDraftRef, error) {
	rows, err := s.DB.Query(`SELECT `+matrixDraftRefColumns+`
		FROM matrix_draft_refs WHERE tenant_id=? AND campaign_id=? ORDER BY created_at, id`,
		tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MatrixDraftRef
	for rows.Next() {
		ref, err := scanMatrixDraftRef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// UpdateMatrixDraftReceipt records the receipt status only. It never touches
// published or charged, and it never fills plan, item, or external post ids.
func (s *Store) UpdateMatrixDraftReceipt(tenantID, draftID, status string) error {
	res, err := s.DB.Exec(`UPDATE matrix_draft_refs SET status=?, updated_at=?
		WHERE tenant_id=? AND draft_id=?`, status, now(), tenantID, draftID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func scanMatrixDraftRef(sc interface{ Scan(...any) error }) (MatrixDraftRef, error) {
	var ref MatrixDraftRef
	var published, charged int
	err := sc.Scan(&ref.ID, &ref.TenantID, &ref.CampaignID, &ref.Revision, &ref.AssetID, &ref.AssetHash,
		&ref.ContentType, &ref.GrantRef, &ref.LicenseRef, &ref.LicenseExpiresAt, &ref.BrandID, &ref.PayerID,
		&ref.ReturnPath, &ref.DraftID, &ref.Status, &published, &charged,
		&ref.MatrixPlanID, &ref.MatrixItemID, &ref.ExternalPostID,
		&ref.CreatedBy, &ref.CreatedAt, &ref.UpdatedAt)
	if err != nil {
		return MatrixDraftRef{}, err
	}
	ref.Published = published != 0
	ref.Charged = charged != 0
	return ref, nil
}
