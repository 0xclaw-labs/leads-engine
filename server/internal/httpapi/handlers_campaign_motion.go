// HUI-2747 获客侧活动动效交出。FEATURE_CAMPAIGN_MOTION 默认 off。
// 不调用模型，不生成成片，不把转化写回创作工程。
package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

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
	}
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
		writeJSON(w, http.StatusOK, motionView(nil, campaignmotion.Conversion{}, campaignmotion.Notice))
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
	s.Log.Printf("campaign motion handoff tenant=%s campaign=%s channels recorded, model_calls=0", c.Member.TenantID, id)
	s.writeCampaignMotion(w, saved)
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
	var local campaignmotion.Conversion
	_ = json.Unmarshal([]byte(row.LocalConversionJSON), &local)
	writeJSON(w, http.StatusOK, motionView(json.RawMessage(row.ExportJSON), local, campaignmotion.Notice))
}

func motionView(export json.RawMessage, local campaignmotion.Conversion, notice string) map[string]any {
	if len(export) == 0 {
		export = nil
	}
	return map[string]any{
		"export":                  export,
		"local_conversion":        local,
		"model_calls":             0,
		"creative_project_writes": 0,
		"piece_generated":         false,
		"piece_sent":              false,
		"notice":                  notice,
	}
}

func motionError(err error) (int, string, string) {
	switch {
	case errors.Is(err, campaignmotion.ErrDigest):
		return http.StatusBadRequest, "digest", "动效引用的 digest 必须是 sha256: 加 64 位小写十六进制，否则不能交出。"
	case errors.Is(err, campaignmotion.ErrMotion):
		return http.StatusBadRequest, "motion", "动效引用只能有 project_id、revision_id、campaign_id 和 digest。"
	case errors.Is(err, campaignmotion.ErrChannel):
		return http.StatusBadRequest, "channel", "至少要有一个渠道名。多个渠道只记下名字，不会按渠道次数生成。"
	case errors.Is(err, campaignmotion.ErrNoMotionReceiver):
		return http.StatusUnprocessableEntity, "motion_receiver", "变体绑定缺少动效接收方账号；没有收件方就不会伪造形状。"
	case errors.Is(err, campaignmotion.ErrForeignTenant):
		return http.StatusForbidden, "foreign_tenant", "变体绑定指向其他租户的动效工程，已拒绝。"
	case errors.Is(err, campaignmotion.ErrVariantExpired):
		return http.StatusUnprocessableEntity, "variant_expired", "变体绑定的到期时间已过；请换新的到期时间再交出。"
	default:
		return http.StatusBadRequest, "invalid", "活动交出不完整，或带有不能出域的内容。"
	}
}
