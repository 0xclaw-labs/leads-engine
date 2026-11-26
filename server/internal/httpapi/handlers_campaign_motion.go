// HUI-2747 获客侧活动动效交出与蜂群/MX-OPS 消费回流。FEATURE_CAMPAIGN_MOTION 默认 off。
// 不调用模型，不重建视频/动效编辑器或计算调度引擎，不生成/外发成片，不把转化写回创作工程。
package httpapi

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/campaignmotion"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

func (s *Server) mountCampaignMotion(mux *http.ServeMux) {
	// HUI-2626 盲评 P2①：capability 发现始终可达——关闭时 200+enabled:false，
	// 面板据此隐藏，详情页不再每次都打 404。fail-closed 不变：关闭时交出读写
	// 路由仍不挂载，一样不可见。
	mux.Handle("GET /api/v1/campaign-motion/capability", s.requireSession(s.handleCampaignMotionCapability))
	if s.Cfg.FeatureCampaignMotion {
		mux.Handle("GET /api/v1/campaigns/{id}/motion-handoff", s.requireSession(s.handleCampaignMotionGet))
		mux.Handle("POST /api/v1/campaigns/{id}/motion-handoff", s.requireSession(s.handleCampaignMotionPost))
		mux.Handle("POST /api/v1/campaigns/{id}/motion-handoff/queue-backflow", s.requireSession(s.handleCampaignMotionQueueBackflow))
		mux.Handle("POST /api/v1/campaigns/{id}/motion-handoff/render-backflow", s.requireSession(s.handleCampaignMotionRenderBackflow))
		mux.Handle("POST /api/v1/campaigns/{id}/motion-handoff/renders/{creativeId}/decision", s.requireSession(s.handleCampaignMotionRenderDecision))
		mux.Handle("POST /api/v1/campaigns/{id}/motion-handoff/conversion", s.requireSession(s.handleCampaignMotionConversion))
		mux.Handle("POST /api/v1/campaigns/{id}/motion-handoff/repeat-methods", s.requireSession(s.handleCampaignMotionRepeatMethod))
		mux.Handle("POST /api/v1/campaigns/{id}/motion-handoff/revoke", s.requireSession(s.handleCampaignMotionRevoke))
	}
}

func (s *Server) lockCampaignMotionKey(tenantID, campaignID string) func() {
	composite := tenantID + "\x00" + campaignID
	s.campaignMotionMu.Lock()
	if s.campaignMotionLocks == nil {
		s.campaignMotionLocks = map[string]*sync.Mutex{}
	}
	mu, ok := s.campaignMotionLocks[composite]
	if !ok {
		mu = &sync.Mutex{}
		s.campaignMotionLocks[composite] = mu
	}
	s.campaignMotionMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

func (s *Server) handleCampaignMotionCapability(w http.ResponseWriter, r *http.Request) {
	payload := motionView(nil, campaignmotion.Conversion{}, campaignmotion.Notice)
	payload["enabled"] = s.Cfg.FeatureCampaignMotion
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleCampaignMotionGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionReadRecord); !ok {
		return
	}
	row, err := s.St.GetCampaignMotion(c.Member.TenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		revoked, _ := s.St.IsCampaignMotionRevoked(c.Member.TenantID, id)
		humanPending, _ := s.St.HasPendingCampaignFollowUp(c.Member.TenantID, id)
		payload := motionView(nil, campaignmotion.Conversion{}, campaignmotion.Notice)
		payload["human_followup_pending"] = humanPending
		payload["wait_stratification"] = campaignmotion.BuildWaitStratification("", humanPending)
		payload["revoked"] = revoked
		writeJSON(w, http.StatusOK, payload)
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff lookup failed")
		return
	}
	s.writeCampaignMotion(w, row)
}

