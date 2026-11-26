// handlers_matrix_draft.go — HUI-2391 获客侧矩阵草稿交出。
// FEATURE_MATRIX_HANDOFF 默认 off：关闭时路由不挂载。
//
// 纪律：
//   - 只建 AG10 文案草稿（source_app=leads, content_type=copy）。图片与视频不走此路由。
//   - return_target 由服务端生成，客户端不能指定路径。
//   - tenant 取自会话成员，不取自请求体。
//   - 幂等不本地短路：每次（含重放）都先经 AG10 CreateDraft 裁决再落本地行——首次之后撤权/授权过期同样在此拒绝。
//   - AG10 拒绝、不可达、存储失败都不写本地行。
//   - 草稿 ≠ 发布 ≠ 扣费 ≠ 触达。published、charged 恒为 false；plan、item、外发 id 未知时为 null。
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/matrixdraftclient"
	"github.com/bianjiefilm/leads-engine/server/internal/matrixhandoff"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

const (
	matrixDraftNotice = "已记下矩阵草稿引用。草稿不等于发布、扣费或触达；审批、排期与发布以矩阵为准。"
	matrixCallTimeout = 10 * time.Second
	matrixBodyLimit   = 64 << 10
)

var (
	matrixTokenRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	// matrixSourceRefRe accepts path-like declared origins, e.g.
	// "touch/landing/camp-matrix". A declaration, never a verification.
	matrixSourceRefRe = regexp.MustCompile(`^[A-Za-z0-9_./:-]{1,256}$`)
	matrixHashRe      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// matrixDraftBody is the only request shape. Unknown keys, such as phone or
// email, fail the strict decode before anything leaves this process.
type matrixDraftBody struct {
	AssetID          string  `json:"asset_id"`
	AssetHash        string  `json:"asset_hash"`
	Revision         int     `json:"revision"`
	ContentType      string  `json:"content_type"`
	GrantRef         string  `json:"grant_ref"`
	LicenseRef       string  `json:"license_ref"`
	LicenseExpiresAt *string `json:"license_expires_at"`
	BrandID          string  `json:"brand_id"`
	PayerID          string  `json:"payer_id"`
	AssetSourceRef   string  `json:"asset_source_ref"`
}

// matrixSourceEventBody is the publish-source backflow shape. It carries only
// ids, a lifecycle kind, optional monotonic version guards, and optional integer
// metrics; unknown keys fail.
type matrixSourceEventBody struct {
	ProviderEventID string `json:"provider_event_id"`
	Kind            string `json:"kind"`
	SourceVersion   *int   `json:"source_version,omitempty"`
	Revision        *int   `json:"revision,omitempty"`
	MatrixPlanID    string `json:"matrix_plan_id"`
	MatrixItemID    string `json:"matrix_item_id"`
	ExternalPostID  string `json:"external_post_id"`
	Reach           *int   `json:"reach"`
	Likes           *int   `json:"likes"`
	Leads           *int   `json:"leads"`
}

// newMatrixDraftClient wires the AG10 client only when the flag and URL are both set.
// A nil client makes the POST route fail closed with config_gate_matrix.
func newMatrixDraftClient(cfg config.Config) *matrixdraftclient.Client {
	if !cfg.FeatureMatrixHandoff || strings.TrimSpace(cfg.MatrixDraftBaseURL) == "" {
		return nil
	}
	return &matrixdraftclient.Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(cfg.MatrixDraftBaseURL), "/"),
		HTTP:    &http.Client{Timeout: matrixCallTimeout},
	}
}

func (s *Server) mountMatrixDraft(mux *http.ServeMux) {
	if !s.Cfg.FeatureMatrixHandoff {
		return
	}
	mux.Handle("POST /api/v1/campaigns/{id}/matrix-drafts", s.requireSession(s.handleMatrixDraftPost))
	mux.Handle("GET /api/v1/campaigns/{id}/matrix-drafts", s.requireSession(s.handleMatrixDraftList))
	mux.Handle("GET /api/v1/campaigns/{id}/matrix-drafts/{draftID}/receipt", s.requireSession(s.handleMatrixDraftReceipt))
	mux.Handle("POST /api/v1/campaigns/{id}/matrix-drafts/{draftID}/source-events", s.requireSession(s.handleMatrixDraftSourceEvent))
}

