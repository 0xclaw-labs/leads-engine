package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/bianjiefilm/leads-engine/server/internal/lightcopy"
)

// ErrLightNotCampaign means the lead is not a touch-campaign scene.
var ErrLightNotCampaign = errors.New("lightcopy: not a campaign")

// ErrLightRevisionConflict means the caller's expected_revision does not match
// the stored draft content_version.
var ErrLightRevisionConflict = errors.New("lightcopy: revision conflict")

// ErrLightDraftModifiedByHuman means an async/late model generation tried to
// overwrite a draft that was already modified by a human or advanced in version.
var ErrLightDraftModifiedByHuman = errors.New("lightcopy: draft modified by human")

// ErrLightHandoffConflict means an idempotency_key was replayed with mismatched
// handoff parameters (tool/source/payer).
var ErrLightHandoffConflict = errors.New("lightcopy: handoff idempotency conflict")

// LightDraft is one manual or model-generated reply, email, or marketing brief.
type LightDraft struct {
	ID                 string         `json:"id"`
	TenantID           string         `json:"tenant_id"`
	SubjectKind        string         `json:"subject_kind"`
	SubjectID          string         `json:"subject_id"`
	Kind               string         `json:"kind"`
	Body               string         `json:"body"`
	Origin             string         `json:"origin"`
	ContentVersion     int            `json:"content_version"`
	Generated          bool           `json:"generated"`
	Sent               bool           `json:"sent"`
	Published          bool           `json:"published"`
	MarketingPermitted bool           `json:"marketing_permitted"`
	UserConfirmed      bool           `json:"user_confirmed"`
	LiveCharge         int            `json:"live_charge"`
	InputFacts         map[string]any `json:"input_facts,omitempty"`
	InputFactsHash     string         `json:"input_facts_hash,omitempty"`
	UnknownFields      []string       `json:"unknown_fields,omitempty"`
	TaskID             string         `json:"task_id,omitempty"`
	BillingVerdict     string         `json:"billing_verdict,omitempty"`
	UsageRef           string         `json:"usage_ref,omitempty"`
	SampleCategory     string         `json:"sample_category,omitempty"`
	UpdatedBy          string         `json:"updated_by"`
	CreatedAt          string         `json:"created_at"`
	UpdatedAt          string         `json:"updated_at"`
}

// LightDraftRevision is one immutable audit snapshot of a draft revision.
type LightDraftRevision struct {
	ID                 string         `json:"id"`
	TenantID           string         `json:"tenant_id"`
	DraftID            string         `json:"draft_id"`
	SubjectKind        string         `json:"subject_kind"`
	SubjectID          string         `json:"subject_id"`
	Kind               string         `json:"kind"`
	ContentVersion     int            `json:"content_version"`
	Origin             string         `json:"origin"`
	Body               string         `json:"body"`
	InputFacts         map[string]any `json:"input_facts,omitempty"`
	InputFactsHash     string         `json:"input_facts_hash,omitempty"`
	UnknownFields      []string       `json:"unknown_fields,omitempty"`
	TaskID             string         `json:"task_id,omitempty"`
	BillingVerdict     string         `json:"billing_verdict,omitempty"`
	UsageRef           string         `json:"usage_ref,omitempty"`
	SampleCategory     string         `json:"sample_category,omitempty"`
	LiveCharge         int            `json:"live_charge"`
	UserConfirmed      bool           `json:"user_confirmed"`
	Sent               bool           `json:"sent"`
	Published          bool           `json:"published"`
	MarketingPermitted bool           `json:"marketing_permitted"`
	UpdatedBy          string         `json:"updated_by"`
	CreatedAt          string         `json:"created_at"`
}