func (s *Server) handleCampaignMotionPost(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionUpdate); !ok {
		return
	}
	unlock := s.lockCampaignMotionKey(c.Member.TenantID, id)
	defer unlock()

	if revoked, err := s.St.IsCampaignMotionRevoked(c.Member.TenantID, id); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revocation lookup failed")
		return
	} else if revoked {
		fail(w, http.StatusForbidden, "revoked", "活动动效交接授权已撤销，已拒绝交出。")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	// The creative-project port is observed, not nil: zero writes is a count.
	port := &campaignmotion.CountingWriter{}
	prepared, err := campaignmotion.Prepare(c.Member.TenantID, id, body, port)
	if err != nil {
		status, code, message := motionError(err)
		fail(w, status, code, message)
		return
	}
	if err := port.AssertZero(); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff save failed")
		return
	}

	ent, err := s.resolveCampaignMotionEntitlement(c.Member.TenantID, id)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "entitlement_unavailable", "无法核验执行权益，已拒绝（fail-closed）。")
		return
	}
	existingPref := ""
	if existingRow, gErr := s.St.GetCampaignMotion(c.Member.TenantID, id); gErr == nil {
		var existingDoc campaignmotion.Document
		if json.Unmarshal([]byte(existingRow.ExportJSON), &existingDoc) == nil {
			existingPref = existingDoc.ExecutionPreference
		}
	}
	if _, err := campaignmotion.AuthorizeExecutionPreference(campaignmotion.EntitlementCheckInput{
		MemberRole:          c.Member.Role,
		RequestedPreference: prepared.Document.ExecutionPreference,
		ExistingPreference:  existingPref,
		Entitlement:         ent,
	}); err != nil {
		status, code, message := motionError(err)
		fail(w, status, code, message)
		return
	}
	if err := campaignmotion.ValidatePayerAuthorization(prepared.Document.Payer, ent); err != nil {
		status, code, message := motionError(err)
		fail(w, status, code, message)
		return
	}
	if ent.MaxBudgetCents > 0 && prepared.Document.Budget != nil && prepared.Document.Budget.MaxBudgetCents > ent.MaxBudgetCents {
		status, code, message := motionError(campaignmotion.ErrBudgetExceeded)
		fail(w, status, code, message)
		return
	}

	local, err := json.Marshal(prepared.Local)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff save failed")
		return
	}
	saved, err := s.St.SaveCampaignMotion(store.CampaignMotion{
		TenantID:            c.Member.TenantID,
		CampaignID:          id,
		ExportJSON:          string(prepared.Export),
		LocalConversionJSON: string(local),
		UpdatedBy:           c.Member.ID,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff save failed")
		return
	}
	s.Log.Printf("campaign motion handoff tenant=%s campaign=%s channels=%d revision=%s model_calls=0",
		c.Member.TenantID, id, len(prepared.Document.Channels), prepared.Document.Motion.RevisionID)
	s.writeCampaignMotion(w, saved)
}

