// HUI-1682 场景内轻文案与受限专业制作交接。FEATURE_LIGHT_COPY 默认 off。
// 没有真实模型时生成失败并说明原因；保存和手改不依赖模型。
// 严格守卫：
//   - 最小已确认事实 + 隐私剥离/拒绝（严禁手机号列表、客户全文、内部备注、未授权画像）；
//   - 过期价格/缺事实标 unknown / needs_clarification，绝不用模板占位冒充真实内容；
//   - draft 保存≠已发送/已发布≠获得营销许可；晚到异步生成绝不覆盖人工改稿（CAS + expected_revision）；
//   - 受限交接按幂等键防重、撤权/过期报价/跨租户 fail-closed，结果仅以受控 asset/task 候选回流。
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/lightcopy"
	"github.com/bianjiefilm/leads-engine/server/internal/platformtask"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

func (s *Server) mountLightCopy(mux *http.ServeMux) {
	// HUI-2626 盲评 P2①：capability 发现始终可达——关闭时 200+enabled:false，
	// 面板据此隐藏，详情页不再每次都打 404。fail-closed 不变：关闭时草稿读写
	// 路由仍不挂载，一样不可见。
	mux.Handle("GET /api/v1/light-copy/capability", s.requireSession(s.handleLightCapability))
	if s.Cfg.FeatureLightCopy {
		s.mountLightSubject(mux, "opportunities", lightcopy.KindOpportunity)
		s.mountLightSubject(mux, "campaigns", lightcopy.KindCampaign)
	}
}

func (s *Server) mountLightSubject(mux *http.ServeMux, segment, subjectKind string) {
	base := "/api/v1/" + segment + "/{id}/light-copy"
	mux.Handle("GET "+base, s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightList(w, r, subjectKind)
	}))
	mux.Handle("GET "+base+"/handoffs", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightHandoffList(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/revoke", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightSubjectRevoke(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/handoffs/{handoffId}/revoke", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightHandoffRevoke(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/handoffs/{handoffId}/recover", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightHandoffRecover(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/handoffs/{handoffId}/returns", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightHandoffReturn(w, r, subjectKind)
	}))
	mux.Handle("PUT "+base+"/{kind}", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightSave(w, r, subjectKind)
	}))
	mux.Handle("GET "+base+"/{kind}/revisions", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightRevisions(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/{kind}/generate", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightGenerate(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/{kind}/confirm", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightConfirm(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/{kind}/facts", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightFacts(w, r, subjectKind)
	}))
	mux.Handle("POST "+base+"/{kind}/handoff", s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		s.handleLightHandoff(w, r, subjectKind)
	}))
}