// LightHandoff is one attempt to open an existing professional tool.
type LightHandoff struct {
	ID                 string                      `json:"id"`
	TenantID           string                      `json:"tenant_id"`
	Key                string                      `json:"idempotency_key"`
	DraftID            string                      `json:"draft_id"`
	Tool               string                      `json:"tool"`
	SourceKind         string                      `json:"source_kind"`
	SourceID           string                      `json:"source_id"`
	ContentVersion     int                         `json:"content_version"`
	Facts              map[string]any              `json:"facts"`
	AuthorizedAssets   []lightcopy.AuthorizedAsset `json:"authorized_assets,omitempty"`
	PermissionRef      string                      `json:"permission_ref,omitempty"`
	PayerRef           string                      `json:"payer_ref,omitempty"`
	QuoteExpiresAt     string                      `json:"quote_expires_at,omitempty"`
	TargetProjectRef   string                      `json:"target_project_ref,omitempty"`
	ExternalTaskRef    string                      `json:"external_task_ref,omitempty"`
	RequestFingerprint string                      `json:"request_fingerprint,omitempty"`
	Status             string                      `json:"status"`
	Reason             string                      `json:"reason"`
	ProjectCreated     bool                        `json:"project_created"`
	Regenerated        bool                        `json:"regenerated"`
	RevokedAt          string                      `json:"revoked_at,omitempty"`
	CreatedAt          string                      `json:"created_at"`
	UpdatedAt          string                      `json:"updated_at,omitempty"`
}

// LightHandoffReturn is one controlled candidate/asset/task reference returned
// from a professional production tool back to the originating CRM record.
type LightHandoffReturn struct {
	ID                 string `json:"id"`
	TenantID           string `json:"tenant_id"`
	HandoffID          string `json:"handoff_id"`
	Key                string `json:"idempotency_key"`
	SourceKind         string `json:"source_kind"`
	SourceID           string `json:"source_id"`
	Tool               string `json:"tool"`
	AssetRef           string `json:"asset_ref,omitempty"`
	TaskRef            string `json:"task_ref,omitempty"`
	CandidateKind      string `json:"candidate_kind"`
	Summary            string `json:"summary,omitempty"`
	ContentVersion     int    `json:"content_version"`
	PermissionRef      string `json:"permission_ref,omitempty"`
	PayerRef           string `json:"payer_ref,omitempty"`
	Status             string `json:"status"`
	Sent               bool   `json:"sent"`
	Published          bool   `json:"published"`
	MarketingPermitted bool   `json:"marketing_permitted"`
	CreatedBy          string `json:"created_by"`
	CreatedAt          string `json:"created_at"`
}

// LoadLightOpportunity copies only the title. Amount is not a proven price,
// and the contact row is not loaded.
func (s *Store) LoadLightOpportunity(id, tenantID string) (lightcopy.Subject, string, error) {
	opp, err := s.GetOpportunity(id, tenantID)
	if err != nil {
		return lightcopy.Subject{}, "", err
	}
	return lightcopy.Subject{
		Kind: lightcopy.KindOpportunity, ID: opp.ID, TenantID: opp.TenantID, Title: opp.Title,
	}, opp.AssignedMemberID, nil
}

// LoadLightCampaign copies the activity reference from a touch-campaign lead.
// Phone, name, email and the authorization snapshot are not selected.
func (s *Store) LoadLightCampaign(id, tenantID string) (lightcopy.Subject, string, error) {
	var assignee, sourceType, activity string
	err := s.DB.QueryRow(`
		SELECT COALESCE(l.assigned_member_id,''), c.source_type, COALESCE(sr.source_ref,'')
		FROM leads l
		JOIN contacts c ON c.id = l.contact_id AND c.tenant_id = l.tenant_id AND c.deleted_at IS NULL
		LEFT JOIN source_refs sr ON sr.id = l.source_ref_id AND sr.tenant_id = l.tenant_id
		WHERE l.id=? AND l.tenant_id=?`, id, tenantID).Scan(&assignee, &sourceType, &activity)
	if err != nil {
		return lightcopy.Subject{}, "", err
	}
	if sourceType != "touch_campaign" {
		return lightcopy.Subject{}, "", ErrLightNotCampaign
	}
	return lightcopy.Subject{
		Kind: lightcopy.KindCampaign, ID: id, TenantID: tenantID, ActivityRef: activity,
	}, assignee, nil
}