func (s *Server) handleCampaignMotionQueueBackflow(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionUpdate); !ok {
		return
	}
	unlock := s.lockCampaignMotionKey(c.Member.TenantID, id)
	defer unlock()

	if revoked, err := s.St.IsCampaignMotionRevoked(c.Member.TenantID, id); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revocation lookup failed")
		return
	} else if revoked {
		fail(w, http.StatusForbidden, "revoked", "活动动效交接授权已撤销，已拒绝队列回流。")
		return
	}

	handoffRow, err := s.St.GetCampaignMotion(c.Member.TenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "campaign motion handoff not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff lookup failed")
		return
	}
	var expectedDoc campaignmotion.Document
	if err := json.Unmarshal([]byte(handoffRow.ExportJSON), &expectedDoc); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "stored handoff unreadable")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if err := guardBackflowRawJSON(body); err != nil {
		status, code, message := motionError(err)
		fail(w, status, code, message)
		return
	}
	var in campaignmotion.QueueBackflow
	if err := decodeStrictJSON(body, &in); err != nil {
		fail(w, http.StatusBadRequest, "invalid", "队列回流包含非法或未知字段。")
		return
	}
	if in.ExecutionPreference != "" {
		ent, err := s.resolveCampaignMotionEntitlement(c.Member.TenantID, id)
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "entitlement_unavailable", "无法核验执行权益，已拒绝（fail-closed）。")
			return
		}
		if _, err := campaignmotion.AuthorizeExecutionPreference(campaignmotion.EntitlementCheckInput{
			MemberRole:          c.Member.Role,
			RequestedPreference: in.ExecutionPreference,
			ExistingPreference:  expectedDoc.ExecutionPreference,
			Entitlement:         ent,
		}); err != nil {
			status, code, message := motionError(err)
			fail(w, status, code, message)
			return
		}
	}

	validated, err := campaignmotion.ValidateQueueBackflow(in, id, expectedDoc)
	if err != nil {
		status, code, message := motionError(err)
		fail(w, status, code, message)
		return
	}
	// Preserve pinned authorized asset versions from the handoff when night/idle
	// backflows omit an explicit asset list.
	if len(validated.RetainedAssetVersions) == 0 && len(expectedDoc.ApprovedAssets) > 0 {
		for _, a := range expectedDoc.ApprovedAssets {
			ver := a.Version
			if ver == "" {
				ver = "v1"
			}
			validated.RetainedAssetVersions = append(validated.RetainedAssetVersions, campaignmotion.RetainedAssetVersion{
				AssetID: a.ID,
				Version: ver,
			})
		}
	}
	if validated.ExecutionPreference == "" {
		validated.ExecutionPreference = expectedDoc.ExecutionPreference
	}

	// Refuse if an already-active queue state points to a different project_id.
	if existingQ, gErr := s.St.GetCampaignMotionQueueState(c.Member.TenantID, id); gErr == nil {
		if existingQ.ProjectID != validated.ProjectID {
			status, code, message := motionError(campaignmotion.ErrProjectMismatch)
			fail(w, status, code, message)
			return
		}
	} else if !errors.Is(gErr, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "queue state lookup failed")
		return
	}

	retainedJSON, err := json.Marshal(validated.RetainedAssetVersions)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "queue state encode failed")
		return
	}
	if validated.RetainedAssetVersions == nil {
		retainedJSON = []byte("[]")
	}
	fp := hashJSON(validated)

	savedQ, replayed, err := s.St.UpsertCampaignMotionQueueBackflow(store.CampaignMotionQueueState{
		TenantID:                  c.Member.TenantID,
		CampaignID:                id,
		HandoffID:                 handoffRow.ID,
		JobID:                     validated.JobID,
		CreativeID:                validated.CreativeID,
		ProjectID:                 validated.ProjectID,
		RevisionID:                validated.RevisionID,
		Digest:                    validated.Digest,
		ExecutionPreference:       validated.ExecutionPreference,
		PolicyVersion:             validated.PolicyVersion,
		Phase:                     validated.Phase,
		QueueState:                validated.QueueState,
		WaitReason:                validated.WaitReason,
		NextWindow:                validated.NextWindow,
		ETASeconds:                validated.ETASeconds,
		RetainedAssetVersionsJSON: string(retainedJSON),
		LastIdempotencyKey:        validated.IdempotencyKey,
		UpdatedBy:                 c.Member.ID,
	}, fp)
	if errors.Is(err, store.ErrCampaignMotionIdempotencyConflict) {
		fail(w, http.StatusConflict, "idempotency_conflict", "同一幂等键被不同队列回流载荷复用，已拒绝。")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "queue backflow save failed")
		return
	}

	s.Log.Printf("campaign motion queue_backflow tenant=%s campaign=%s job=%s phase=%s queue_state=%s replayed=%t",
		c.Member.TenantID, id, savedQ.JobID, savedQ.Phase, savedQ.QueueState, replayed)
	payload := s.buildCampaignMotionPayload(handoffRow)
	payload["replayed"] = replayed
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleCampaignMotionRenderBackflow(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionUpdate); !ok {
		return
	}
	unlock := s.lockCampaignMotionKey(c.Member.TenantID, id)
	defer unlock()

	if revoked, err := s.St.IsCampaignMotionRevoked(c.Member.TenantID, id); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revocation lookup failed")
		return
	} else if revoked {
		fail(w, http.StatusForbidden, "revoked", "活动动效交接授权已撤销，已拒绝渲染回流。")
		return
	}

	handoffRow, err := s.St.GetCampaignMotion(c.Member.TenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "campaign motion handoff not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff lookup failed")
		return
	}
	var expectedDoc campaignmotion.Document
	if err := json.Unmarshal([]byte(handoffRow.ExportJSON), &expectedDoc); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "stored handoff unreadable")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if err := guardBackflowRawJSON(body); err != nil {
		status, code, message := motionError(err)
		fail(w, status, code, message)
		return
	}
	var in campaignmotion.RenderBackflow
	if err := decodeStrictJSON(body, &in); err != nil {
		fail(w, http.StatusBadRequest, "invalid", "渲染回流包含非法或未知字段。")
		return
	}

	// Fast-path idempotent replay check before cumulative accounting so replaying
	// the exact same receipt does not falsely trigger duplicate-charge or spike errors.
	trimmedKey := strings.TrimSpace(in.IdempotencyKey)
	if trimmedKey != "" {
		existing, found, err := s.St.FindCampaignMotionRenderByIdempotencyKey(c.Member.TenantID, id, trimmedKey)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "render lookup failed")
			return
		}
		if found {
			// Re-validate shape (ignoring self in accounting) to build exact fingerprint.
			accEmpty := campaignmotion.RenderAccountingState{ExistingReceiptIDs: map[string]string{}}
			validatedReplay, vErr := campaignmotion.ValidateRenderBackflow(in, id, expectedDoc, accEmpty)
			if vErr != nil {
				status, code, message := motionError(vErr)
				fail(w, status, code, message)
				return
			}
			if existing.RequestFingerprint != hashJSON(validatedReplay) {
				fail(w, http.StatusConflict, "idempotency_conflict", "同一幂等键被不同渲染回流载荷复用，已拒绝。")
				return
			}
			payload := s.buildCampaignMotionPayload(handoffRow)
			payload["replayed"] = true
			payload["render"] = renderCandidateView(existing)
			writeJSON(w, http.StatusOK, payload)
			return
		}
	}

	existingRenders, err := s.St.ListCampaignMotionRenders(c.Member.TenantID, id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "render list failed")
		return
	}
	acc := campaignmotion.RenderAccountingState{
		ExistingReceiptIDs: map[string]string{},
	}
	seenCh := map[string]bool{}
	targetKey := strings.TrimSpace(in.CreativeID) + "|" + strings.TrimSpace(in.VariantID)
	for _, rRow := range existingRenders {
		rowKey := rRow.CreativeID + "|" + rRow.VariantID
		if rRow.BillingReceiptID != "" {
			acc.ExistingReceiptIDs[rRow.BillingReceiptID] = rowKey
		}
		if rowKey == targetKey {
			continue
		}
		acc.ExistingTotalHighCostCalls += rRow.HighCostModelCalls
		acc.ExistingTotalCostCents += rRow.CostCents
		if rRow.Channel != "" {
			seenCh[rRow.Channel] = true
		}
	}
	acc.ExistingChannelCount = len(seenCh)

	validated, err := campaignmotion.ValidateRenderBackflow(in, id, expectedDoc, acc)
	if err != nil {
		status, code, message := motionError(err)
		fail(w, status, code, message)
		return
	}

	port := &campaignmotion.CountingWriter{}
	if err := port.AssertZero(); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "creative project write forbidden")
		return
	}

	saved, replayed, err := s.St.UpsertCampaignMotionRenderBackflow(store.CampaignMotionRender{
		TenantID:              c.Member.TenantID,
		CampaignID:            id,
		HandoffID:             handoffRow.ID,
		IdempotencyKey:        validated.IdempotencyKey,
		RequestFingerprint:    hashJSON(validated),
		CreativeID:            validated.CreativeID,
		ProjectID:             validated.ProjectID,
		RevisionID:            validated.RevisionID,
		Digest:                validated.Digest,
		RenderID:              validated.RenderID,
		VariantID:             validated.VariantID,
		Channel:               validated.Channel,
		SourceFactsVersion:    validated.SourceFactsVersion,
		RenderMode:            validated.RenderMode,
		HighCostModelCalls:    validated.HighCostModelCalls,
		BilledGenerationCount: validated.BilledGenerationCount,
		CostCents:             validated.CostCents,
		BillingReceiptID:      validated.BillingReceiptID,
		PayerRef:              validated.PayerRef,
		BudgetAttributionID:   validated.BudgetAttributionID,
		Status:                "candidate",
		CreatedBy:             c.Member.ID,
	})
	if errors.Is(err, store.ErrCampaignMotionIdempotencyConflict) {
		fail(w, http.StatusConflict, "idempotency_conflict", "同一幂等键被不同渲染回流载荷复用，已拒绝。")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "render backflow save failed")
		return
	}

	s.Log.Printf("campaign motion render_backflow tenant=%s campaign=%s creative=%s variant=%s channel=%s replayed=%t",
		c.Member.TenantID, id, saved.CreativeID, saved.VariantID, saved.Channel, replayed)
	payload := s.buildCampaignMotionPayload(handoffRow)
	payload["replayed"] = replayed
	payload["render"] = renderCandidateView(saved)
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleCampaignMotionRenderDecision(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	creativeID := strings.TrimSpace(r.PathValue("creativeId"))
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionUpdate); !ok {
		return
	}
	unlock := s.lockCampaignMotionKey(c.Member.TenantID, id)
	defer unlock()

	if revoked, err := s.St.IsCampaignMotionRevoked(c.Member.TenantID, id); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revocation lookup failed")
		return
	} else if revoked {
		fail(w, http.StatusForbidden, "revoked", "活动动效交接授权已撤销。")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16+1))
	if err != nil || len(body) > 1<<16 {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	var in struct {
		Status               string `json:"status"`
		VariantID            string `json:"variant_id,omitempty"`
		AutoOutreach         bool   `json:"auto_outreach,omitempty"`
		AutoPublish          bool   `json:"auto_publish,omitempty"`
		AutoRecordConversion bool   `json:"auto_record_conversion,omitempty"`
	}
	if err := decodeStrictJSON(body, &in); err != nil {
		fail(w, http.StatusBadRequest, "invalid", "候选审核载荷非法。")
		return
	}
	if in.AutoOutreach || in.AutoPublish || in.AutoRecordConversion {
		status, code, message := motionError(campaignmotion.ErrAutoSideEffectForbidden)
		fail(w, status, code, message)
		return
	}
	st := strings.TrimSpace(in.Status)
	if st != "accepted" && st != "rejected" {
		fail(w, http.StatusBadRequest, "invalid", "status 只能为 accepted 或 rejected。")
		return
	}

	port := &campaignmotion.CountingWriter{}
	updated, err := s.St.DecideCampaignMotionRender(c.Member.TenantID, id, creativeID, strings.TrimSpace(in.VariantID), st)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "render candidate not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "render decision failed")
		return
	}
	if err := port.AssertZero(); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "creative project write forbidden")
		return
	}
	handoffRow, err := s.St.GetCampaignMotion(c.Member.TenantID, id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff lookup failed")
		return
	}
	payload := s.buildCampaignMotionPayload(handoffRow)
	payload["render"] = renderCandidateView(updated)
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleCampaignMotionConversion(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionUpdate); !ok {
		return
	}
	unlock := s.lockCampaignMotionKey(c.Member.TenantID, id)
	defer unlock()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16+1))
	if err != nil || len(body) > 1<<16 {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	var in struct {
		Leads                 int64 `json:"leads"`
		Conversions           int64 `json:"conversions"`
		RevenueCents          int64 `json:"revenue_cents"`
		WritebackCreativeFact bool  `json:"writeback_creative_fact,omitempty"`
	}
	if err := decodeStrictJSON(body, &in); err != nil {
		fail(w, http.StatusBadRequest, "invalid", "转化归因载荷非法。")
		return
	}
	if in.WritebackCreativeFact {
		status, code, message := motionError(campaignmotion.ErrCreativeWritebackForbidden)
		fail(w, status, code, message)
		return
	}
	if in.Leads < 0 || in.Conversions < 0 || in.RevenueCents < 0 {
		fail(w, http.StatusBadRequest, "invalid", "转化归因计数不能为负数。")
		return
	}

	// Observed zero-write port: Leads conversion attribution NEVER writes back
	// to the creative project.
	port := &campaignmotion.CountingWriter{}
	localJSON, err := json.Marshal(campaignmotion.Conversion{
		Leads:        in.Leads,
		Conversions:  in.Conversions,
		RevenueCents: in.RevenueCents,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "conversion encode failed")
		return
	}
	saved, err := s.St.UpdateCampaignMotionLocalConversion(c.Member.TenantID, id, string(localJSON), c.Member.ID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "campaign motion handoff not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "conversion update failed")
		return
	}
	if err := port.AssertZero(); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "creative project write forbidden")
		return
	}
	s.writeCampaignMotion(w, saved)
}