func (s *Server) lockLightHandoffKey(tenantID, key string) func() {
	composite := tenantID + "\x00" + key
	s.lightHandoffMu.Lock()
	if s.lightHandoffLocks == nil {
		s.lightHandoffLocks = map[string]*sync.Mutex{}
	}
	mu, ok := s.lightHandoffLocks[composite]
	if !ok {
		mu = &sync.Mutex{}
		s.lightHandoffLocks[composite] = mu
	}
	s.lightHandoffMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// textCapability reports the effective platform-task capability used for
// light-copy generation.
func (s *Server) textCapability() string {
	if c := strings.TrimSpace(s.Cfg.TaskTextCapability); c != "" {
		return c
	}
	return platformtask.DefaultTextCapability
}

func (s *Server) handleLightCapability(w http.ResponseWriter, r *http.Request) {
	ready := s.LightCopyPhraser != nil
	note := "没有接入真实文案模型。生成会失败并说明原因，不会用模板占位。保存和手改不需要模型。生成完成不等于已发送、已发布或已取得营销许可。"
	if ready {
		note = "文案生成走平台任务计量（报价/预占/结算沿既有语义）。任务未完成或未返回正文时仍然拒绝，不写占位。生成完成不等于已发送、已发布或已取得营销许可。"
	}
	payload := map[string]any{
		"enabled":          s.Cfg.FeatureLightCopy,
		"model_ready":      ready,
		"tool_entry_ready": s.LightCopyToolDispatcher != nil,
		"live_charge":      0,
		"tools": []string{
			lightcopy.ToolGoBoost,
			lightcopy.ToolSwarm,
			lightcopy.ToolProductImage,
			lightcopy.ToolDigitalHuman,
			lightcopy.ToolAiCut,
		},
		"note": note,
	}
	if ready {
		payload["provider"] = "platformtask:" + s.textCapability()
	}
	writeJSON(w, http.StatusOK, payload)
}

func validLightKind(kind string) bool {
	return kind == lightcopy.KindReply || kind == lightcopy.KindEmail || kind == lightcopy.KindBrief
}

func (s *Server) lightSubject(w http.ResponseWriter, r *http.Request, subjectKind string, action authz.Action) (lightcopy.Subject, bool) {
	c := callerFrom(r)
	id := r.PathValue("id")
	var (
		sub      lightcopy.Subject
		assignee string
		err      error
	)
	if subjectKind == lightcopy.KindOpportunity {
		sub, assignee, err = s.St.LoadLightOpportunity(id, c.Member.TenantID)
	} else {
		sub, assignee, err = s.St.LoadLightCampaign(id, c.Member.TenantID)
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrLightNotCampaign) {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return lightcopy.Subject{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "scene lookup failed")
		return lightcopy.Subject{}, false
	}
	if !s.requireAction(c, action, authz.RecordScope{TenantID: sub.TenantID, AssigneeMemberID: assignee}, w) {
		return lightcopy.Subject{}, false
	}
	return sub, true
}

func (s *Server) handleLightList(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionReadRecord)
	if !ok {
		return
	}
	items, err := s.St.ListLightDrafts(sub.TenantID, sub.Kind, sub.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft list failed")
		return
	}
	if items == nil {
		items = []store.LightDraft{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleLightRevisions(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionReadRecord)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !validLightKind(kind) {
		fail(w, http.StatusBadRequest, "bad_request", "kind must be reply, email, or marketing_brief")
		return
	}
	items, err := s.St.ListLightDraftRevisions(sub.TenantID, sub.Kind, sub.ID, kind)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft revisions lookup failed")
		return
	}
	if items == nil {
		items = []store.LightDraftRevision{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleLightSave(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !validLightKind(kind) {
		fail(w, http.StatusBadRequest, "bad_request", "kind must be reply, email, or marketing_brief")
		return
	}
	var in struct {
		Body             string `json:"body"`
		ExpectedRevision *int   `json:"expected_revision"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	current, err := s.St.GetLightDraft(sub.TenantID, sub.Kind, sub.ID, kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "draft lookup failed")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		current = store.LightDraft{}
	}
	if in.ExpectedRevision != nil && *in.ExpectedRevision != current.ContentVersion {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":             lightcopy.ReasonRevisionConflict,
			"message":           "草稿已被更新，请先刷新后再保存，避免覆盖他人或已保存的修改。",
			"expected_revision": *in.ExpectedRevision,
			"current_version":   current.ContentVersion,
		})
		return
	}
	next, reason := lightcopy.SaveManual(draftToRule(current), in.Body)
	if reason != "" {
		fail(w, http.StatusBadRequest, reason, "草稿正文不能为空")
		return
	}
	draftRow := ruleToDraft(next, current, sub, kind, callerFrom(r).Member.ID)
	// Manual edits clear active model provenance on the current row while
	// preserving full historical provenance in light_copy_draft_revisions.
	draftRow.TaskID = ""
	draftRow.BillingVerdict = ""
	draftRow.UsageRef = ""
	saved, err := s.St.SaveLightDraftCAS(draftRow, in.ExpectedRevision, false)
	if errors.Is(err, store.ErrLightRevisionConflict) {
		latest, _ := s.St.GetLightDraft(sub.TenantID, sub.Kind, sub.ID, kind)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":           lightcopy.ReasonRevisionConflict,
			"message":         "草稿版本冲突，未覆盖当前已保存内容。",
			"current_version": latest.ContentVersion,
		})
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft save failed")
		return
	}
	s.Log.Printf("light copy save tenant=%s subject=%s kind=%s version=%d", sub.TenantID, sub.ID, kind, saved.ContentVersion)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleLightGenerate(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !validLightKind(kind) {
		fail(w, http.StatusBadRequest, "bad_request", "kind must be reply, email, or marketing_brief")
		return
	}
	var in struct {
		Selected            []string                    `json:"selected"`
		Facts               []lightcopy.ConfirmedFact   `json:"facts"`
		AuthorizedAssets    []lightcopy.AuthorizedAsset `json:"authorized_assets"`
		SampleCategory      string                      `json:"sample_category"`
		Language            string                      `json:"language"`
		ExpectedRevision    *int                        `json:"expected_revision"`
		AllowUnknownMarking bool                        `json:"allow_unknown_marking"`
		StripPII            bool                        `json:"strip_pii"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	refused := func(code, message string, receipt map[string]any, extra map[string]any) {
		body := map[string]any{
			"error": code, "message": message, "model": receipt,
			"generated": false, "sent": false, "published": false, "marketing_permitted": false,
		}
		for k, v := range extra {
			body[k] = v
		}
		writeJSON(w, http.StatusConflict, body)
	}
	if s.LightCopyPhraser == nil {
		s.Log.Printf("light copy generate refused tenant=%s subject=%s kind=%s reason=%s", sub.TenantID, sub.ID, kind, lightcopy.ReasonModelUnavailable)
		refused(lightcopy.ReasonModelUnavailable, "没有接入真实文案模型，不能生成正文，也不会用模板占位。请在当前页手写或修改草稿。",
			map[string]any{"task_id": "", "cost_cents": 0, "billing_verdict": "not_completed"}, nil)
		return
	}
	current, err := s.St.GetLightDraft(sub.TenantID, sub.Kind, sub.ID, kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "draft lookup failed")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		current = store.LightDraft{}
	}
	baseVersion := current.ContentVersion
	if in.ExpectedRevision != nil && *in.ExpectedRevision != baseVersion {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":             lightcopy.ReasonRevisionConflict,
			"message":           "草稿已被人工修改，生成前检测到版本冲突，未发起模型调用。",
			"expected_revision": *in.ExpectedRevision,
			"current_version":   baseVersion,
			"generated":         false,
			"sent":              false,
			"published":         false,
			"marketing_permitted": false,
		})
		return
	}
	// The fact & PII gate runs before any metered call: only confirmed,
	// authorized, PII-free facts can reach the prompt; bad inputs cost zero.
	brief, reason := lightcopy.ProjectBrief(lightcopy.BriefInput{
		CallerTenant:        callerFrom(r).Member.TenantID,
		Subject:             sub,
		Selected:            in.Selected,
		Facts:               in.Facts,
		AuthorizedAssets:    in.AuthorizedAssets,
		SampleCategory:      in.SampleCategory,
		AllowUnknownMarking: in.AllowUnknownMarking,
		StripPII:            in.StripPII,
	}, time.Now().UTC())
	if reason != "" {
		fail(w, http.StatusBadRequest, reason, factMessage(reason))
		return
	}
	lang := strings.TrimSpace(in.Language)
	if lang == "" {
		lang = "zh"
	}
	keySuffix := selectedKey(in.Selected)
	if len(in.Facts) > 0 || len(in.AuthorizedAssets) > 0 || len(brief.UnknownFields) > 0 {
		keySuffix = brief.InputFactsHash[:16]
	}
	key := "lightcopy:" + sub.Kind + ":" + sub.ID + ":" + kind + ":v" + strconv.Itoa(baseVersion) + ":" + keySuffix
	result := s.LightCopyPhraser.Generate(r.Context(), platformtask.GenerateInput{
		IdempotencyKey: key, Language: lang, Prompt: lightcopy.PromptFromBrief(kind, lang, brief),
	})
	usageRef := ""
	if result.TaskID != "" {
		usageRef = "platformtask:" + s.textCapability() + ":" + result.TaskID
	}
	receipt := map[string]any{
		"task_id":         result.TaskID,
		"cost_cents":      result.CostCents,
		"billing_verdict": result.BillingVerdict,
		"usage_ref":       usageRef,
	}
	if result.BillingVerdict != "recorded" {
		s.Log.Printf("light copy generate not completed tenant=%s subject=%s kind=%s task=%s verdict=%s", sub.TenantID, sub.ID, kind, result.TaskID, result.BillingVerdict)
		refused("generation_not_completed", "平台任务没有完成，这次没有生成正文，也没有记账。可以重试或手写。", receipt, map[string]any{"brief": brief})
		return
	}
	if strings.TrimSpace(result.Text) == "" {
		s.Log.Printf("light copy generate unusable output tenant=%s subject=%s kind=%s task=%s cost=%d", sub.TenantID, sub.ID, kind, result.TaskID, result.CostCents)
		refused("model_output_unusable", "平台任务已完成并记账，但没有返回可用正文。这次不写草稿，账务按 recorded 如实保留。", receipt, map[string]any{"brief": brief})
		return
	}
	audit := lightcopy.AuditGeneratedCopy(brief, result.Text)
	if !audit.Passed {
		s.Log.Printf("light copy generate audit blocked tenant=%s subject=%s kind=%s task=%s reason=%s", sub.TenantID, sub.ID, kind, result.TaskID, audit.BlockingReason)
		msg := "模型输出未通过事实与隐私核验，正文不写入，账务按 recorded 如实保留。"
		if audit.BlockingReason == lightcopy.ReasonPlaceholder {
			msg = "不能使用占位正文。任务已记账，正文不写入。"
		} else if audit.BlockingReason == lightcopy.ReasonPIIDetected {
			msg = "模型输出包含敏感隐私信息，已拦截写入。任务已记账。"
		} else if audit.BlockingReason == lightcopy.ReasonUngroundedClaim {
			msg = "模型输出包含无依据价格或绝对承诺新增，已拦截写入。任务已记账。"
		}
		refused(audit.BlockingReason, msg, receipt, map[string]any{"brief": brief, "audit": audit})
		return
	}
	next, reason := lightcopy.Generate(draftToRule(current), lightcopy.ModelOutput{
		FromLiveModel: true, Text: result.Text, CostCents: result.CostCents,
	})
	if reason == lightcopy.ReasonPlaceholder {
		s.Log.Printf("light copy generate placeholder tenant=%s subject=%s kind=%s task=%s cost=%d", sub.TenantID, sub.ID, kind, result.TaskID, result.CostCents)
		refused(lightcopy.ReasonPlaceholder, "不能使用占位正文。任务已记账，正文不写入。", receipt, map[string]any{"brief": brief, "audit": audit})
		return
	}
	if reason != "" {
		refused("model_output_unusable", "模型输出没有通过检查，正文不写入。", receipt, map[string]any{"brief": brief, "audit": audit})
		return
	}
	draftRow := ruleToDraft(next, current, sub, kind, callerFrom(r).Member.ID)
	draftRow.InputFacts = brief.Facts
	draftRow.InputFactsHash = brief.InputFactsHash
	draftRow.UnknownFields = brief.UnknownFields
	draftRow.TaskID = result.TaskID
	draftRow.BillingVerdict = result.BillingVerdict
	draftRow.UsageRef = usageRef
	draftRow.SampleCategory = brief.SampleCategory

	// Atomic CAS write anchored on baseVersion with human-overwrite protection:
	// a late or slow model generation NEVER overwrites a human edit saved in the meantime.
	saved, err := s.St.SaveLightDraftCAS(draftRow, &baseVersion, true)
	if errors.Is(err, store.ErrLightDraftModifiedByHuman) || errors.Is(err, store.ErrLightRevisionConflict) {
		latest, _ := s.St.GetLightDraft(sub.TenantID, sub.Kind, sub.ID, kind)
		s.Log.Printf("light copy generate stale overwrite blocked tenant=%s subject=%s kind=%s base=%d current=%d task=%s",
			sub.TenantID, sub.ID, kind, baseVersion, latest.ContentVersion, result.TaskID)
		refused(lightcopy.ReasonDraftModifiedByHuman,
			"生成期间草稿已被人工修改保存，晚到生成结果未覆盖人工改稿；模型任务与账务回执已如实保留。",
			receipt,
			map[string]any{
				"base_version":    baseVersion,
				"current_version": latest.ContentVersion,
				"brief":           brief,
				"audit":           audit,
			})
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft save failed")
		return
	}
	s.Log.Printf("light copy generate tenant=%s subject=%s kind=%s version=%d task=%s verdict=%s cost=%d", sub.TenantID, sub.ID, kind, saved.ContentVersion, result.TaskID, result.BillingVerdict, result.CostCents)
	writeJSON(w, http.StatusOK, map[string]any{
		"draft": saved,
		"model": receipt,
		"brief": brief,
		"audit": audit,
	})
}

func selectedKey(selected []string) string {
	cp := append([]string(nil), selected...)
	sort.Strings(cp)
	return strings.Join(cp, "+")
}

func (s *Server) handleLightConfirm(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !validLightKind(kind) {
		fail(w, http.StatusBadRequest, "bad_request", "kind must be reply, email, or marketing_brief")
		return
	}
	var in struct {
		ExpectedRevision *int `json:"expected_revision"`
	}
	// Body is optional ({} or empty) for backward compatibility.
	if r.Body != nil {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 4<<10))
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &in); err != nil {
				fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
				return
			}
		}
	}
	current, err := s.St.GetLightDraft(sub.TenantID, sub.Kind, sub.ID, kind)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "draft not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft lookup failed")
		return
	}
	if in.ExpectedRevision != nil && *in.ExpectedRevision != current.ContentVersion {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":             lightcopy.ReasonRevisionConflict,
			"message":           "草稿版本已更新，请先核对最新版本再确认。",
			"expected_revision": *in.ExpectedRevision,
			"current_version":   current.ContentVersion,
		})
		return
	}
	next := lightcopy.Confirm(draftToRule(current))
	draftRow := ruleToDraft(next, current, sub, kind, callerFrom(r).Member.ID)
	saved, err := s.St.SaveLightDraftCAS(draftRow, in.ExpectedRevision, false)
	if errors.Is(err, store.ErrLightRevisionConflict) {
		latest, _ := s.St.GetLightDraft(sub.TenantID, sub.Kind, sub.ID, kind)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":           lightcopy.ReasonRevisionConflict,
			"message":         "草稿版本冲突，请先核对最新版本再确认。",
			"current_version": latest.ContentVersion,
		})
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "draft confirm failed")
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleLightFacts(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionReadRecord)
	if !ok {
		return
	}
	var in struct {
		Selected            []string                    `json:"selected"`
		Facts               []lightcopy.ConfirmedFact   `json:"facts"`
		AuthorizedAssets    []lightcopy.AuthorizedAsset `json:"authorized_assets"`
		SampleCategory      string                      `json:"sample_category"`
		AllowUnknownMarking bool                        `json:"allow_unknown_marking"`
		StripPII            bool                        `json:"strip_pii"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	brief, reason := lightcopy.ProjectBrief(lightcopy.BriefInput{
		CallerTenant:        callerFrom(r).Member.TenantID,
		Subject:             sub,
		Selected:            in.Selected,
		Facts:               in.Facts,
		AuthorizedAssets:    in.AuthorizedAssets,
		SampleCategory:      in.SampleCategory,
		AllowUnknownMarking: in.AllowUnknownMarking,
		StripPII:            in.StripPII,
	}, time.Now().UTC())
	if reason != "" {
		fail(w, http.StatusBadRequest, reason, factMessage(reason))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subject_kind":        brief.SubjectKind,
		"subject_id":          brief.SubjectID,
		"sample_category":     brief.SampleCategory,
		"facts":               brief.Facts,
		"authorized_assets":   brief.AuthorizedAssets,
		"unknown_fields":      brief.UnknownFields,
		"clarification_items": brief.ClarificationItems,
		"input_facts_hash":    brief.InputFactsHash,
		"stripped_pii":        brief.StrippedPII,
	})
}

func factMessage(reason string) string {
	switch reason {
	case lightcopy.ReasonTenant:
		return "场景不属于当前租户"
	case lightcopy.ReasonPrice, lightcopy.ReasonQuoteExpired:
		return "没有未过期的价格证明，不能把过期报价交给模型或后续工具"
	case lightcopy.ReasonUnauthorized:
		return "不能把联系人全文、内部销售备注、手机号列表或未授权字段交给后续工具"
	case lightcopy.ReasonUnconfirmed:
		return "只允许传递已确认的最小事实，未确认事实请先核实或标记待澄清"
	case lightcopy.ReasonPIIDetected:
		return "检测到手机号列表、邮箱或内部备注等隐私信息，已拒绝进入生产简报"
	case lightcopy.ReasonAssetUnauthorized:
		return "素材缺少合法授权引用，已拒绝进入生产工具"
	case lightcopy.ReasonAssetExpired:
		return "所选素材授权已过期，已拒绝进入生产工具"
	case lightcopy.ReasonMissing:
		return "所选事实不在当前商机或活动上"
	default:
		return "没有选中可交出的事实"
	}
}

func handoffFingerprint(tool, sourceKind, sourceID, payerRef, permissionRef string, decline bool) string {
	raw := strings.Join([]string{tool, sourceKind, sourceID, payerRef, permissionRef, strconv.FormatBool(decline)}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}

func (s *Server) handleLightHandoff(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !validLightKind(kind) {
		fail(w, http.StatusBadRequest, "bad_request", "kind must be reply, email, or marketing_brief")
		return
	}
	var in struct {
		Tool             string                      `json:"tool"`
		Key              string                      `json:"idempotency_key"`
		Decline          bool                        `json:"decline"`
		Selected         []string                    `json:"selected"`
		Facts            []lightcopy.ConfirmedFact   `json:"facts"`
		AuthorizedAssets []lightcopy.AuthorizedAsset `json:"authorized_assets"`
		PermissionRef    string                      `json:"permission_ref"`
		PayerRef         string                      `json:"payer_ref"`
		QuoteExpiresAt   string                      `json:"quote_expires_at"`
		StripPII         bool                        `json:"strip_pii"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	in.Key = strings.TrimSpace(in.Key)
	if in.Key == "" || len(in.Key) > 128 {
		fail(w, http.StatusBadRequest, "bad_request", "idempotency_key is required")
		return
	}
	canonicalTool := strings.TrimSpace(in.Tool)
	if !in.Decline {
		norm, ok := lightcopy.NormalizeTool(in.Tool)
		if !ok {
			fail(w, http.StatusBadRequest, lightcopy.ReasonUnknownTool, "只能打开已有的专业工具入口（GoBoost/蜂群/数字人/商品图/AICut），不能在 CRM 里新建视频、产品图或数字人引擎")
			return
		}
		canonicalTool = norm
	} else if norm, ok := lightcopy.NormalizeTool(in.Tool); ok {
		canonicalTool = norm
	}

	in.PermissionRef = strings.TrimSpace(in.PermissionRef)
	in.PayerRef = strings.TrimSpace(in.PayerRef)
	in.QuoteExpiresAt = strings.TrimSpace(in.QuoteExpiresAt)
	if in.PermissionRef != "" && !lightcopy.ValidateSafeRef(in.PermissionRef) {
		fail(w, http.StatusBadRequest, lightcopy.ReasonPIIDetected, "permission_ref 非法或包含敏感信息")
		return
	}
	if in.PayerRef != "" && !lightcopy.ValidateSafeRef(in.PayerRef) {
		fail(w, http.StatusBadRequest, lightcopy.ReasonPIIDetected, "payer_ref 非法或包含敏感信息")
		return
	}
	nowUTC := time.Now().UTC()
	if in.QuoteExpiresAt != "" {
		exp, err := time.Parse(time.RFC3339, in.QuoteExpiresAt)
		if err != nil || !exp.After(nowUTC) {
			fail(w, http.StatusBadRequest, lightcopy.ReasonQuoteExpired, "报价已过期或格式非法，已拒绝交接")
			return
		}
	}

	// Revocation check runs fail-closed before any handoff replay/dispatch.
	revoked, err := s.St.IsLightSubjectRevoked(sub.TenantID, sub.Kind, sub.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revocation lookup failed")
		return
	}
	if revoked && !in.Decline {
		fail(w, http.StatusForbidden, lightcopy.ReasonPermissionRevoked, "该客户任务的专业交接权限已撤销，已拒绝交接；已保存草稿仍可手改。")
		return
	}

	c := callerFrom(r)
	fp := handoffFingerprint(canonicalTool, sub.Kind, sub.ID, in.PayerRef, in.PermissionRef, in.Decline)

	// Serialize concurrent identical-key clicks so downstream Dispatch is called
	// at most once and never creates duplicate projects or duplicate charges.
	unlock := s.lockLightHandoffKey(sub.TenantID, in.Key)
	defer unlock()

	if existingRow, err := s.St.GetLightHandoff(sub.TenantID, in.Key); err == nil {
		if existingRow.RevokedAt != "" || existingRow.Status == lightcopy.StatusRevoked {
			fail(w, http.StatusForbidden, lightcopy.ReasonPermissionRevoked, "该交接权限已撤销，拒绝重放。")
			return
		}
		if existingRow.RequestFingerprint != "" && existingRow.RequestFingerprint != fp {
			fail(w, http.StatusConflict, lightcopy.ReasonIdempotencyConflict, "同幂等键参数与既有交接不一致，已拒绝重复建工程。")
			return
		}
		existingRow.Regenerated = false
		existingRow.ProjectCreated = false
		writeJSON(w, http.StatusOK, existingRow)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "handoff lookup failed")
		return
	}

	draft, err := s.St.GetLightDraft(sub.TenantID, sub.Kind, sub.ID, kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusInternalServerError, "internal", "draft lookup failed")
		return
	}

	var (
		facts  map[string]any
		assets []lightcopy.AuthorizedAsset
	)
	if !in.Decline {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusBadRequest, "draft_missing", "先保存草稿，再交接。拒绝专业工具时仍可只保存草稿。")
			return
		}
		brief, reason := lightcopy.ProjectBrief(lightcopy.BriefInput{
			CallerTenant:        c.Member.TenantID,
			Subject:             sub,
			Selected:            in.Selected,
			Facts:               in.Facts,
			AuthorizedAssets:    in.AuthorizedAssets,
			AllowUnknownMarking: false,
			StripPII:            in.StripPII,
		}, nowUTC)
		if reason != "" {
			fail(w, http.StatusBadRequest, reason, factMessage(reason))
			return
		}
		facts = brief.Facts
		assets = brief.AuthorizedAssets
	}

	var (
		status           string
		reason           string
		targetProjectRef string
		externalTaskRef  string
	)
	if in.Decline || s.LightCopyToolDispatcher == nil {
		decision := lightcopy.OpenHandoff(lightcopy.HandoffInput{
			Key: in.Key, Tool: canonicalTool, Decline: in.Decline, EntryConfigured: false,
			SourceKind: sub.Kind, SourceID: sub.ID, DraftID: draft.ID,
			ContentVersion: draft.ContentVersion, Facts: facts,
		})
		if decision.Reason == lightcopy.ReasonUnknownTool {
			fail(w, http.StatusBadRequest, decision.Reason, "只能打开已有的专业工具入口，不能在 CRM 里新建视频、产品图或数字人引擎")
			return
		}
		status = decision.Status
		reason = decision.Reason
	} else {
		dispatchRes := s.LightCopyToolDispatcher.Dispatch(r.Context(), lightcopy.HandoffDispatchRequest{
			TenantID:         sub.TenantID,
			IdempotencyKey:   in.Key,
			Tool:             canonicalTool,
			SourceKind:       sub.Kind,
			SourceID:         sub.ID,
			DraftID:          draft.ID,
			ContentVersion:   draft.ContentVersion,
			Facts:            facts,
			AuthorizedAssets: assets,
			PermissionRef:    in.PermissionRef,
			PayerRef:         in.PayerRef,
			QuoteExpiresAt:   in.QuoteExpiresAt,
		})
		status = dispatchRes.Status
		reason = dispatchRes.Reason
		targetProjectRef = strings.TrimSpace(dispatchRes.TargetProjectRef)
		externalTaskRef = strings.TrimSpace(dispatchRes.ExternalTaskRef)
		switch status {
		case lightcopy.StatusRecorded:
			if reason == "" {
				reason = lightcopy.ReasonOpenExisting
			}
		case lightcopy.StatusUnknown:
			if reason == "" {
				reason = lightcopy.ReasonHandoffUnknown
			}
		default:
			status = lightcopy.StatusFailed
			if reason == "" {
				reason = lightcopy.ReasonToolEntry
			}
		}
	}

	saved, err := s.St.InsertLightHandoff(store.LightHandoff{
		TenantID:           sub.TenantID,
		Key:                in.Key,
		DraftID:            draft.ID,
		Tool:               canonicalTool,
		SourceKind:         sub.Kind,
		SourceID:           sub.ID,
		ContentVersion:     draft.ContentVersion,
		Facts:              facts,
		AuthorizedAssets:   assets,
		PermissionRef:      in.PermissionRef,
		PayerRef:           in.PayerRef,
		QuoteExpiresAt:     in.QuoteExpiresAt,
		TargetProjectRef:   targetProjectRef,
		ExternalTaskRef:    externalTaskRef,
		RequestFingerprint: fp,
		Status:             status,
		Reason:             reason,
	})
	if errors.Is(err, store.ErrLightHandoffConflict) {
		fail(w, http.StatusConflict, lightcopy.ReasonIdempotencyConflict, "同幂等键参数与既有交接不一致，已拒绝重复建工程。")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff save failed")
		return
	}
	saved.Regenerated = false
	saved.ProjectCreated = false
	s.Log.Printf("light copy handoff tenant=%s subject=%s tool=%s status=%s", sub.TenantID, sub.ID, saved.Tool, saved.Status)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleLightHandoffList(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionReadRecord)
	if !ok {
		return
	}
	handoffs, err := s.St.ListLightHandoffs(sub.TenantID, sub.Kind, sub.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff list failed")
		return
	}
	type handoffWithReturns struct {
		store.LightHandoff
		Returns []store.LightHandoffReturn `json:"returns"`
	}
	out := make([]handoffWithReturns, 0, len(handoffs))
	for _, h := range handoffs {
		rets, err := s.St.ListLightHandoffReturns(sub.TenantID, h.ID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "handoff returns list failed")
			return
		}
		if rets == nil {
			rets = []store.LightHandoffReturn{}
		}
		out = append(out, handoffWithReturns{LightHandoff: h, Returns: rets})
	}
	revoked, _ := s.St.IsLightSubjectRevoked(sub.TenantID, sub.Kind, sub.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"items":   out,
		"revoked": revoked,
	})
}

