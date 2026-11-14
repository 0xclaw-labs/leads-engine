package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/bianjiefilm/leads-engine/server/internal/authz"
	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/notifyingest"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type touchFollowUpInput struct {
	SourceTenant string `json:"source_tenant_id"`
	SourceRef    string `json:"source_ref"`
	Revision     int    `json:"source_revision"`
	Principal    string `json:"principal_id"`
	Handoff      string `json:"handoff_id,omitempty"`
	StoreID      string `json:"origin_store_id,omitempty"`
}

// The existing follow-up service credential is additionally bound to the
// exact path/body. A browser BFF injecting X-Internal-Token cannot assert a
// principal or register a navigation context by itself.
func (s *Server) touchFollowUpServiceBody(w http.ResponseWriter, r *http.Request) (touchFollowUpInput, bool) {
	var in touchFollowUpInput
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if err != nil || len(raw) > 8192 {
		fail(w, 400, "bad_request", "invalid follow-up request")
		return in, false
	}
	mac := hmac.New(sha256.New, []byte(s.Cfg.InternalToken))
	mac.Write([]byte("touch-follow-up/v1\n" + r.URL.Path + "\n"))
	mac.Write(raw)
	got, err := hex.DecodeString(r.Header.Get("X-Touch-Follow-Up-Signature"))
	if err != nil || !hmac.Equal(got, mac.Sum(nil)) {
		fail(w, 401, "unauthorized", "trusted follow-up service proof required")
		return in, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.SourceTenant == "" || in.SourceRef == "" || in.Principal == "" || in.Revision < 1 {
		fail(w, 400, "bad_request", "complete source identity required")
		return in, false
	}
	return in, true
}

func (s *Server) touchFollowUpRecord(w http.ResponseWriter, r *http.Request, in touchFollowUpInput) (store.Lead, bool) {
	lead, err := s.St.TouchFollowUpLead(in.SourceTenant, in.SourceRef, in.Revision)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		fail(w, 404, "not_found", "original source is not stored")
		return lead, false
	case errors.Is(err, store.ErrFollowUpRevoked), errors.Is(err, store.ErrAmbiguousNotify), errors.Is(err, store.ErrNotifySourceScopeConflict):
		fail(w, 409, "follow_up_unavailable", "source permission or unique mapping cannot be verified")
		return lead, false
	case err != nil:
		fail(w, 503, "follow_up_unavailable", "original mapping could not be read")
		return lead, false
	}
	member, err := s.St.GetMemberByPrincipal(lead.TenantID, in.Principal)
	if err != nil {
		fail(w, 403, "not_member", "no CRM membership for this record")
		return lead, false
	}
	c := &caller{Member: &member}
	if authz.Role(member.Role) == authz.RoleAgent {
		c.Grant, _ = s.St.GetAgentGrant(lead.TenantID, in.Principal)
	}
	if !s.requireAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: lead.TenantID, AssigneeMemberID: lead.AssignedMemberID}, w) {
		return lead, false
	}
	// Notify can lag a source withdrawal. Re-read the existing receiver seam
	// without advancing, clearing or repairing any consent/watermark.
	var src config.IngestSource
	for _, candidate := range s.Cfg.IngestSources {
		if candidate.AppID == "touch-engine" {
			src = candidate
		}
	}
	rec, err := fetchIngestRecord(r, src, in.SourceTenant, in.SourceRef)
	if err != nil {
		fail(w, 503, "source_unavailable", "source permission could not be rechecked")
		return lead, false
	}
	if rec.SubmissionRef != in.SourceRef || rec.TenantID != in.SourceTenant || rec.Tombstone || rec.Authorization != "granted" || !rec.Consents.FormAuthorized || notifyingest.ValidateTouchRecord(rec, in.Revision) != nil {
		fail(w, 409, "source_revoked", "source no longer permits follow-up")
		return lead, false
	}
	return lead, true
}

func (s *Server) handleTouchFollowUpResolve(w http.ResponseWriter, r *http.Request) {
	in, ok := s.touchFollowUpServiceBody(w, r)
	if !ok {
		return
	}
	lead, ok := s.touchFollowUpRecord(w, r, in)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"crm_tenant_id": lead.TenantID, "lead_id": lead.ID})
}

var followUpHandoff = regexp.MustCompile(`^hnd_[a-f0-9]{32}$`)