func (s *Server) handleCampaignMotionRepeatMethod(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionUpdate); !ok {
		return
	}
	unlock := s.lockCampaignMotionKey(c.Member.TenantID, id)
	defer unlock()

	if revoked, err := s.St.IsCampaignMotionRevoked(c.Member.TenantID, id); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revocation lookup failed")
		return
	} else if revoked {
		fail(w, http.StatusForbidden, "revoked", "活动动效交接授权已撤销，已拒绝保存复用配方。")
		return
	}

	handoffRow, err := s.St.GetCampaignMotion(c.Member.TenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "campaign motion handoff not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff lookup failed")
		return
	}
	var expectedDoc campaignmotion.Document
	if err := json.Unmarshal([]byte(handoffRow.ExportJSON), &expectedDoc); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "stored handoff unreadable")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16+1))
	if err != nil || len(body) > 1<<16 {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if err := guardBackflowRawJSON(body); err != nil {
		status, code, message := motionError(err)
		fail(w, status, code, message)
		return
	}
	var in campaignmotion.RepeatMethodSpec
	if err := decodeStrictJSON(body, &in); err != nil {
		fail(w, http.StatusBadRequest, "invalid", "Repeat Method 载荷包含非法或未知字段。")
		return
	}
	validated, err := campaignmotion.ValidateRepeatMethod(in, time.Now().UTC())
	if err != nil {
		status, code, message := motionError(err)
		fail(w, status, code, message)
		return
	}
	slotsJSON, err := json.Marshal(validated.ParametricSlots)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "slots encode failed")
		return
	}
	if validated.ParametricSlots == nil {
		slotsJSON = []byte("{}")
	}
	factsVer := validated.SourceFactsVersion
	if factsVer == "" {
		factsVer = expectedDoc.SourceFactsVersion
	}

	saved, err := s.St.SaveCampaignMotionRepeatMethod(store.CampaignMotionRepeatMethod{
		TenantID:            c.Member.TenantID,
		CampaignID:          id,
		HandoffID:           handoffRow.ID,
		Mode:                validated.Mode,
		Name:                validated.Name,
		TemplateID:          validated.TemplateID,
		TemplateVisibility:  validated.TemplateVisibility,
		TeamGrantID:         validated.TeamGrantID,
		SourceProjectID:     expectedDoc.Motion.ProjectID,
		SourceRevisionID:    expectedDoc.Motion.RevisionID,
		SourceFactsVersion:  factsVer,
		ParametricSlotsJSON: string(slotsJSON),
		PublicTemplate:      validated.PublicTemplate,
		CreatedBy:           c.Member.ID,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "repeat method save failed")
		return
	}
	payload := s.buildCampaignMotionPayload(handoffRow)
	payload["repeat_method"] = repeatMethodView(saved)
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleCampaignMotionRevoke(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionUpdate); !ok {
		return
	}
	unlock := s.lockCampaignMotionKey(c.Member.TenantID, id)
	defer unlock()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16+1))
	if err != nil || len(body) > 1<<16 {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	reason := ""
	if len(bytes.TrimSpace(body)) > 0 {
		var in struct {
			Reason string `json:"reason"`
		}
		if err := decodeStrictJSON(body, &in); err != nil {
			fail(w, http.StatusBadRequest, "invalid", "撤销载荷非法。")
			return
		}
		reason = strings.TrimSpace(in.Reason)
		if campaignmotion.ContainsPrivacyLeak(reason) {
			status, code, message := motionError(campaignmotion.ErrPrivacyBlocked)
			fail(w, status, code, message)
			return
		}
	}
	rev, err := s.St.RevokeCampaignMotion(c.Member.TenantID, id, c.Member.ID, reason)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revoke failed")
		return
	}
	s.Log.Printf("campaign motion revoked tenant=%s campaign=%s by=%s", c.Member.TenantID, id, c.Member.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"revoked":     true,
		"campaign_id": rev.CampaignID,
		"revoked_at":  rev.RevokedAt,
	})
}