const lightDraftSelectCols = `
	id, tenant_id, subject_kind, subject_id, kind, body, origin, content_version,
	generated, sent, published, marketing_permitted, user_confirmed, live_charge,
	input_facts_json, input_facts_hash, unknown_fields_json, task_id, billing_verdict,
	usage_ref, sample_category, updated_by, created_at, updated_at`

func (s *Store) ListLightDrafts(tenantID, subjectKind, subjectID string) ([]LightDraft, error) {
	rows, err := s.DB.Query(`
		SELECT `+lightDraftSelectCols+`
		FROM light_copy_drafts
		WHERE tenant_id=? AND subject_kind=? AND subject_id=?
		ORDER BY kind`, tenantID, subjectKind, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LightDraft
	for rows.Next() {
		d, err := scanLightDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetLightDraft(tenantID, subjectKind, subjectID, kind string) (LightDraft, error) {
	row := s.DB.QueryRow(`
		SELECT `+lightDraftSelectCols+`
		FROM light_copy_drafts
		WHERE tenant_id=? AND subject_kind=? AND subject_id=? AND kind=?`,
		tenantID, subjectKind, subjectID, kind)
	return scanLightDraft(row)
}

func scanLightDraft(sc interface{ Scan(...any) error }) (LightDraft, error) {
	var d LightDraft
	var generated, sent, published, permitted, confirmed, charge int
	var inputFactsRaw, unknownRaw string
	err := sc.Scan(&d.ID, &d.TenantID, &d.SubjectKind, &d.SubjectID, &d.Kind, &d.Body, &d.Origin,
		&d.ContentVersion, &generated, &sent, &published, &permitted, &confirmed, &charge,
		&inputFactsRaw, &d.InputFactsHash, &unknownRaw, &d.TaskID, &d.BillingVerdict,
		&d.UsageRef, &d.SampleCategory, &d.UpdatedBy, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return LightDraft{}, err
	}
	d.Generated = generated != 0
	d.Sent = sent != 0
	d.Published = published != 0
	d.MarketingPermitted = permitted != 0
	d.UserConfirmed = confirmed != 0
	d.LiveCharge = charge
	if inputFactsRaw != "" && inputFactsRaw != "{}" {
		var m map[string]any
		if json.Unmarshal([]byte(inputFactsRaw), &m) == nil && len(m) > 0 {
			d.InputFacts = m
		}
	}
	if unknownRaw != "" && unknownRaw != "[]" {
		var u []string
		if json.Unmarshal([]byte(unknownRaw), &u) == nil && len(u) > 0 {
			d.UnknownFields = u
		}
	}
	return d, nil
}

// SaveLightDraft upserts the draft text without an expected_revision guard.
// Flags that would mean sent, published, or granted marketing consent cannot be stored.
func (s *Store) SaveLightDraft(d LightDraft) (LightDraft, error) {
	return s.SaveLightDraftCAS(d, nil, false)
}

// SaveLightDraftCAS atomically saves a draft with optional optimistic-concurrency
// revision checks and human-edit overwrite protection. Every saved revision is
// also recorded in light_copy_draft_revisions for full provenance and billing audit.
func (s *Store) SaveLightDraftCAS(d LightDraft, expectedRevision *int, protectManual bool) (LightDraft, error) {
	ts := now()
	if d.ID == "" {
		d.ID = newID("lcd_")
	}
	if d.CreatedAt == "" {
		d.CreatedAt = ts
	}
	d.UpdatedAt = ts

	confirmed := 0
	generated := 0
	if d.UserConfirmed {
		confirmed = 1
	}
	if d.Generated {
		generated = 1
	}

	factsJSON := "{}"
	if len(d.InputFacts) > 0 {
		if b, err := json.Marshal(d.InputFacts); err == nil {
			factsJSON = string(b)
		}
	}
	unknownJSON := "[]"
	if len(d.UnknownFields) > 0 {
		if b, err := json.Marshal(d.UnknownFields); err == nil {
			unknownJSON = string(b)
		}
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return LightDraft{}, err
	}
	defer tx.Rollback()

	var (
		curID      string
		curVer     int
		curOrigin  string
		curCreated string
	)
	err = tx.QueryRow(`
		SELECT id, content_version, origin, created_at
		FROM light_copy_drafts
		WHERE tenant_id=? AND subject_kind=? AND subject_id=? AND kind=?`,
		d.TenantID, d.SubjectKind, d.SubjectID, d.Kind).Scan(&curID, &curVer, &curOrigin, &curCreated)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		if expectedRevision != nil && *expectedRevision != 0 {
			if protectManual {
				return LightDraft{}, ErrLightDraftModifiedByHuman
			}
			return LightDraft{}, ErrLightRevisionConflict
		}
		_, err = tx.Exec(`
			INSERT INTO light_copy_drafts(
				id, tenant_id, subject_kind, subject_id, kind, body, origin, content_version,
				generated, sent, published, marketing_permitted, user_confirmed, live_charge,
				input_facts_json, input_facts_hash, unknown_fields_json, task_id, billing_verdict,
				usage_ref, sample_category, updated_by, created_at, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,0,0,0,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.ID, d.TenantID, d.SubjectKind, d.SubjectID, d.Kind, d.Body, d.Origin, d.ContentVersion,
			generated, confirmed, d.LiveCharge,
			factsJSON, d.InputFactsHash, unknownJSON, d.TaskID, d.BillingVerdict,
			d.UsageRef, d.SampleCategory, d.UpdatedBy, d.CreatedAt, d.UpdatedAt)
		if err != nil {
			return LightDraft{}, err
		}
	case err != nil:
		return LightDraft{}, err
	default:
		if expectedRevision != nil && curVer != *expectedRevision {
			if protectManual {
				return LightDraft{}, ErrLightDraftModifiedByHuman
			}
			return LightDraft{}, ErrLightRevisionConflict
		}
		d.ID = curID
		d.CreatedAt = curCreated
		res, err := tx.Exec(`
			UPDATE light_copy_drafts SET
				body=?,
				origin=?,
				content_version=?,
				generated=?,
				live_charge=?,
				user_confirmed=?,
				input_facts_json=?,
				input_facts_hash=?,
				unknown_fields_json=?,
				task_id=?,
				billing_verdict=?,
				usage_ref=?,
				sample_category=?,
				updated_by=?,
				updated_at=?
			WHERE tenant_id=? AND subject_kind=? AND subject_id=? AND kind=? AND content_version=?`,
			d.Body, d.Origin, d.ContentVersion, generated, d.LiveCharge, confirmed,
			factsJSON, d.InputFactsHash, unknownJSON, d.TaskID, d.BillingVerdict,
			d.UsageRef, d.SampleCategory, d.UpdatedBy, d.UpdatedAt,
			d.TenantID, d.SubjectKind, d.SubjectID, d.Kind, curVer)
		if err != nil {
			return LightDraft{}, err
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			if protectManual {
				return LightDraft{}, ErrLightDraftModifiedByHuman
			}
			return LightDraft{}, ErrLightRevisionConflict
		}
	}

	// Upsert revision audit trail: confirm updates user_confirmed on the same
	// content_version, while new versions append distinct rows.
	revID := newID("lcr_")
	_, err = tx.Exec(`
		INSERT INTO light_copy_draft_revisions(
			id, tenant_id, draft_id, subject_kind, subject_id, kind, content_version,
			origin, body, input_facts_json, input_facts_hash, unknown_fields_json,
			task_id, billing_verdict, usage_ref, sample_category, live_charge,
			user_confirmed, sent, published, marketing_permitted, updated_by, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,0,?,?)
		ON CONFLICT(tenant_id, subject_kind, subject_id, kind, content_version) DO UPDATE SET
			user_confirmed=excluded.user_confirmed,
			updated_by=excluded.updated_by`,
		revID, d.TenantID, d.ID, d.SubjectKind, d.SubjectID, d.Kind, d.ContentVersion,
		d.Origin, d.Body, factsJSON, d.InputFactsHash, unknownJSON,
		d.TaskID, d.BillingVerdict, d.UsageRef, d.SampleCategory, d.LiveCharge,
		confirmed, d.UpdatedBy, ts)
	if err != nil {
		return LightDraft{}, err
	}

	if err := tx.Commit(); err != nil {
		return LightDraft{}, err
	}
	return s.GetLightDraft(d.TenantID, d.SubjectKind, d.SubjectID, d.Kind)
}

func (s *Store) ListLightDraftRevisions(tenantID, subjectKind, subjectID, kind string) ([]LightDraftRevision, error) {
	rows, err := s.DB.Query(`
		SELECT id, tenant_id, draft_id, subject_kind, subject_id, kind, content_version,
		       origin, body, input_facts_json, input_facts_hash, unknown_fields_json,
		       task_id, billing_verdict, usage_ref, sample_category, live_charge,
		       user_confirmed, sent, published, marketing_permitted, updated_by, created_at
		FROM light_copy_draft_revisions
		WHERE tenant_id=? AND subject_kind=? AND subject_id=? AND kind=?
		ORDER BY content_version ASC`, tenantID, subjectKind, subjectID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LightDraftRevision
	for rows.Next() {
		var r LightDraftRevision
		var confirmed, sent, published, permitted int
		var inputFactsRaw, unknownRaw string
		if err := rows.Scan(&r.ID, &r.TenantID, &r.DraftID, &r.SubjectKind, &r.SubjectID, &r.Kind,
			&r.ContentVersion, &r.Origin, &r.Body, &inputFactsRaw, &r.InputFactsHash, &unknownRaw,
			&r.TaskID, &r.BillingVerdict, &r.UsageRef, &r.SampleCategory, &r.LiveCharge,
			&confirmed, &sent, &published, &permitted, &r.UpdatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.UserConfirmed = confirmed != 0
		r.Sent = sent != 0
		r.Published = published != 0
		r.MarketingPermitted = permitted != 0
		if inputFactsRaw != "" && inputFactsRaw != "{}" {
			var m map[string]any
			if json.Unmarshal([]byte(inputFactsRaw), &m) == nil && len(m) > 0 {
				r.InputFacts = m
			}
		}
		if unknownRaw != "" && unknownRaw != "[]" {
			var u []string
			if json.Unmarshal([]byte(unknownRaw), &u) == nil && len(u) > 0 {
				r.UnknownFields = u
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const lightHandoffSelectCols = `
	id, tenant_id, idempotency_key, draft_id, tool, source_kind, source_id,
	content_version, fact_json, authorized_assets_json, permission_ref, payer_ref,
	quote_expires_at, target_project_ref, external_task_ref, request_fingerprint,
	status, reason, project_created, regenerated, revoked_at, created_at, updated_at`

func scanLightHandoff(sc interface{ Scan(...any) error }) (LightHandoff, error) {
	var h LightHandoff
	var facts, assetsRaw string
	var created, regenerated int
	err := sc.Scan(
		&h.ID, &h.TenantID, &h.Key, &h.DraftID, &h.Tool, &h.SourceKind, &h.SourceID,
		&h.ContentVersion, &facts, &assetsRaw, &h.PermissionRef, &h.PayerRef,
		&h.QuoteExpiresAt, &h.TargetProjectRef, &h.ExternalTaskRef, &h.RequestFingerprint,
		&h.Status, &h.Reason, &created, &regenerated, &h.RevokedAt, &h.CreatedAt, &h.UpdatedAt,
	)
	if err != nil {
		return LightHandoff{}, err
	}
	h.ProjectCreated = created != 0
	h.Regenerated = regenerated != 0
	h.Facts = map[string]any{}
	if facts != "" {
		_ = json.Unmarshal([]byte(facts), &h.Facts)
	}
	if assetsRaw != "" && assetsRaw != "[]" {
		var assets []lightcopy.AuthorizedAsset
		if json.Unmarshal([]byte(assetsRaw), &assets) == nil && len(assets) > 0 {
			h.AuthorizedAssets = assets
		}
	}
	return h, nil
}

func (s *Store) GetLightHandoff(tenantID, key string) (LightHandoff, error) {
	row := s.DB.QueryRow(`
		SELECT `+lightHandoffSelectCols+`
		FROM light_copy_handoffs WHERE tenant_id=? AND idempotency_key=?`, tenantID, key)
	return scanLightHandoff(row)
}

func (s *Store) GetLightHandoffByID(tenantID, id string) (LightHandoff, error) {
	row := s.DB.QueryRow(`
		SELECT `+lightHandoffSelectCols+`
		FROM light_copy_handoffs WHERE tenant_id=? AND id=?`, tenantID, id)
	return scanLightHandoff(row)
}

func (s *Store) ListLightHandoffs(tenantID, sourceKind, sourceID string) ([]LightHandoff, error) {
	rows, err := s.DB.Query(`
		SELECT `+lightHandoffSelectCols+`
		FROM light_copy_handoffs
		WHERE tenant_id=? AND source_kind=? AND source_id=?
		ORDER BY created_at DESC`, tenantID, sourceKind, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LightHandoff
	for rows.Next() {
		h, err := scanLightHandoff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// InsertLightHandoff stores the first attempt for a key. A duplicate key
// returns the original row (or ErrLightHandoffConflict when fingerprint differs).
func (s *Store) InsertLightHandoff(h LightHandoff) (LightHandoff, error) {
	if existing, err := s.GetLightHandoff(h.TenantID, h.Key); err == nil {
		if h.RequestFingerprint != "" && existing.RequestFingerprint != "" && existing.RequestFingerprint != h.RequestFingerprint {
			return LightHandoff{}, ErrLightHandoffConflict
		}
		existing.Regenerated = false
		existing.ProjectCreated = false
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return LightHandoff{}, err
	}
	ts := now()
	if h.ID == "" {
		h.ID = newID("lch_")
	}
	if h.CreatedAt == "" {
		h.CreatedAt = ts
	}
	if h.UpdatedAt == "" {
		h.UpdatedAt = ts
	}
	if h.Facts == nil {
		h.Facts = map[string]any{}
	}
	rawFacts, err := json.Marshal(h.Facts)
	if err != nil {
		return LightHandoff{}, err
	}
	rawAssets := "[]"
	if len(h.AuthorizedAssets) > 0 {
		b, err := json.Marshal(h.AuthorizedAssets)
		if err != nil {
			return LightHandoff{}, err
		}
		rawAssets = string(b)
	}
	_, err = s.DB.Exec(`
		INSERT INTO light_copy_handoffs(
			id, tenant_id, idempotency_key, draft_id, tool, source_kind, source_id,
			content_version, fact_json, authorized_assets_json, permission_ref, payer_ref,
			quote_expires_at, target_project_ref, external_task_ref, request_fingerprint,
			status, reason, project_created, regenerated, revoked_at, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,?,?,?)`,
		h.ID, h.TenantID, h.Key, h.DraftID, h.Tool, h.SourceKind, h.SourceID,
		h.ContentVersion, string(rawFacts), rawAssets, h.PermissionRef, h.PayerRef,
		h.QuoteExpiresAt, h.TargetProjectRef, h.ExternalTaskRef, h.RequestFingerprint,
		h.Status, h.Reason, h.RevokedAt, h.CreatedAt, h.UpdatedAt)
	if err != nil {
		if existing, getErr := s.GetLightHandoff(h.TenantID, h.Key); getErr == nil {
			if h.RequestFingerprint != "" && existing.RequestFingerprint != "" && existing.RequestFingerprint != h.RequestFingerprint {
				return LightHandoff{}, ErrLightHandoffConflict
			}
			existing.Regenerated = false
			existing.ProjectCreated = false
			return existing, nil
		}
		return LightHandoff{}, err
	}
	return s.GetLightHandoff(h.TenantID, h.Key)
}

// UpdateLightHandoffRecovery transitions an unknown handoff to its recovered
// status without ever creating a duplicate row or mutating project_created.
func (s *Store) UpdateLightHandoffRecovery(tenantID, id, status, reason, targetProjectRef, externalTaskRef string) (LightHandoff, error) {
	ts := now()
	res, err := s.DB.Exec(`
		UPDATE light_copy_handoffs
		SET status=?, reason=?,
		    target_project_ref=CASE WHEN ?<>'' THEN ? ELSE target_project_ref END,
		    external_task_ref=CASE WHEN ?<>'' THEN ? ELSE external_task_ref END,
		    updated_at=?
		WHERE tenant_id=? AND id=? AND revoked_at=''`,
		status, reason, targetProjectRef, targetProjectRef, externalTaskRef, externalTaskRef, ts, tenantID, id)
	if err != nil {
		return LightHandoff{}, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return LightHandoff{}, sql.ErrNoRows
	}
	return s.GetLightHandoffByID(tenantID, id)
}

// RevokeLightHandoff marks one handoff revoked fail-closed so subsequent
// backflows and recoveries are rejected.
func (s *Store) RevokeLightHandoff(tenantID, id, reason string) (LightHandoff, error) {
	ts := now()
	if reason == "" {
		reason = lightcopy.ReasonPermissionRevoked
	}
	res, err := s.DB.Exec(`
		UPDATE light_copy_handoffs
		SET status='revoked', reason=?, revoked_at=CASE WHEN revoked_at='' THEN ? ELSE revoked_at END, updated_at=?
		WHERE tenant_id=? AND id=?`,
		reason, ts, ts, tenantID, id)
	if err != nil {
		return LightHandoff{}, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return LightHandoff{}, sql.ErrNoRows
	}
	return s.GetLightHandoffByID(tenantID, id)
}

// RevokeLightSubject revokes professional handoff / backflow permission for a
// CRM subject without deleting saved drafts.
func (s *Store) RevokeLightSubject(tenantID, subjectKind, subjectID, revokedBy, reason string) error {
	ts := now()
	_, err := s.DB.Exec(`
		INSERT INTO light_copy_subject_revocations(tenant_id, subject_kind, subject_id, revoked_by, reason, revoked_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(tenant_id, subject_kind, subject_id) DO UPDATE SET
			revoked_by=excluded.revoked_by,
			reason=excluded.reason,
			revoked_at=excluded.revoked_at`,
		tenantID, subjectKind, subjectID, revokedBy, reason, ts)
	return err
}

// IsLightSubjectRevoked reports whether the subject has revoked handoff permission.
func (s *Store) IsLightSubjectRevoked(tenantID, subjectKind, subjectID string) (bool, error) {
	var count int
	err := s.DB.QueryRow(`
		SELECT COUNT(*) FROM light_copy_subject_revocations
		WHERE tenant_id=? AND subject_kind=? AND subject_id=?`,
		tenantID, subjectKind, subjectID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// InsertLightHandoffReturn stores a controlled asset/task backflow candidate.
// Same (tenant_id, handoff_id, idempotency_key) is idempotent.
func (s *Store) InsertLightHandoffReturn(ret LightHandoffReturn) (LightHandoffReturn, bool, error) {
	if existing, err := s.getLightHandoffReturn(ret.TenantID, ret.HandoffID, ret.Key); err == nil {
		return existing, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return LightHandoffReturn{}, false, err
	}
	if ret.ID == "" {
		ret.ID = newID("lcb_")
	}
	if ret.CreatedAt == "" {
		ret.CreatedAt = now()
	}
	if ret.Status == "" {
		ret.Status = "candidate"
	}
	_, err := s.DB.Exec(`
		INSERT INTO light_copy_handoff_returns(
			id, tenant_id, handoff_id, idempotency_key, source_kind, source_id, tool,
			asset_ref, task_ref, candidate_kind, summary, content_version,
			permission_ref, payer_ref, status, sent, published, marketing_permitted,
			created_by, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,0,?,?)`,
		ret.ID, ret.TenantID, ret.HandoffID, ret.Key, ret.SourceKind, ret.SourceID, ret.Tool,
		ret.AssetRef, ret.TaskRef, ret.CandidateKind, ret.Summary, ret.ContentVersion,
		ret.PermissionRef, ret.PayerRef, ret.Status, ret.CreatedBy, ret.CreatedAt)
	if err != nil {
		if existing, getErr := s.getLightHandoffReturn(ret.TenantID, ret.HandoffID, ret.Key); getErr == nil {
			return existing, true, nil
		}
		return LightHandoffReturn{}, false, err
	}
	stored, err := s.getLightHandoffReturn(ret.TenantID, ret.HandoffID, ret.Key)
	return stored, false, err
}

func (s *Store) getLightHandoffReturn(tenantID, handoffID, key string) (LightHandoffReturn, error) {
	var r LightHandoffReturn
	var sent, published, permitted int
	err := s.DB.QueryRow(`
		SELECT id, tenant_id, handoff_id, idempotency_key, source_kind, source_id, tool,
		       asset_ref, task_ref, candidate_kind, summary, content_version,
		       permission_ref, payer_ref, status, sent, published, marketing_permitted,
		       created_by, created_at
		FROM light_copy_handoff_returns
		WHERE tenant_id=? AND handoff_id=? AND idempotency_key=?`,
		tenantID, handoffID, key).Scan(
		&r.ID, &r.TenantID, &r.HandoffID, &r.Key, &r.SourceKind, &r.SourceID, &r.Tool,
		&r.AssetRef, &r.TaskRef, &r.CandidateKind, &r.Summary, &r.ContentVersion,
		&r.PermissionRef, &r.PayerRef, &r.Status, &sent, &published, &permitted,
		&r.CreatedBy, &r.CreatedAt)
	if err != nil {
		return LightHandoffReturn{}, err
	}
	r.Sent = sent != 0
	r.Published = published != 0
	r.MarketingPermitted = permitted != 0
	return r, nil
}

func (s *Store) ListLightHandoffReturns(tenantID, handoffID string) ([]LightHandoffReturn, error) {
	rows, err := s.DB.Query(`
		SELECT id, tenant_id, handoff_id, idempotency_key, source_kind, source_id, tool,
		       asset_ref, task_ref, candidate_kind, summary, content_version,
		       permission_ref, payer_ref, status, sent, published, marketing_permitted,
		       created_by, created_at
		FROM light_copy_handoff_returns
		WHERE tenant_id=? AND handoff_id=?
		ORDER BY created_at DESC`, tenantID, handoffID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LightHandoffReturn
	for rows.Next() {
		var r LightHandoffReturn
		var sent, published, permitted int
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.HandoffID, &r.Key, &r.SourceKind, &r.SourceID, &r.Tool,
			&r.AssetRef, &r.TaskRef, &r.CandidateKind, &r.Summary, &r.ContentVersion,
			&r.PermissionRef, &r.PayerRef, &r.Status, &sent, &published, &permitted,
			&r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Sent = sent != 0
		r.Published = published != 0
		r.MarketingPermitted = permitted != 0
		out = append(out, r)
	}
	return out, rows.Err()
}
