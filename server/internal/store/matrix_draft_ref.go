package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

var (
	// ErrEventConflict is a repeated provider event id with different fields.
	ErrEventConflict = errors.New("store: provider event id reuse with different fields")
	// ErrRevoked is a publish-source event against a revoked draft ref.
	ErrRevoked = errors.New("store: draft ref revoked")
	// ErrBadEvent is an event this store refuses at the boundary.
	ErrBadEvent = errors.New("store: bad source event")
)

// MatrixDraftRef is the source-side reference to one AG10 copy draft.
// Published and Charged stay false: the table CHECKs them to 0.
// Plan, item, and external post ids stay empty until a real source event
// supplies them; metrics stay nil for the same reason.
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
	AssetSourceRef   string
	DraftID          string
	Status           string
	Published        bool
	Charged          bool
	MatrixPlanID     string
	MatrixItemID     string
	ExternalPostID   string
	MatrixReach      *int
	MatrixLikes      *int
	MatrixLeads      *int
	CreatedBy        string
	CreatedAt        string
	UpdatedAt        string
}

// MatrixDraftSourceFacts are the publish-source facts one event carries.
// Nil metrics mean "not supplied"; they never mean zero.
type MatrixDraftSourceFacts struct {
	Kind           string
	MatrixPlanID   string
	MatrixItemID   string
	ExternalPostID string
	Reach          *int
	Likes          *int
	Leads          *int
}

// MatrixDraftSourceEvent is one recorded provider event row.
type MatrixDraftSourceEvent struct {
	ID              string
	TenantID        string
	DraftRefID      string
	ProviderEventID string
	Kind            string
	Fingerprint     string
	CreatedBy       string
	CreatedAt       string
}

const matrixDraftRefColumns = `id, tenant_id, campaign_id, revision, asset_id, asset_hash, content_type,
	grant_ref, license_ref, COALESCE(license_expires_at,''), brand_id, payer_id, return_path,
	COALESCE(asset_source_ref,''), draft_id, status, published, charged,
	COALESCE(matrix_plan_id,''), COALESCE(matrix_item_id,''), COALESCE(external_post_id,''),
	matrix_reach, matrix_likes, matrix_leads,
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
			asset_source_ref, draft_id, status, published, charged, created_by, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,NULLIF(?,''),?,?,?,?,?,?,0,0,?,?,?)
		ON CONFLICT(tenant_id, campaign_id, revision, asset_hash) DO NOTHING`,
		in.ID, in.TenantID, in.CampaignID, in.Revision, in.AssetID, in.AssetHash, in.ContentType,
		in.GrantRef, in.LicenseRef, in.LicenseExpiresAt, in.BrandID, in.PayerID, in.ReturnPath,
		in.AssetSourceRef, in.DraftID, in.Status, in.CreatedBy, ts, ts)
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

// GetMatrixDraftRefByID returns one row by its primary key, only inside its tenant.
func (s *Store) GetMatrixDraftRefByID(tenantID, id string) (MatrixDraftRef, error) {
	return s.getMatrixDraftRefRow(tenantID, id)
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
		&ref.ReturnPath, &ref.AssetSourceRef, &ref.DraftID, &ref.Status, &published, &charged,
		&ref.MatrixPlanID, &ref.MatrixItemID, &ref.ExternalPostID,
		&ref.MatrixReach, &ref.MatrixLikes, &ref.MatrixLeads,
		&ref.CreatedBy, &ref.CreatedAt, &ref.UpdatedAt)
	if err != nil {
		return MatrixDraftRef{}, err
	}
	ref.Published = published != 0
	ref.Charged = charged != 0
	return ref, nil
}