func (s *Server) resolveCampaignMotionEntitlement(tenantID, campaignID string) (campaignmotion.VerifiedEntitlement, error) {
	if s.CampaignMotionEntitlement != nil {
		return s.CampaignMotionEntitlement.VerifyCampaignEntitlement(tenantID, campaignID)
	}
	cache, err := s.St.GetEntitlementCache(tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return campaignmotion.VerifiedEntitlement{
			TenantID:   tenantID,
			Tier:       "free",
			PlanStatus: "inactive",
			VerifiedBy: "local_entitlement_cache",
		}, nil
	}
	if err != nil {
		return campaignmotion.VerifiedEntitlement{}, err
	}
	tier := strings.TrimSpace(cache.Plan)
	if tier == "" {
		tier = "free"
	}
	return campaignmotion.VerifiedEntitlement{
		TenantID:          tenantID,
		Tier:              tier,
		PlanStatus:        strings.TrimSpace(cache.Status),
		OrgBillingAccount: strings.TrimSpace(cache.OrgBillingAccountID),
		AuthorizedPayer:   strings.TrimSpace(cache.AuthorizedPayerAccountID),
		VerifiedBy:        "local_entitlement_cache",
	}, nil
}

func (s *Server) campaignMotionSubject(w http.ResponseWriter, c *caller, id string, action authz.Action) (string, bool) {
	assignee, err := s.St.LoadCampaignMotionSubject(id, c.Member.TenantID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return "", false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign lookup failed")
		return "", false
	}
	if !s.requireAction(c, action, authz.RecordScope{TenantID: c.Member.TenantID, AssigneeMemberID: assignee}, w) {
		return "", false
	}
	return assignee, true
}