func (s *Server) handleMatrixDraftPost(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionUpdate); !ok {
		return
	}
	if s.MatrixDraft == nil {
		fail(w, http.StatusServiceUnavailable, "config_gate_matrix", "矩阵草稿服务地址未配置，已停用。")
		return
	}
	if !matrixTokenRe.MatchString(id) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, matrixBodyLimit+1))
	if err != nil || len(raw) > matrixBodyLimit {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	in, err := decodeMatrixDraftBody(raw)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req, errStatus, code, msg, ok := matrixRequest(in, c.Member.TenantID, id, time.Now().UTC())
	if !ok {
		fail(w, errStatus, code, msg)
		return
	}
	// No local shortcut: the service decides every create, including a replay.
	// A grant revoked or a license expired after the first draft must refuse here too.
	ctx, cancel := context.WithTimeout(r.Context(), matrixCallTimeout)
	defer cancel()
	draft, err := s.MatrixDraft.CreateDraft(ctx, req)
	if err != nil {
		matrixCallError(w, err)
		return
	}
	var expiresText string
	if req.LicenseExpiresAt != nil {
		expiresText = req.LicenseExpiresAt.UTC().Format(time.RFC3339)
	}
	saved, existed, err := s.St.SaveMatrixDraftRef(store.MatrixDraftRef{
		TenantID:         c.Member.TenantID,
		CampaignID:       id,
		Revision:         in.Revision,
		AssetID:          in.AssetID,
		AssetHash:        in.AssetHash,
		ContentType:      "copy",
		GrantRef:         in.GrantRef,
		LicenseRef:       in.LicenseRef,
		LicenseExpiresAt: expiresText,
		BrandID:          in.BrandID,
		PayerID:          in.PayerID,
		ReturnPath:       "/campaigns/" + id,
		AssetSourceRef:   in.AssetSourceRef,
		DraftID:          draft.ID,
		Status:           draft.Status,
		CreatedBy:        c.Member.ID,
	})
	if err != nil {
		// The service may already hold the draft. A retry with the same fields replays it.
		fail(w, http.StatusInternalServerError, "store_failed", "draft recorded upstream; retry with the same fields")
		return
	}
	replay := existed || draft.Replay
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	s.Log.Printf("matrix draft ref tenant=%s campaign=%s revision=%d replay=%v", c.Member.TenantID, id, in.Revision, replay)
	writeJSON(w, status, matrixRefView(saved, replay))
}

func (s *Server) handleMatrixDraftList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionReadRecord); !ok {
		return
	}
	rows, err := s.St.ListMatrixDraftRefs(c.Member.TenantID, id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft lookup failed")
		return
	}
	drafts := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		drafts = append(drafts, matrixRefView(row, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"drafts": drafts, "notice": matrixDraftNotice})
}

func decodeMatrixDraftBody(raw []byte) (matrixDraftBody, error) {
	var in matrixDraftBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return matrixDraftBody{}, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return matrixDraftBody{}, errors.New("trailing json")
	}
	return in, nil
}

// matrixRequest validates shape locally. It returns an HTTP status, code and copy
// when the request cannot be a copy draft. It never guesses a missing field, never
// coerces image/video/needs_video into copy, and fails closed on expired licenses
// or smuggled private data before calling Matrix.
func matrixRequest(in matrixDraftBody, tenantID, campaignID string, now time.Time) (matrixdraftclient.Request, int, string, string, bool) {
	if err := matrixhandoff.ValidateContentType(in.ContentType); err != nil {
		return matrixdraftclient.Request{}, http.StatusUnprocessableEntity, "wrong_type", "矩阵草稿只接受文案(copy)；图片≠视频，needs_video≠成功，不静默生成、转码或扣费。", false
	}
	if !matrixTokenRe.MatchString(in.AssetID) || !matrixHashRe.MatchString(in.AssetHash) || in.Revision < 1 {
		return matrixdraftclient.Request{}, http.StatusUnprocessableEntity, "invalid", "内容资产、哈希或版本不完整。", false
	}
	for _, v := range []string{in.GrantRef, in.LicenseRef, in.BrandID, in.PayerID} {
		if !matrixTokenRe.MatchString(v) {
			return matrixdraftclient.Request{}, http.StatusUnprocessableEntity, "invalid", "授权、许可、品牌或付款方标识不完整。", false
		}
	}
	// The asset source is a recorded declaration of where the hash came from.
	// It is stored and echoed, never verified here.
	if in.AssetSourceRef != "" && !matrixSourceRefRe.MatchString(in.AssetSourceRef) {
		return matrixdraftclient.Request{}, http.StatusUnprocessableEntity, "invalid", "资产来源引用格式不完整。", false
	}
	for _, v := range []string{in.AssetID, in.GrantRef, in.LicenseRef, in.BrandID, in.PayerID, in.AssetSourceRef} {
		if err := matrixhandoff.ValidateRestrictedDeclaration(v); err != nil {
			return matrixdraftclient.Request{}, http.StatusBadRequest, "bad_request", "交接引用不得携带手机号、邮箱或 CRM 内部备注。", false
		}
	}
	req := matrixdraftclient.Request{
		ContentType: "copy",
		CampaignID:  campaignID,
		Revision:    in.Revision,
		AssetID:     in.AssetID,
		AssetHash:   in.AssetHash,
		GrantRef:    in.GrantRef,
		TenantID:    tenantID,
		BrandID:     in.BrandID,
		PayerID:     in.PayerID,
		LicenseRef:  in.LicenseRef,
		ReturnPath:  "/campaigns/" + campaignID,
	}
	if in.LicenseExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *in.LicenseExpiresAt)
		if err != nil {
			return matrixdraftclient.Request{}, http.StatusUnprocessableEntity, "invalid", "许可到期时间必须是 RFC3339。", false
		}
		if err := matrixhandoff.ValidateLicenseExpiry(&t, now); err != nil {
			return matrixdraftclient.Request{}, http.StatusForbidden, "license_expired", "该素材许可已过期，草稿未建立。", false
		}
		req.LicenseExpiresAt = &t
	}
	return req, 0, "", "", true
}