func (s *Server) handleTouchFollowUpRegister(w http.ResponseWriter, r *http.Request) {
	in, ok := s.touchFollowUpServiceBody(w, r)
	if !ok {
		return
	}
	if !followUpHandoff.MatchString(in.Handoff) || in.StoreID == "" {
		fail(w, 400, "bad_request", "shell handoff and original store required")
		return
	}
	lead, ok := s.touchFollowUpRecord(w, r, in)
	if !ok {
		return
	}
	n := store.FollowUpNavigation{HandoffID: in.Handoff, Kind: "forward", PrincipalID: in.Principal, SourceTenantID: in.SourceTenant, SourceRef: in.SourceRef, SourceRevision: in.Revision, StoreID: in.StoreID, CRMTenantID: lead.TenantID, LeadID: lead.ID, VisitRef: store.NewFollowUpVisitRef(), ExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err := s.St.InsertFollowUpNavigation(n); err != nil {
		fail(w, 409, "handoff_conflict", "handoff already registered")
		return
	}
	writeJSON(w, 201, map[string]any{"registered": true})
}

// The server selects the mapped CRM tenant; the browser cannot substitute one.
// The existing session middleware still resolves the real platform principal.
func (s *Server) handleTouchFollowUpResume(w http.ResponseWriter, r *http.Request) {
	n, err := s.St.GetFollowUpNavigation(r.PathValue("h"))
	if err != nil || n.Kind != "forward" {
		writeJSON(w, 200, map[string]any{"known": false})
		return
	}
	r.Header.Set(tenantHeader, n.CRMTenantID)
	s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		c := callerFrom(r)
		if c.Principal.ID != n.PrincipalID {
			fail(w, 404, "not_found", "no navigation for this principal")
			return
		}
		if n.UsedAt != 0 || n.ExpiresAt <= time.Now().Unix() {
			fail(w, 410, "handoff_expired", "请回碰一碰线索列表重新进入")
			return
		}
		lead, ok := s.touchFollowUpRecord(w, r, touchFollowUpInput{SourceTenant: n.SourceTenantID, SourceRef: n.SourceRef, Revision: n.SourceRevision, Principal: c.Principal.ID})
		if !ok {
			return
		}
		if lead.ID != n.LeadID || lead.TenantID != n.CRMTenantID {
			fail(w, 409, "source_changed", "original source mapping changed")
			return
		}
		if s.St.ConsumeFollowUpNavigation(n.HandoffID, c.Principal.ID, time.Now()) != nil {
			fail(w, 410, "handoff_expired", "handoff already used")
			return
		}
		writeJSON(w, 200, map[string]any{"known": true, "crm_tenant_id": lead.TenantID, "path": "/leads/" + url.PathEscape(lead.ID) + "?touch_follow_up=" + url.QueryEscape(n.VisitRef)})
	}).ServeHTTP(w, r)
}

func (s *Server) handleTouchFollowUpReturn(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	n, err := s.St.GetFollowUpVisit(r.PathValue("visit"))
	if err != nil || n.PrincipalID != c.Principal.ID || n.CRMTenantID != c.Member.TenantID {
		fail(w, 404, "not_found", "return context not found")
		return
	}
	if n.UsedAt+1800 <= time.Now().Unix() {
		fail(w, 410, "return_expired", "返回上下文已过期，请从原应用重新进入")
		return
	}
	lead, err := s.St.GetLead(n.LeadID, n.CRMTenantID)
	if err != nil {
		fail(w, 404, "not_found", "record not found")
		return
	}
	if !s.requireAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: lead.TenantID, AssigneeMemberID: lead.AssignedMemberID}, w) {
		return
	}
	launch, err := s.ID.LaunchShell(r.Context(), c.Principal.ID, n.SourceTenantID, "touch-engine", "ti-touch-engine-web")
	if err != nil {
		fail(w, 503, "return_target_unavailable", "原店返回入口未获平台登记或当前不可用，请稍后重试；线索详情仍可查看")
		return
	}
	u, _ := url.Parse(launch)
	h := u.Query().Get("h")
	if !followUpHandoff.MatchString(h) {
		fail(w, 502, "invalid_shell_handoff", "shell returned no usable handoff")
		return
	}
	var src config.IngestSource
	for _, candidate := range s.Cfg.IngestSources {
		if candidate.AppID == "touch-engine" {
			src = candidate
		}
	}
	raw, _ := json.Marshal(map[string]string{"handoff_id": h, "principal_id": c.Principal.ID})
	req, err := http.NewRequestWithContext(r.Context(), "POST", strings.TrimRight(src.FetchBase, "/")+"/api/v1/internal/lead-follow-ups/"+url.PathEscape(n.HandoffID)+"/returns", bytes.NewReader(raw))
	if err != nil {
		fail(w, 503, "return_unavailable", "return service unavailable")
		return
	}
	req.Header.Set("X-Service-ID", src.FetchServiceID)
	req.Header.Set("X-Service-Token", src.FetchToken)
	req.Header.Set("X-Leads-Tenant", n.SourceTenantID)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		fail(w, 503, "return_unavailable", "原店返回服务暂时不可用，可重试")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 201 {
		status := res.StatusCode
		if status < 400 || status > 599 {
			status = 502
		}
		fail(w, status, "return_denied", "当前身份不能返回该门店，或返回上下文已失效")
		return
	}
	writeJSON(w, 200, map[string]string{"launch_url": launch})
}