// AppendMatrixDraftSourceEvent lands one publish-source event on one draft ref
// row. A result fills only the refs and metrics the source actually sent; nil
// metrics stay NULL. A revoke latches the row: later result events are refused.
// The same (tenant, provider event id) with the same fields is idempotent and
// changes nothing; with different fields it is ErrEventConflict.
func (s *Store) AppendMatrixDraftSourceEvent(tenantID, draftRefID, providerEventID, createdBy string, facts MatrixDraftSourceFacts) (bool, bool, error) {
	if strings.TrimSpace(providerEventID) == "" || strings.TrimSpace(createdBy) == "" {
		return false, false, ErrBadEvent
	}
	switch facts.Kind {
	case "result", "revoke", "replay", "edit", "withdraw":
	default:
		return false, false, ErrBadEvent
	}
	fp := sourceEventFingerprint(facts)
	if ev, ok, err := s.findMatrixDraftSourceEvent(tenantID, providerEventID); err != nil {
		return false, false, err
	} else if ok {
		if ev.Fingerprint != fp {
			return false, false, ErrEventConflict
		}
		return false, true, nil
	}
	ref, err := s.getMatrixDraftRefRow(tenantID, draftRefID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, ErrBadEvent
	}
	if err != nil {
		return false, false, err
	}
	if ref.Status == "revoked" {
		return false, false, ErrRevoked
	}
	if facts.Kind == "revoke" {
		if _, err := s.DB.Exec(`UPDATE matrix_draft_refs SET status='revoked', updated_at=?
			WHERE id=? AND tenant_id=?`, now(), ref.ID, tenantID); err != nil {
			return false, false, err
		}
	}
	if facts.Kind == "result" {
		if _, err := s.DB.Exec(`
			UPDATE matrix_draft_refs SET
				matrix_plan_id = COALESCE(NULLIF(?,''), matrix_plan_id),
				matrix_item_id = COALESCE(NULLIF(?,''), matrix_item_id),
				external_post_id = COALESCE(NULLIF(?,''), external_post_id),
				matrix_reach = COALESCE(?, matrix_reach),
				matrix_likes = COALESCE(?, matrix_likes),
				matrix_leads = COALESCE(?, matrix_leads),
				updated_at = ?
			WHERE id=? AND tenant_id=?`,
			facts.MatrixPlanID, facts.MatrixItemID, facts.ExternalPostID,
			facts.Reach, facts.Likes, facts.Leads,
			now(), ref.ID, tenantID); err != nil {
			return false, false, err
		}
	}
	id := newID("mdse_")
	ts := now()
	if _, err := s.DB.Exec(`INSERT INTO matrix_draft_source_events(
			id, tenant_id, draft_ref_id, provider_event_id, kind, fingerprint, created_by, created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		id, tenantID, ref.ID, providerEventID, facts.Kind, fp, createdBy, ts); err != nil {
		return false, false, err
	}
	return true, false, nil
}

// ListMatrixDraftSourceEvents returns the recorded events of one ref, oldest first.
func (s *Store) ListMatrixDraftSourceEvents(tenantID, draftRefID string) ([]MatrixDraftSourceEvent, error) {
	rows, err := s.DB.Query(`SELECT id, tenant_id, draft_ref_id, provider_event_id, kind, fingerprint, created_by, created_at
		FROM matrix_draft_source_events WHERE tenant_id=? AND draft_ref_id=? ORDER BY created_at, id`, tenantID, draftRefID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MatrixDraftSourceEvent
	for rows.Next() {
		var ev MatrixDraftSourceEvent
		if err := rows.Scan(&ev.ID, &ev.TenantID, &ev.DraftRefID, &ev.ProviderEventID, &ev.Kind, &ev.Fingerprint, &ev.CreatedBy, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *Store) findMatrixDraftSourceEvent(tenantID, providerEventID string) (MatrixDraftSourceEvent, bool, error) {
	row := s.DB.QueryRow(`SELECT id, tenant_id, draft_ref_id, provider_event_id, kind, fingerprint, created_by, created_at
		FROM matrix_draft_source_events WHERE tenant_id=? AND provider_event_id=?`, tenantID, providerEventID)
	var ev MatrixDraftSourceEvent
	err := row.Scan(&ev.ID, &ev.TenantID, &ev.DraftRefID, &ev.ProviderEventID, &ev.Kind, &ev.Fingerprint, &ev.CreatedBy, &ev.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MatrixDraftSourceEvent{}, false, nil
	}
	if err != nil {
		return MatrixDraftSourceEvent{}, false, err
	}
	return ev, true, nil
}

// getMatrixDraftRefRow loads the row by its primary key inside one tenant.
func (s *Store) getMatrixDraftRefRow(tenantID, id string) (MatrixDraftRef, error) {
	row := s.DB.QueryRow(`SELECT `+matrixDraftRefColumns+`
		FROM matrix_draft_refs WHERE tenant_id=? AND id=?`, tenantID, id)
	return scanMatrixDraftRef(row)
}

// sourceEventFingerprint binds the fact fields a repeat must repeat exactly.
func sourceEventFingerprint(facts MatrixDraftSourceFacts) string {
	metric := func(p *int) string {
		if p == nil {
			return "-"
		}
		return strconv.Itoa(*p)
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		facts.Kind, facts.MatrixPlanID, facts.MatrixItemID, facts.ExternalPostID,
		metric(facts.Reach), metric(facts.Likes), metric(facts.Leads),
	}, "\x1f")))
	return "fp_" + hex.EncodeToString(sum[:])
}