func (s *Server) writeCampaignMotion(w http.ResponseWriter, row store.CampaignMotion) {
	writeJSON(w, http.StatusOK, s.buildCampaignMotionPayload(row))
}

func (s *Server) buildCampaignMotionPayload(row store.CampaignMotion) map[string]any {
	var local campaignmotion.Conversion
	_ = json.Unmarshal([]byte(row.LocalConversionJSON), &local)
	payload := motionView(json.RawMessage(row.ExportJSON), local, campaignmotion.Notice)

	queueStateStr := ""
	if q, err := s.St.GetCampaignMotionQueueState(row.TenantID, row.CampaignID); err == nil {
		queueStateStr = q.QueueState
		payload["job_queue_view"] = queueStateView(q)
	}

	renders, err := s.St.ListCampaignMotionRenders(row.TenantID, row.CampaignID)
	renderViews := make([]map[string]any, 0, len(renders))
	metricItems := make([]campaignmotion.RenderMetricItem, 0, len(renders))
	pendingReview := false
	if err == nil {
		for _, rRow := range renders {
			renderViews = append(renderViews, renderCandidateView(rRow))
			metricItems = append(metricItems, campaignmotion.RenderMetricItem{
				Channel:            rRow.Channel,
				Status:             rRow.Status,
				HighCostModelCalls: rRow.HighCostModelCalls,
				CostCents:          rRow.CostCents,
			})
			if rRow.Status == "candidate" {
				pendingReview = true
			}
		}
	}

	openFollowUp, _ := s.St.HasPendingCampaignFollowUp(row.TenantID, row.CampaignID)
	humanPending := openFollowUp || pendingReview
	payload["human_followup_pending"] = humanPending
	payload["wait_stratification"] = campaignmotion.BuildWaitStratification(queueStateStr, humanPending)
	payload["render_candidates"] = renderViews
	payload["attribution"] = campaignmotion.ComputeAttributionSummary(metricItems)

	methods, mErr := s.St.ListCampaignMotionRepeatMethods(row.TenantID, row.CampaignID)
	methodViews := make([]map[string]any, 0, len(methods))
	if mErr == nil {
		for _, mRow := range methods {
			methodViews = append(methodViews, repeatMethodView(mRow))
		}
	}
	payload["repeat_methods"] = methodViews

	revoked, _ := s.St.IsCampaignMotionRevoked(row.TenantID, row.CampaignID)
	payload["revoked"] = revoked
	payload["auto_outreach_triggered"] = false
	payload["auto_published"] = false
	payload["auto_conversion_recorded"] = false
	return payload
}

