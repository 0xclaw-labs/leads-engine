package store

import (
	"database/sql"
	"errors"
)

// ErrCampaignMotionIdempotencyConflict is returned when an idempotency key is
// reused with a different request fingerprint.
var ErrCampaignMotionIdempotencyConflict = errors.New("store: campaign motion idempotency conflict")

// CampaignMotion is the stored leads-side handoff.
// ExportJSON is the only document that may leave leads.
// LocalConversionJSON is not part of that document.
type CampaignMotion struct {
	ID                    string
	TenantID              string
	CampaignID            string
	ExportJSON            string
	LocalConversionJSON   string
	ModelCalls            int
	CreativeProjectWrites int
	PieceGenerated        bool
	PieceSent             bool
	UpdatedBy             string
	CreatedAt             string
	UpdatedAt             string
}

// CampaignMotionQueueState is the MX-OPS queue / night / idle waiting projection
// for one campaign handoff. ETASeconds is nullable (nil means unknown, never 0).
type CampaignMotionQueueState struct {
	ID                        string
	TenantID                  string
	CampaignID                string
	HandoffID                 string
	JobID                     string
	CreativeID                string
	ProjectID                 string
	RevisionID                string
	Digest                    string
	ExecutionPreference       string
	PolicyVersion             string
	Phase                     string
	QueueState                string
	WaitReason                string
	NextWindow                string
	ETASeconds                *int64
	RetainedAssetVersionsJSON string
	LastIdempotencyKey        string
	AutoOutreachTriggered     bool
	AutoPublished             bool
	AutoConversionRecorded    bool
	UpdatedBy                 string
	CreatedAt                 string
	UpdatedAt                 string
}

// CampaignMotionRender is one returned candidate render attached to the single
// campaign handoff project with full attribution keys and cost counters.
type CampaignMotionRender struct {
	ID                     string
	TenantID               string
	CampaignID             string
	HandoffID              string
	IdempotencyKey         string
	RequestFingerprint     string
	CreativeID             string
	ProjectID              string
	RevisionID             string
	Digest                 string
	RenderID               string
	VariantID              string
	Channel                string
	SourceFactsVersion     string
	RenderMode             string
	HighCostModelCalls     int
	BilledGenerationCount  int
	CostCents              int64
	BillingReceiptID       string
	PayerRef               string
	BudgetAttributionID    string
	Status                 string
	AutoOutreachTriggered  bool
	AutoPublished          bool
	AutoConversionRecorded bool
	CreatedBy              string
	CreatedAt              string
	UpdatedAt              string
}

// CampaignMotionRepeatMethod is a persisted, boundary-checked Repeat Method recipe.
type CampaignMotionRepeatMethod struct {
	ID                  string
	TenantID            string
	CampaignID          string
	HandoffID           string
	Mode                string
	Name                string
	TemplateID          string
	TemplateVisibility  string
	TeamGrantID         string
	SourceProjectID     string
	SourceRevisionID    string
	SourceFactsVersion  string
	ParametricSlotsJSON string
	PublicTemplate      bool
	CreatedBy           string
	CreatedAt           string
	UpdatedAt           string
}

// CampaignMotionRevocation marks a campaign handoff as revoked (fail-closed).
type CampaignMotionRevocation struct {
	TenantID   string
	CampaignID string
	RevokedBy  string
	Reason     string
	RevokedAt  string
}

// LoadCampaignMotionSubject returns the assignee of a touch-campaign lead.
// Phone, email, and the contact name are not selected.
func (s *Store) LoadCampaignMotionSubject(id, tenantID string) (string, error) {
	var assignee string
	err := s.DB.QueryRow(`
		SELECT COALESCE(l.assigned_member_id,'')
		FROM leads l
		JOIN contacts c ON c.id = l.contact_id AND c.tenant_id = l.tenant_id AND c.deleted_at IS NULL
		WHERE l.id=? AND l.tenant_id=? AND c.source_type='touch_campaign'`, id, tenantID).Scan(&assignee)
	return assignee, err
}