var matrixRefusal = map[string]string{
	"grant_unknown":   "矩阵无法确认该授权，草稿未建立。",
	"grant_expired":   "该授权已过期，草稿未建立。",
	"grant_revoked":   "该授权已撤销，草稿未建立。",
	"license_expired": "该素材许可已过期，草稿未建立。",
}

// matrixCallError maps a service answer to a copy the user can act on.
// Only 503 is retryable with the same fields. No local row is written on any error.
func matrixCallError(w http.ResponseWriter, err error) {
	var api *matrixdraftclient.APIError
	switch {
	case errors.Is(err, matrixdraftclient.ErrNotLeadsCopy):
		fail(w, http.StatusUnprocessableEntity, "wrong_type", "矩阵草稿只接受文案(copy)。")
	case errors.Is(err, matrixdraftclient.ErrInvalid):
		fail(w, http.StatusUnprocessableEntity, "invalid", "草稿请求不完整。")
	case errors.As(err, &api):
		if msg, ok := matrixRefusal[api.Code]; ok {
			fail(w, http.StatusForbidden, api.Code, msg)
			return
		}
		switch api.Code {
		case "wrong_type", "old_version", "hash_conflict", "idempotency_conflict":
			fail(w, http.StatusConflict, api.Code, "矩阵拒绝了这次草稿：与已记录的版本或幂等键冲突。")
		case "bad_request":
			fail(w, http.StatusUnprocessableEntity, "invalid", "矩阵认为草稿参数无效。")
		default:
			fail(w, http.StatusServiceUnavailable, "matrix_unavailable", "矩阵草稿服务暂不可用，可用同样的内容重试；本地未记录。")
		}
	default:
		fail(w, http.StatusServiceUnavailable, "matrix_unavailable", "矩阵草稿服务暂不可用，可用同样的内容重试；本地未记录。")
	}
}

// handleMatrixDraftReceipt answers with the live AG10 receipt plus the local
// row view. It never invents an answer from the local row: when the service
// cannot be reached the endpoint says so with 503 and the row stays untouched.
// Plan, item, and external post ids stay null until a source event lands them.
func (s *Server) handleMatrixDraftReceipt(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	draftRefID := r.PathValue("draftID")
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionReadRecord); !ok {
		return
	}
	if s.MatrixDraft == nil {
		fail(w, http.StatusServiceUnavailable, "config_gate_matrix", "矩阵草稿服务地址未配置，已停用。")
		return
	}
	if !matrixTokenRe.MatchString(draftRefID) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	ref, err := s.St.GetMatrixDraftRefByID(c.Member.TenantID, draftRefID)
	if err != nil || ref.CampaignID != id {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), matrixCallTimeout)
	defer cancel()
	live, err := s.MatrixDraft.GetReceipt(ctx, c.Member.TenantID, ref.DraftID)
	if err != nil {
		var api *matrixdraftclient.APIError
		if errors.As(err, &api) {
			if msg, ok := matrixRefusal[api.Code]; ok {
				fail(w, http.StatusForbidden, api.Code, msg)
				return
			}
		}
		fail(w, http.StatusServiceUnavailable, "matrix_unavailable", "矩阵回查暂不可用；本地行未改写。")
		return
	}
	s.Log.Printf("matrix draft receipt tenant=%s campaign=%s ref=%s draft=%s status=%s", c.Member.TenantID, id, ref.ID, ref.DraftID, live.Status)
	writeJSON(w, http.StatusOK, matrixReceiptView(live, ref, time.Now().UTC()))
}