func (s *Server) handleLightSubjectRevoke(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 4<<10))
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &in); err != nil {
				fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
				return
			}
		}
	}
	if err := s.St.RevokeLightSubject(sub.TenantID, sub.Kind, sub.ID, callerFrom(r).Member.ID, strings.TrimSpace(in.Reason)); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revoke failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subject_kind": sub.Kind,
		"subject_id":   sub.ID,
		"revoked":      true,
		"drafts_kept":  true,
	})
}

func (s *Server) handleLightHandoffRevoke(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	handoffID := r.PathValue("handoffId")
	h, err := s.St.GetLightHandoffByID(sub.TenantID, handoffID)
	if errors.Is(err, sql.ErrNoRows) || h.SourceKind != sub.Kind || h.SourceID != sub.ID {
		fail(w, http.StatusNotFound, "not_found", "handoff not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff lookup failed")
		return
	}
	revoked, err := s.St.RevokeLightHandoff(sub.TenantID, handoffID, lightcopy.ReasonPermissionRevoked)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff revoke failed")
		return
	}
	writeJSON(w, http.StatusOK, revoked)
}

func (s *Server) handleLightHandoffRecover(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	handoffID := r.PathValue("handoffId")
	h, err := s.St.GetLightHandoffByID(sub.TenantID, handoffID)
	if errors.Is(err, sql.ErrNoRows) || h.SourceKind != sub.Kind || h.SourceID != sub.ID {
		fail(w, http.StatusNotFound, "not_found", "handoff not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff lookup failed")
		return
	}
	subjectRevoked, err := s.St.IsLightSubjectRevoked(sub.TenantID, sub.Kind, sub.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revocation lookup failed")
		return
	}
	if subjectRevoked || h.RevokedAt != "" || h.Status == lightcopy.StatusRevoked {
		fail(w, http.StatusForbidden, lightcopy.ReasonPermissionRevoked, "交接权限已撤销，拒绝恢复。")
		return
	}
	if h.QuoteExpiresAt != "" {
		if exp, parseErr := time.Parse(time.RFC3339, h.QuoteExpiresAt); parseErr != nil || !exp.After(time.Now().UTC()) {
			fail(w, http.StatusConflict, lightcopy.ReasonQuoteExpired, "交接报价已过期，拒绝恢复。")
			return
		}
	}
	if h.Status != lightcopy.StatusUnknown {
		h.Regenerated = false
		h.ProjectCreated = false
		writeJSON(w, http.StatusOK, h)
		return
	}
	if s.LightCopyToolDispatcher == nil {
		writeJSON(w, http.StatusOK, h)
		return
	}
	// Read-only status query by original idempotency key: never dispatches a
	// second creation or duplicate charge.
	res := s.LightCopyToolDispatcher.QueryStatus(r.Context(), sub.TenantID, h.Key, h.Tool)
	nextStatus := res.Status
	nextReason := res.Reason
	switch nextStatus {
	case lightcopy.StatusRecorded:
		if nextReason == "" {
			nextReason = lightcopy.ReasonOpenExisting
		}
	case lightcopy.StatusFailed:
		if nextReason == "" {
			nextReason = lightcopy.ReasonToolEntry
		}
	default:
		nextStatus = lightcopy.StatusUnknown
		if nextReason == "" {
			nextReason = lightcopy.ReasonHandoffUnknown
		}
	}
	updated, err := s.St.UpdateLightHandoffRecovery(sub.TenantID, h.ID, nextStatus, nextReason, strings.TrimSpace(res.TargetProjectRef), strings.TrimSpace(res.ExternalTaskRef))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff recovery update failed")
		return
	}
	updated.Regenerated = false
	updated.ProjectCreated = false
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleLightHandoffReturn(w http.ResponseWriter, r *http.Request, subjectKind string) {
	sub, ok := s.lightSubject(w, r, subjectKind, authz.ActionUpdate)
	if !ok {
		return
	}
	handoffID := r.PathValue("handoffId")
	h, err := s.St.GetLightHandoffByID(sub.TenantID, handoffID)
	if errors.Is(err, sql.ErrNoRows) || h.SourceKind != sub.Kind || h.SourceID != sub.ID {
		fail(w, http.StatusNotFound, "not_found", "handoff not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "handoff lookup failed")
		return
	}

	// Fail-closed: revoked subject/handoff or expired quote cannot accept backflow.
	subjectRevoked, err := s.St.IsLightSubjectRevoked(sub.TenantID, sub.Kind, sub.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "revocation lookup failed")
		return
	}
	if subjectRevoked || h.RevokedAt != "" || h.Status == lightcopy.StatusRevoked {
		fail(w, http.StatusForbidden, lightcopy.ReasonPermissionRevoked, "授权已撤销，拒绝接收产物回流。")
		return
	}
	if h.QuoteExpiresAt != "" {
		if exp, parseErr := time.Parse(time.RFC3339, h.QuoteExpiresAt); parseErr != nil || !exp.After(time.Now().UTC()) {
			fail(w, http.StatusConflict, lightcopy.ReasonQuoteExpired, "原交接报价已过期，拒绝接收产物回流。")
			return
		}
	}
	if h.Status != lightcopy.StatusRecorded {
		fail(w, http.StatusConflict, lightcopy.ReasonHandoffNotActive, "当前交接未处于有效已记录状态，不能接收回流。")
		return
	}

	// Strict JSON decode rejects unknown/PII keys before anything is stored.
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<10))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var in lightcopy.ReturnCandidateInput
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, lightcopy.ReasonUnauthorized, "回流载荷包含未授权字段或格式非法")
		return
	}
	clean, reason := lightcopy.ValidateReturnCandidate(in)
	if reason != "" {
		fail(w, http.StatusBadRequest, reason, "回流结果仅允许受控 asset/task 引用与无隐私摘要")
		return
	}
	contentVer := clean.ContentVersion
	if contentVer <= 0 {
		contentVer = h.ContentVersion
	}
	stored, replayed, err := s.St.InsertLightHandoffReturn(store.LightHandoffReturn{
		TenantID:       sub.TenantID,
		HandoffID:      h.ID,
		Key:            clean.IdempotencyKey,
		SourceKind:     sub.Kind,
		SourceID:       sub.ID,
		Tool:           h.Tool,
		AssetRef:       clean.AssetRef,
		TaskRef:        clean.TaskRef,
		CandidateKind:  clean.CandidateKind,
		Summary:        clean.Summary,
		ContentVersion: contentVer,
		PermissionRef:  h.PermissionRef,
		PayerRef:       h.PayerRef,
		Status:         "candidate",
		CreatedBy:      callerFrom(r).Member.ID,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "return candidate save failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"candidate":         stored,
		"idempotent_replay": replayed,
		"draft_overwritten": false,
	})
}