func queueStateView(q store.CampaignMotionQueueState) map[string]any {
	retained := []campaignmotion.RetainedAssetVersion{}
	_ = json.Unmarshal([]byte(q.RetainedAssetVersionsJSON), &retained)
	if retained == nil {
		retained = []campaignmotion.RetainedAssetVersion{}
	}
	// Nullable ETA: nil serializes as JSON null (unknown ≠ 0).
	var eta any
	if q.ETASeconds != nil {
		eta = *q.ETASeconds
	}
	return map[string]any{
		"id":                       q.ID,
		"job_id":                   q.JobID,
		"creative_id":              q.CreativeID,
		"campaign_id":              q.CampaignID,
		"project_id":               q.ProjectID,
		"revision_id":              q.RevisionID,
		"digest":                   q.Digest,
		"execution_preference":     q.ExecutionPreference,
		"policy_version":           q.PolicyVersion,
		"phase":                    q.Phase,
		"queue_state":              q.QueueState,
		"wait_reason":              q.WaitReason,
		"next_window":              q.NextWindow,
		"eta_seconds":              eta,
		"retained_asset_versions":  retained,
		"last_idempotency_key":     q.LastIdempotencyKey,
		"auto_outreach_triggered":  false,
		"auto_published":           false,
		"auto_conversion_recorded": false,
		"updated_at":               q.UpdatedAt,
	}
}

func renderCandidateView(r store.CampaignMotionRender) map[string]any {
	return map[string]any{
		"id":                       r.ID,
		"campaign_id":              r.CampaignID,
		"creative_id":              r.CreativeID,
		"project_id":               r.ProjectID,
		"revision_id":              r.RevisionID,
		"digest":                   r.Digest,
		"render_id":                r.RenderID,
		"variant_id":               r.VariantID,
		"channel":                  r.Channel,
		"source_facts_version":     r.SourceFactsVersion,
		"render_mode":              r.RenderMode,
		"high_cost_model_calls":    r.HighCostModelCalls,
		"billed_generation_count":  r.BilledGenerationCount,
		"cost_cents":               r.CostCents,
		"billing_receipt_id":       r.BillingReceiptID,
		"payer_ref":                r.PayerRef,
		"budget_attribution_id":    r.BudgetAttributionID,
		"status":                   r.Status,
		"auto_outreach_triggered":  false,
		"auto_published":           false,
		"auto_conversion_recorded": false,
		"updated_at":               r.UpdatedAt,
	}
}

func repeatMethodView(m store.CampaignMotionRepeatMethod) map[string]any {
	slots := map[string]string{}
	_ = json.Unmarshal([]byte(m.ParametricSlotsJSON), &slots)
	if slots == nil {
		slots = map[string]string{}
	}
	return map[string]any{
		"id":                   m.ID,
		"campaign_id":          m.CampaignID,
		"mode":                 m.Mode,
		"name":                 m.Name,
		"template_id":          m.TemplateID,
		"template_visibility":  m.TemplateVisibility,
		"team_grant_id":        m.TeamGrantID,
		"source_project_id":    m.SourceProjectID,
		"source_revision_id":   m.SourceRevisionID,
		"source_facts_version": m.SourceFactsVersion,
		"parametric_slots":     slots,
		"public_template":      m.PublicTemplate,
		"updated_at":           m.UpdatedAt,
	}
}

func motionView(export json.RawMessage, local campaignmotion.Conversion, notice string) map[string]any {
	if len(export) == 0 {
		export = nil
	}
	return map[string]any{
		"export":                   export,
		"local_conversion":         local,
		"model_calls":              0,
		"creative_project_writes":  0,
		"piece_generated":          false,
		"piece_sent":               false,
		"job_queue_view":           nil,
		"human_followup_pending":   false,
		"wait_stratification":      campaignmotion.BuildWaitStratification("", false),
		"render_candidates":        []map[string]any{},
		"repeat_methods":           []map[string]any{},
		"attribution":              campaignmotion.ComputeAttributionSummary(nil),
		"revoked":                  false,
		"auto_outreach_triggered":  false,
		"auto_published":           false,
		"auto_conversion_recorded": false,
		"notice":                   notice,
	}
}

func guardBackflowRawJSON(body []byte) error {
	var tree any
	if err := json.Unmarshal(body, &tree); err != nil {
		return campaignmotion.ErrInvalid
	}
	if err := campaignmotion.ValidateNoGlobalResourceConfig(tree); err != nil {
		return err
	}
	if hasBackflowPrivacyKey(tree) {
		return campaignmotion.ErrPrivacyBlocked
	}
	return nil
}