// handleMatrixDraftSourceEvent lands one publish-source event on the campaign's
// draft ref. It only records ids, lifecycle, and optional metrics: it never
// creates a publish item, never sets published or charged, and drops private
// domain keys by strict decode. The real matrix webhook machine auth is the
// AG01 chain; this session route is the local stand-in.
func (s *Server) handleMatrixDraftSourceEvent(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	id := r.PathValue("id")
	draftRefID := r.PathValue("draftID")
	if _, ok := s.campaignMotionSubject(w, c, id, authz.ActionUpdate); !ok {
		return
	}
	if !matrixTokenRe.MatchString(draftRefID) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, matrixBodyLimit+1))
	if err != nil || len(raw) > matrixBodyLimit {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	in, err := decodeMatrixSourceEventBody(raw)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	switch in.Kind {
	case "result", "revoke", "replay", "edit", "withdraw":
	default:
		fail(w, http.StatusUnprocessableEntity, "unknown_kind", "来源事件类型不被记录。")
		return
	}
	if !matrixTokenRe.MatchString(in.ProviderEventID) {
		fail(w, http.StatusUnprocessableEntity, "invalid", "来源事件标识不完整。")
		return
	}
	for _, refToken := range []string{in.MatrixPlanID, in.MatrixItemID, in.ExternalPostID} {
		if refToken == "" {
			continue
		}
		if !matrixTokenRe.MatchString(refToken) {
			fail(w, http.StatusUnprocessableEntity, "invalid", "来源引用标识格式不合法。")
			return
		}
		if err := matrixhandoff.ValidateRestrictedDeclaration(refToken); err != nil {
			fail(w, http.StatusBadRequest, "bad_request", "来源引用不得携带敏感信息或客户档案。")
			return
		}
	}
	if err := matrixhandoff.ValidateMetrics(in.Reach, in.Likes, in.Leads); err != nil {
		fail(w, http.StatusUnprocessableEntity, "invalid", "指标计数不能为负数；未知指标请传 null。")
		return
	}
	var sourceVersion, expectedRevision int
	if in.SourceVersion != nil {
		if *in.SourceVersion < 1 {
			fail(w, http.StatusUnprocessableEntity, "invalid", "source_version 必须大于等于 1。")
			return
		}
		sourceVersion = *in.SourceVersion
	}
	if in.Revision != nil {
		if *in.Revision < 1 {
			fail(w, http.StatusUnprocessableEntity, "invalid", "revision 必须大于等于 1。")
			return
		}
		expectedRevision = *in.Revision
	}
	ref, err := s.St.GetMatrixDraftRefByID(c.Member.TenantID, draftRefID)
	if err != nil || ref.CampaignID != id {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	facts := store.MatrixDraftSourceFacts{
		Kind:             in.Kind,
		SourceVersion:    sourceVersion,
		ExpectedRevision: expectedRevision,
		MatrixPlanID:     in.MatrixPlanID,
		MatrixItemID:     in.MatrixItemID,
		ExternalPostID:   in.ExternalPostID,
		Reach:            in.Reach,
		Likes:            in.Likes,
		Leads:            in.Leads,
	}
	created, replayed, err := s.St.AppendMatrixDraftSourceEvent(c.Member.TenantID, ref.ID, in.ProviderEventID, c.Member.ID, facts)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrEventConflict):
			fail(w, http.StatusConflict, "idempotency_conflict", "同一来源事件带了不同事实，已拒绝。")
		case errors.Is(err, store.ErrStaleEvent):
			fail(w, http.StatusConflict, "stale_receipt", "旧回执或晚到事件不能覆盖已采用新版，本地已保持不变。")
		case errors.Is(err, store.ErrRevoked):
			fail(w, http.StatusForbidden, "grant_revoked", "该草稿引用已撤销，结果事件不再落地。")
		default:
			fail(w, http.StatusUnprocessableEntity, "invalid", "来源事件不完整。")
		}
		return
	}
	replay := replayed || !created
	stored, err := s.St.GetMatrixDraftRefByID(c.Member.TenantID, ref.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "source event lookup failed")
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	s.Log.Printf("matrix draft source event tenant=%s campaign=%s ref=%s kind=%s replay=%v", c.Member.TenantID, id, ref.ID, in.Kind, replay)
	writeJSON(w, status, matrixRefView(stored, replay))
}