func draftToRule(d store.LightDraft) lightcopy.Draft {
	return lightcopy.Draft{
		Body: d.Body, Origin: d.Origin, ContentVersion: d.ContentVersion,
		Generated: d.Generated, Sent: d.Sent, Published: d.Published, MarketingPermitted: d.MarketingPermitted,
		UserConfirmed: d.UserConfirmed, LiveCharge: d.LiveCharge,
	}
}

func ruleToDraft(next lightcopy.Draft, current store.LightDraft, sub lightcopy.Subject, kind, memberID string) store.LightDraft {
	return store.LightDraft{
		ID: current.ID, TenantID: sub.TenantID, SubjectKind: sub.Kind, SubjectID: sub.ID, Kind: kind,
		Body: next.Body, Origin: next.Origin, ContentVersion: next.ContentVersion,
		Generated: next.Generated, UserConfirmed: next.UserConfirmed, LiveCharge: next.LiveCharge,
		InputFacts: current.InputFacts, InputFactsHash: current.InputFactsHash,
		UnknownFields: current.UnknownFields, TaskID: current.TaskID,
		BillingVerdict: current.BillingVerdict, UsageRef: current.UsageRef,
		SampleCategory: current.SampleCategory,
		UpdatedBy: memberID, CreatedAt: current.CreatedAt,
	}
}