func hasBackflowPrivacyKey(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "phone", "email", "mobile", "chat", "chats", "chat_record", "transcript",
				"messages", "conversation", "unauthorized_leads", "customer_privacy",
				"crm_profile", "id_card", "address", "wechat", "contacts":
				return true
			}
			if hasBackflowPrivacyKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if hasBackflowPrivacyKey(child) {
				return true
			}
		}
	}
	return false
}

func decodeStrictJSON(body []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}

func hashJSON(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func motionError(err error) (int, string, string) {
	switch {
	case errors.Is(err, campaignmotion.ErrDigest):
		return http.StatusBadRequest, "digest", "动效引用的 digest 必须是 sha256: 加 64 位小写十六进制，否则不能交出。"
	case errors.Is(err, campaignmotion.ErrMotion):
		return http.StatusBadRequest, "motion", "动效引用缺少或包含非法 project_id、revision_id、campaign_id 或 digest。"
	case errors.Is(err, campaignmotion.ErrChannel):
		return http.StatusBadRequest, "channel", "至少要有一个合法渠道名。多个渠道只做参数化变体，不按渠道次数原创生成。"
	case errors.Is(err, campaignmotion.ErrNoMotionReceiver):
		return http.StatusUnprocessableEntity, "motion_receiver", "变体绑定缺少动效接收方账号；没有收件方就不会伪造形状。"
	case errors.Is(err, campaignmotion.ErrForeignTenant):
		return http.StatusForbidden, "foreign_tenant", "变体绑定或引用指向其他租户的动效工程，已拒绝。"
	case errors.Is(err, campaignmotion.ErrVariantExpired):
		return http.StatusUnprocessableEntity, "variant_expired", "交接引用、授权素材或变体绑定已过期，已拒绝。"
	case errors.Is(err, campaignmotion.ErrPrivacyBlocked):
		return http.StatusUnprocessableEntity, "privacy_blocked", "检测到 CRM 客户隐私、联系人手机号/邮箱、聊天记录或未授权线索，已拦截不出域。"
	case errors.Is(err, campaignmotion.ErrGlobalResourceConfigForbidden):
		return http.StatusForbidden, "global_resource_forbidden", "禁止提交或暴露全局算力/队列资源配置。"
	case errors.Is(err, campaignmotion.ErrInternalPreferenceForbidden):
		return http.StatusForbidden, "internal_privilege_forbidden", "严禁访问 internal 调度权限或非受权优先级。"
	case errors.Is(err, campaignmotion.ErrEntitlementDenied):
		return http.StatusForbidden, "entitlement_denied", "当前账号未获得经服务端核验的执行偏好权益（priority/night/idle）。"
	case errors.Is(err, campaignmotion.ErrAgentEscalation):
		return http.StatusForbidden, "agent_escalation_forbidden", "Agent 禁止提权或请求 priority 调度等级。"
	case errors.Is(err, campaignmotion.ErrLinearModelCallSpike):
		return http.StatusUnprocessableEntity, "linear_model_call_blocked", "参数化多渠道变体禁止产生按渠道线性增长的高价原创模型调用或重复生成计费。"
	case errors.Is(err, campaignmotion.ErrDuplicateCharge):
		return http.StatusConflict, "duplicate_charge", "检测到重复计费回执，已拒绝重复扣费。"
	case errors.Is(err, campaignmotion.ErrBudgetExceeded):
		return http.StatusUnprocessableEntity, "budget_exceeded", "超出活动预算上限或付款方未获授权。"
	case errors.Is(err, campaignmotion.ErrPrivateWorkCannotBecomePublic):
		return http.StatusForbidden, "private_template_forbidden", "用户私有客户作品绝不能自动变成公共模板或跨客户复用。"
	case errors.Is(err, campaignmotion.ErrRepeatMethodForbidden):
		return http.StatusForbidden, "repeat_method_forbidden", "复用模式非法或缺少有效的授权团队模板凭证。"
	case errors.Is(err, campaignmotion.ErrAutoSideEffectForbidden):
		return http.StatusUnprocessableEntity, "auto_side_effect_forbidden", "队列或渲染回流严禁自动触达客户、自动发布内容或自动记录转化。"
	case errors.Is(err, campaignmotion.ErrProjectMismatch):
		return http.StatusConflict, "project_mismatch", "回流工程或版本与当前活动交接不一致，拒绝新建第二份工程。"
	case errors.Is(err, campaignmotion.ErrRevoked):
		return http.StatusForbidden, "revoked", "活动动效交接授权已撤销。"
	case errors.Is(err, campaignmotion.ErrIdempotencyConflict):
		return http.StatusConflict, "idempotency_conflict", "同一幂等键被不同载荷复用，已拒绝。"
	case errors.Is(err, campaignmotion.ErrCreativeWritebackForbidden):
		return http.StatusUnprocessableEntity, "creative_writeback_forbidden", "转化归因归获客权威，禁止反向写入创作工程为创作事实。"
	default:
		return http.StatusBadRequest, "invalid", "活动交出不完整，或带有不能出域的内容。"
	}
}