func decodeMatrixSourceEventBody(raw []byte) (matrixSourceEventBody, error) {
	var in matrixSourceEventBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return matrixSourceEventBody{}, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return matrixSourceEventBody{}, errors.New("trailing json")
	}
	return in, nil
}

// matrixReceiptView merges the live service answer with the local row. Facts
// the source has not supplied stay null; without official platform publish
// authorization, published and charged remain false and publish_stage never
// fabricates "published".
func matrixReceiptView(live matrixdraftclient.Draft, ref store.MatrixDraftRef, fetchedAt time.Time) map[string]any {
	var expires any
	if live.LicenseExpiresAt != nil {
		expires = live.LicenseExpiresAt.UTC().Format(time.RFC3339)
	}
	rawStatus := live.Status
	if ref.Status == "revoked" {
		rawStatus = "revoked"
	}
	stage, published := matrixhandoff.ClassifyPublishStage(rawStatus, false)
	// Without official publish authorization, never adopt a fabricated "published"
	// status or published=true / charged=true claim from an upstream fixture.
	if strings.EqualFold(strings.TrimSpace(rawStatus), "published") || strings.EqualFold(strings.TrimSpace(rawStatus), "live") {
		rawStatus = matrixhandoff.StageDraftSaved
	}
	return map[string]any{
		"draft_id":                  live.ID,
		"schema_version":            live.SchemaVersion,
		"source_app":                live.SourceApp,
		"campaign_id":               ref.CampaignID,
		"revision":                  live.Revision,
		"asset_id":                  live.AssetID,
		"asset_hash":                live.AssetHash,
		"content_type":              live.ContentType,
		"status":                    rawStatus,
		"publish_stage":             stage,
		"official_publish_authorized": false,
		"published":                 published,
		"charged":                   false,
		"ugc_reward_triggered":      false,
		"conversion_roi":            nil,
		"fetched_at":                fetchedAt.UTC().Format(time.RFC3339),
		"license_expires_at":        expires,
		"return_path":               ref.ReturnPath,
		"brand_id":                  ref.BrandID,
		"created_by":                ref.CreatedBy,
		"asset_source_ref":          nilIfEmpty(ref.AssetSourceRef),
		"matrix_plan_id":            nilIfEmpty(ref.MatrixPlanID),
		"matrix_item_id":            nilIfEmpty(ref.MatrixItemID),
		"external_post_id":          nilIfEmpty(ref.ExternalPostID),
		"matrix_reach":              ref.MatrixReach,
		"matrix_likes":              ref.MatrixLikes,
		"matrix_leads":              ref.MatrixLeads,
		"notice":                    matrixDraftNotice,
	}
}

func matrixRefView(ref store.MatrixDraftRef, replay bool) map[string]any {
	var expires any
	if ref.LicenseExpiresAt != "" {
		expires = ref.LicenseExpiresAt
	}
	stage, published := matrixhandoff.ClassifyPublishStage(ref.Status, false)
	return map[string]any{
		"ref_id":                    ref.ID,
		"draft_id":                  ref.DraftID,
		"status":                    ref.Status,
		"publish_stage":             stage,
		"official_publish_authorized": false,
		"campaign_id":               ref.CampaignID,
		"revision":                  ref.Revision,
		"asset_id":                  ref.AssetID,
		"asset_hash":                ref.AssetHash,
		"content_type":              ref.ContentType,
		"return_path":               ref.ReturnPath,
		"brand_id":                  ref.BrandID,
		"created_by":                ref.CreatedBy,
		"fetched_at":                ref.UpdatedAt,
		"license_expires_at":        expires,
		"asset_source_ref":          nilIfEmpty(ref.AssetSourceRef),
		"published":                 published,
		"charged":                   ref.Charged,
		"ugc_reward_triggered":      false,
		"conversion_roi":            nil,
		"matrix_plan_id":            nilIfEmpty(ref.MatrixPlanID),
		"matrix_item_id":            nilIfEmpty(ref.MatrixItemID),
		"external_post_id":          nilIfEmpty(ref.ExternalPostID),
		"matrix_reach":              ref.MatrixReach,
		"matrix_likes":              ref.MatrixLikes,
		"matrix_leads":              ref.MatrixLeads,
		"idempotent_replay":         replay,
		"notice":                    matrixDraftNotice,
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