// HasPendingCampaignFollowUp reports whether the campaign lead has at least one
// open (uncompleted) sales follow-up item. GPU queueing never blocks these rows.
func (s *Store) HasPendingCampaignFollowUp(tenantID, campaignID string) (bool, error) {
	var exists int
	err := s.DB.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM follow_ups
			WHERE tenant_id=? AND lead_id=? AND completed_at IS NULL
		)`, tenantID, campaignID).Scan(&exists)
	return exists != 0, err
}

func (s *Store) GetCampaignMotion(tenantID, campaignID string) (CampaignMotion, error) {
	row := s.DB.QueryRow(`
		SELECT id, tenant_id, campaign_id, export_json, local_conversion_json,
		       model_calls, creative_project_writes, piece_generated, piece_sent,
		       updated_by, created_at, updated_at
		FROM campaign_motion_handoffs
		WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID)
	return scanCampaignMotion(row)
}

// SaveCampaignMotion upserts one handoff. Generation and creative-project writes cannot be stored.
func (s *Store) SaveCampaignMotion(row CampaignMotion) (CampaignMotion, error) {
	if row.ID == "" {
		row.ID = newID("cmh_")
	}
	if row.CreatedAt == "" {
		row.CreatedAt = now()
	}
	row.UpdatedAt = now()
	_, err := s.DB.Exec(`
		INSERT INTO campaign_motion_handoffs(
			id, tenant_id, campaign_id, export_json, local_conversion_json,
			model_calls, creative_project_writes, piece_generated, piece_sent,
			updated_by, created_at, updated_at)
		VALUES(?,?,?,?,?,0,0,0,0,?,?,?)
		ON CONFLICT(tenant_id, campaign_id) DO UPDATE SET
			export_json=excluded.export_json,
			local_conversion_json=excluded.local_conversion_json,
			updated_by=excluded.updated_by,
			updated_at=excluded.updated_at`,
		row.ID, row.TenantID, row.CampaignID, row.ExportJSON, row.LocalConversionJSON,
		row.UpdatedBy, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return CampaignMotion{}, err
	}
	return s.GetCampaignMotion(row.TenantID, row.CampaignID)
}

// UpdateCampaignMotionLocalConversion updates ONLY local_conversion_json on the
// Leads side. export_json and creative_project_writes are untouched.
func (s *Store) UpdateCampaignMotionLocalConversion(tenantID, campaignID, localConversionJSON, updatedBy string) (CampaignMotion, error) {
	res, err := s.DB.Exec(`
		UPDATE campaign_motion_handoffs
		SET local_conversion_json=?, updated_by=?, updated_at=?
		WHERE tenant_id=? AND campaign_id=?`,
		localConversionJSON, updatedBy, now(), tenantID, campaignID)
	if err != nil {
		return CampaignMotion{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return CampaignMotion{}, err
	}
	if affected == 0 {
		return CampaignMotion{}, sql.ErrNoRows
	}
	return s.GetCampaignMotion(tenantID, campaignID)
}

func scanCampaignMotion(sc interface{ Scan(...any) error }) (CampaignMotion, error) {
	var row CampaignMotion
	var generated, sent int
	err := sc.Scan(&row.ID, &row.TenantID, &row.CampaignID, &row.ExportJSON, &row.LocalConversionJSON,
		&row.ModelCalls, &row.CreativeProjectWrites, &generated, &sent,
		&row.UpdatedBy, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return CampaignMotion{}, err
	}
	row.PieceGenerated = generated != 0
	row.PieceSent = sent != 0
	return row, nil
}

// GetCampaignMotionQueueState returns the latest queue projection for one campaign.
func (s *Store) GetCampaignMotionQueueState(tenantID, campaignID string) (CampaignMotionQueueState, error) {
	row := s.DB.QueryRow(`
		SELECT id, tenant_id, campaign_id, handoff_id, job_id, creative_id,
		       project_id, revision_id, digest, execution_preference, policy_version,
		       phase, queue_state, wait_reason, next_window, eta_seconds,
		       retained_asset_versions_json, last_idempotency_key,
		       auto_outreach_triggered, auto_published, auto_conversion_recorded,
		       updated_by, created_at, updated_at
		FROM campaign_motion_queue_states
		WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID)
	return scanCampaignMotionQueueState(row)
}

// UpsertCampaignMotionQueueBackflow records an idempotent MX-OPS queue backflow
// in a single transaction, updating the same creative/job projection without
// creating a second project or triggering outreach/publish/conversion.
// Returns (state, replayed, error).
func (s *Store) UpsertCampaignMotionQueueBackflow(row CampaignMotionQueueState, fingerprint string) (CampaignMotionQueueState, bool, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return CampaignMotionQueueState{}, false, err
	}
	defer tx.Rollback()

	var existingFP string
	err = tx.QueryRow(`
		SELECT request_fingerprint FROM campaign_motion_queue_events
		WHERE tenant_id=? AND campaign_id=? AND idempotency_key=?`,
		row.TenantID, row.CampaignID, row.LastIdempotencyKey).Scan(&existingFP)
	if err == nil {
		if existingFP != fingerprint {
			return CampaignMotionQueueState{}, false, ErrCampaignMotionIdempotencyConflict
		}
		cur, gErr := scanCampaignMotionQueueState(tx.QueryRow(`
			SELECT id, tenant_id, campaign_id, handoff_id, job_id, creative_id,
			       project_id, revision_id, digest, execution_preference, policy_version,
			       phase, queue_state, wait_reason, next_window, eta_seconds,
			       retained_asset_versions_json, last_idempotency_key,
			       auto_outreach_triggered, auto_published, auto_conversion_recorded,
			       updated_by, created_at, updated_at
			FROM campaign_motion_queue_states
			WHERE tenant_id=? AND campaign_id=?`, row.TenantID, row.CampaignID))
		if gErr != nil {
			return CampaignMotionQueueState{}, false, gErr
		}
		if err := tx.Commit(); err != nil {
			return CampaignMotionQueueState{}, false, err
		}
		return cur, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CampaignMotionQueueState{}, false, err
	}

	ts := now()
	if row.ID == "" {
		row.ID = newID("cmq_")
	}
	if row.CreatedAt == "" {
		row.CreatedAt = ts
	}
	row.UpdatedAt = ts

	var etaVal any
	if row.ETASeconds != nil {
		etaVal = *row.ETASeconds
	}

	if _, err := tx.Exec(`
		INSERT INTO campaign_motion_queue_events(
			id, tenant_id, campaign_id, idempotency_key, request_fingerprint,
			job_id, creative_id, phase, queue_state, created_by, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		newID("cmqe_"), row.TenantID, row.CampaignID, row.LastIdempotencyKey, fingerprint,
		row.JobID, row.CreativeID, row.Phase, row.QueueState, row.UpdatedBy, ts); err != nil {
		return CampaignMotionQueueState{}, false, err
	}

	if _, err := tx.Exec(`
		INSERT INTO campaign_motion_queue_states(
			id, tenant_id, campaign_id, handoff_id, job_id, creative_id,
			project_id, revision_id, digest, execution_preference, policy_version,
			phase, queue_state, wait_reason, next_window, eta_seconds,
			retained_asset_versions_json, last_idempotency_key,
			auto_outreach_triggered, auto_published, auto_conversion_recorded,
			updated_by, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,0,?,?,?)
		ON CONFLICT(tenant_id, campaign_id) DO UPDATE SET
			handoff_id=excluded.handoff_id,
			job_id=excluded.job_id,
			creative_id=excluded.creative_id,
			project_id=excluded.project_id,
			revision_id=excluded.revision_id,
			digest=excluded.digest,
			execution_preference=CASE WHEN excluded.execution_preference<>'' THEN excluded.execution_preference ELSE campaign_motion_queue_states.execution_preference END,
			policy_version=excluded.policy_version,
			phase=excluded.phase,
			queue_state=excluded.queue_state,
			wait_reason=excluded.wait_reason,
			next_window=excluded.next_window,
			eta_seconds=excluded.eta_seconds,
			retained_asset_versions_json=CASE WHEN excluded.retained_asset_versions_json<>'[]' THEN excluded.retained_asset_versions_json ELSE campaign_motion_queue_states.retained_asset_versions_json END,
			last_idempotency_key=excluded.last_idempotency_key,
			updated_by=excluded.updated_by,
			updated_at=excluded.updated_at`,
		row.ID, row.TenantID, row.CampaignID, row.HandoffID, row.JobID, row.CreativeID,
		row.ProjectID, row.RevisionID, row.Digest, row.ExecutionPreference, row.PolicyVersion,
		row.Phase, row.QueueState, row.WaitReason, row.NextWindow, etaVal,
		row.RetainedAssetVersionsJSON, row.LastIdempotencyKey,
		row.UpdatedBy, row.CreatedAt, row.UpdatedAt); err != nil {
		return CampaignMotionQueueState{}, false, err
	}

	saved, err := scanCampaignMotionQueueState(tx.QueryRow(`
		SELECT id, tenant_id, campaign_id, handoff_id, job_id, creative_id,
		       project_id, revision_id, digest, execution_preference, policy_version,
		       phase, queue_state, wait_reason, next_window, eta_seconds,
		       retained_asset_versions_json, last_idempotency_key,
		       auto_outreach_triggered, auto_published, auto_conversion_recorded,
		       updated_by, created_at, updated_at
		FROM campaign_motion_queue_states
		WHERE tenant_id=? AND campaign_id=?`, row.TenantID, row.CampaignID))
	if err != nil {
		return CampaignMotionQueueState{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return CampaignMotionQueueState{}, false, err
	}
	return saved, false, nil
}

func scanCampaignMotionQueueState(sc interface{ Scan(...any) error }) (CampaignMotionQueueState, error) {
	var row CampaignMotionQueueState
	var eta sql.NullInt64
	var outreach, published, conv int
	err := sc.Scan(
		&row.ID, &row.TenantID, &row.CampaignID, &row.HandoffID, &row.JobID, &row.CreativeID,
		&row.ProjectID, &row.RevisionID, &row.Digest, &row.ExecutionPreference, &row.PolicyVersion,
		&row.Phase, &row.QueueState, &row.WaitReason, &row.NextWindow, &eta,
		&row.RetainedAssetVersionsJSON, &row.LastIdempotencyKey,
		&outreach, &published, &conv,
		&row.UpdatedBy, &row.CreatedAt, &row.UpdatedAt,
	)
	if err != nil {
		return CampaignMotionQueueState{}, err
	}
	if eta.Valid {
		v := eta.Int64
		row.ETASeconds = &v
	}
	row.AutoOutreachTriggered = outreach != 0
	row.AutoPublished = published != 0
	row.AutoConversionRecorded = conv != 0
	return row, nil
}

// ListCampaignMotionRenders returns all render candidates for one campaign.
func (s *Store) ListCampaignMotionRenders(tenantID, campaignID string) ([]CampaignMotionRender, error) {
	rows, err := s.DB.Query(`
		SELECT id, tenant_id, campaign_id, handoff_id, idempotency_key, request_fingerprint,
		       creative_id, project_id, revision_id, digest, render_id, variant_id,
		       channel, source_facts_version, render_mode, high_cost_model_calls,
		       billed_generation_count, cost_cents, billing_receipt_id, payer_ref,
		       budget_attribution_id, status, auto_outreach_triggered, auto_published,
		       auto_conversion_recorded, created_by, created_at, updated_at
		FROM campaign_motion_renders
		WHERE tenant_id=? AND campaign_id=?
		ORDER BY created_at ASC, id ASC`, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CampaignMotionRender{}
	for rows.Next() {
		r, err := scanCampaignMotionRender(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FindCampaignMotionRenderByIdempotencyKey checks if a render backflow key was
// already processed.
func (s *Store) FindCampaignMotionRenderByIdempotencyKey(tenantID, campaignID, idempotencyKey string) (CampaignMotionRender, bool, error) {
	row := s.DB.QueryRow(`
		SELECT id, tenant_id, campaign_id, handoff_id, idempotency_key, request_fingerprint,
		       creative_id, project_id, revision_id, digest, render_id, variant_id,
		       channel, source_facts_version, render_mode, high_cost_model_calls,
		       billed_generation_count, cost_cents, billing_receipt_id, payer_ref,
		       budget_attribution_id, status, auto_outreach_triggered, auto_published,
		       auto_conversion_recorded, created_by, created_at, updated_at
		FROM campaign_motion_renders
		WHERE tenant_id=? AND campaign_id=? AND idempotency_key=?`, tenantID, campaignID, idempotencyKey)
	r, err := scanCampaignMotionRender(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CampaignMotionRender{}, false, nil
	}
	if err != nil {
		return CampaignMotionRender{}, false, err
	}
	return r, true, nil
}

// UpsertCampaignMotionRenderBackflow inserts or updates a render candidate
// idempotently for (tenant_id, campaign_id, creative_id, variant_id) without
// creating a second project or triggering outreach/publish/conversion.
func (s *Store) UpsertCampaignMotionRenderBackflow(row CampaignMotionRender) (CampaignMotionRender, bool, error) {
	existing, found, err := s.FindCampaignMotionRenderByIdempotencyKey(row.TenantID, row.CampaignID, row.IdempotencyKey)
	if err != nil {
		return CampaignMotionRender{}, false, err
	}
	if found {
		if existing.RequestFingerprint != row.RequestFingerprint {
			return CampaignMotionRender{}, false, ErrCampaignMotionIdempotencyConflict
		}
		return existing, true, nil
	}

	ts := now()
	if row.ID == "" {
		row.ID = newID("cmr_")
	}
	if row.Status == "" {
		row.Status = "candidate"
	}
	if row.CreatedAt == "" {
		row.CreatedAt = ts
	}
	row.UpdatedAt = ts

	_, err = s.DB.Exec(`
		INSERT INTO campaign_motion_renders(
			id, tenant_id, campaign_id, handoff_id, idempotency_key, request_fingerprint,
			creative_id, project_id, revision_id, digest, render_id, variant_id,
			channel, source_facts_version, render_mode, high_cost_model_calls,
			billed_generation_count, cost_cents, billing_receipt_id, payer_ref,
			budget_attribution_id, status, auto_outreach_triggered, auto_published,
			auto_conversion_recorded, created_by, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,0,?,?,?)
		ON CONFLICT(tenant_id, campaign_id, creative_id, variant_id) DO UPDATE SET
			idempotency_key=excluded.idempotency_key,
			request_fingerprint=excluded.request_fingerprint,
			render_id=excluded.render_id,
			source_facts_version=excluded.source_facts_version,
			render_mode=excluded.render_mode,
			high_cost_model_calls=excluded.high_cost_model_calls,
			billed_generation_count=excluded.billed_generation_count,
			cost_cents=excluded.cost_cents,
			billing_receipt_id=excluded.billing_receipt_id,
			payer_ref=excluded.payer_ref,
			budget_attribution_id=excluded.budget_attribution_id,
			updated_at=excluded.updated_at`,
		row.ID, row.TenantID, row.CampaignID, row.HandoffID, row.IdempotencyKey, row.RequestFingerprint,
		row.CreativeID, row.ProjectID, row.RevisionID, row.Digest, row.RenderID, row.VariantID,
		row.Channel, row.SourceFactsVersion, row.RenderMode, row.HighCostModelCalls,
		row.BilledGenerationCount, row.CostCents, row.BillingReceiptID, row.PayerRef,
		row.BudgetAttributionID, row.Status, row.CreatedBy, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return CampaignMotionRender{}, false, err
	}
	saved, err := scanCampaignMotionRender(s.DB.QueryRow(`
		SELECT id, tenant_id, campaign_id, handoff_id, idempotency_key, request_fingerprint,
		       creative_id, project_id, revision_id, digest, render_id, variant_id,
		       channel, source_facts_version, render_mode, high_cost_model_calls,
		       billed_generation_count, cost_cents, billing_receipt_id, payer_ref,
		       budget_attribution_id, status, auto_outreach_triggered, auto_published,
		       auto_conversion_recorded, created_by, created_at, updated_at
		FROM campaign_motion_renders
		WHERE tenant_id=? AND campaign_id=? AND creative_id=? AND variant_id=?`,
		row.TenantID, row.CampaignID, row.CreativeID, row.VariantID))
	if err != nil {
		return CampaignMotionRender{}, false, err
	}
	return saved, false, nil
}

// DecideCampaignMotionRender marks a candidate render accepted or rejected on the
// Leads side only (no outreach/publish/creative writeback).
func (s *Store) DecideCampaignMotionRender(tenantID, campaignID, creativeID, variantID, status string) (CampaignMotionRender, error) {
	query := `UPDATE campaign_motion_renders SET status=?, updated_at=? WHERE tenant_id=? AND campaign_id=? AND creative_id=?`
	args := []any{status, now(), tenantID, campaignID, creativeID}
	if variantID != "" {
		query += ` AND variant_id=?`
		args = append(args, variantID)
	}
	res, err := s.DB.Exec(query, args...)
	if err != nil {
		return CampaignMotionRender{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return CampaignMotionRender{}, err
	}
	if affected == 0 {
		return CampaignMotionRender{}, sql.ErrNoRows
	}
	sel := `
		SELECT id, tenant_id, campaign_id, handoff_id, idempotency_key, request_fingerprint,
		       creative_id, project_id, revision_id, digest, render_id, variant_id,
		       channel, source_facts_version, render_mode, high_cost_model_calls,
		       billed_generation_count, cost_cents, billing_receipt_id, payer_ref,
		       budget_attribution_id, status, auto_outreach_triggered, auto_published,
		       auto_conversion_recorded, created_by, created_at, updated_at
		FROM campaign_motion_renders
		WHERE tenant_id=? AND campaign_id=? AND creative_id=?`
	selArgs := []any{tenantID, campaignID, creativeID}
	if variantID != "" {
		sel += ` AND variant_id=?`
		selArgs = append(selArgs, variantID)
	}
	sel += ` ORDER BY updated_at DESC LIMIT 1`
	return scanCampaignMotionRender(s.DB.QueryRow(sel, selArgs...))
}

func scanCampaignMotionRender(sc interface{ Scan(...any) error }) (CampaignMotionRender, error) {
	var row CampaignMotionRender
	var outreach, published, conv int
	err := sc.Scan(
		&row.ID, &row.TenantID, &row.CampaignID, &row.HandoffID, &row.IdempotencyKey, &row.RequestFingerprint,
		&row.CreativeID, &row.ProjectID, &row.RevisionID, &row.Digest, &row.RenderID, &row.VariantID,
		&row.Channel, &row.SourceFactsVersion, &row.RenderMode, &row.HighCostModelCalls,
		&row.BilledGenerationCount, &row.CostCents, &row.BillingReceiptID, &row.PayerRef,
		&row.BudgetAttributionID, &row.Status, &outreach, &published, &conv,
		&row.CreatedBy, &row.CreatedAt, &row.UpdatedAt,
	)
	if err != nil {
		return CampaignMotionRender{}, err
	}
	row.AutoOutreachTriggered = outreach != 0
	row.AutoPublished = published != 0
	row.AutoConversionRecorded = conv != 0
	return row, nil
}

// SaveCampaignMotionRepeatMethod persists a boundary-checked Repeat Method recipe.
func (s *Store) SaveCampaignMotionRepeatMethod(row CampaignMotionRepeatMethod) (CampaignMotionRepeatMethod, error) {
	ts := now()
	if row.ID == "" {
		row.ID = newID("cmrm_")
	}
	if row.CreatedAt == "" {
		row.CreatedAt = ts
	}
	row.UpdatedAt = ts
	pub := 0
	if row.PublicTemplate {
		pub = 1
	}
	_, err := s.DB.Exec(`
		INSERT INTO campaign_motion_repeat_methods(
			id, tenant_id, campaign_id, handoff_id, mode, name, template_id,
			template_visibility, team_grant_id, source_project_id, source_revision_id,
			source_facts_version, parametric_slots_json, public_template,
			created_by, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id, campaign_id, mode, template_id) DO UPDATE SET
			name=excluded.name,
			template_visibility=excluded.template_visibility,
			team_grant_id=excluded.team_grant_id,
			source_project_id=excluded.source_project_id,
			source_revision_id=excluded.source_revision_id,
			source_facts_version=excluded.source_facts_version,
			parametric_slots_json=excluded.parametric_slots_json,
			public_template=excluded.public_template,
			updated_at=excluded.updated_at`,
		row.ID, row.TenantID, row.CampaignID, row.HandoffID, row.Mode, row.Name, row.TemplateID,
		row.TemplateVisibility, row.TeamGrantID, row.SourceProjectID, row.SourceRevisionID,
		row.SourceFactsVersion, row.ParametricSlotsJSON, pub,
		row.CreatedBy, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return CampaignMotionRepeatMethod{}, err
	}
	return scanCampaignMotionRepeatMethod(s.DB.QueryRow(`
		SELECT id, tenant_id, campaign_id, handoff_id, mode, name, template_id,
		       template_visibility, team_grant_id, source_project_id, source_revision_id,
		       source_facts_version, parametric_slots_json, public_template,
		       created_by, created_at, updated_at
		FROM campaign_motion_repeat_methods
		WHERE tenant_id=? AND campaign_id=? AND mode=? AND template_id=?`,
		row.TenantID, row.CampaignID, row.Mode, row.TemplateID))
}

// ListCampaignMotionRepeatMethods returns all saved Repeat Methods for one campaign.
func (s *Store) ListCampaignMotionRepeatMethods(tenantID, campaignID string) ([]CampaignMotionRepeatMethod, error) {
	rows, err := s.DB.Query(`
		SELECT id, tenant_id, campaign_id, handoff_id, mode, name, template_id,
		       template_visibility, team_grant_id, source_project_id, source_revision_id,
		       source_facts_version, parametric_slots_json, public_template,
		       created_by, created_at, updated_at
		FROM campaign_motion_repeat_methods
		WHERE tenant_id=? AND campaign_id=?
		ORDER BY created_at ASC, id ASC`, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CampaignMotionRepeatMethod{}
	for rows.Next() {
		r, err := scanCampaignMotionRepeatMethod(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanCampaignMotionRepeatMethod(sc interface{ Scan(...any) error }) (CampaignMotionRepeatMethod, error) {
	var row CampaignMotionRepeatMethod
	var pub int
	err := sc.Scan(
		&row.ID, &row.TenantID, &row.CampaignID, &row.HandoffID, &row.Mode, &row.Name, &row.TemplateID,
		&row.TemplateVisibility, &row.TeamGrantID, &row.SourceProjectID, &row.SourceRevisionID,
		&row.SourceFactsVersion, &row.ParametricSlotsJSON, &pub,
		&row.CreatedBy, &row.CreatedAt, &row.UpdatedAt,
	)
	if err != nil {
		return CampaignMotionRepeatMethod{}, err
	}
	row.PublicTemplate = pub != 0
	return row, nil
}

// RevokeCampaignMotion marks a campaign handoff revoked (idempotent).
func (s *Store) RevokeCampaignMotion(tenantID, campaignID, revokedBy, reason string) (CampaignMotionRevocation, error) {
	ts := now()
	_, err := s.DB.Exec(`
		INSERT INTO campaign_motion_revocations(tenant_id, campaign_id, revoked_by, reason, revoked_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(tenant_id, campaign_id) DO UPDATE SET
			revoked_by=excluded.revoked_by,
			reason=excluded.reason,
			revoked_at=excluded.revoked_at`,
		tenantID, campaignID, revokedBy, reason, ts)
	if err != nil {
		return CampaignMotionRevocation{}, err
	}
	return s.GetCampaignMotionRevocation(tenantID, campaignID)
}

// GetCampaignMotionRevocation fetches a campaign revocation record if present.
func (s *Store) GetCampaignMotionRevocation(tenantID, campaignID string) (CampaignMotionRevocation, error) {
	var row CampaignMotionRevocation
	err := s.DB.QueryRow(`
		SELECT tenant_id, campaign_id, revoked_by, reason, revoked_at
		FROM campaign_motion_revocations
		WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID).Scan(
		&row.TenantID, &row.CampaignID, &row.RevokedBy, &row.Reason, &row.RevokedAt)
	return row, err
}

// IsCampaignMotionRevoked reports whether a campaign handoff has been revoked.
func (s *Store) IsCampaignMotionRevoked(tenantID, campaignID string) (bool, error) {
	var exists int
	err := s.DB.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM campaign_motion_revocations
			WHERE tenant_id=? AND campaign_id=?
		)`, tenantID, campaignID).Scan(&exists)
	return exists != 0, err
}
