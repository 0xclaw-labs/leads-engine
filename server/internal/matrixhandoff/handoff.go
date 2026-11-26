// Package matrixhandoff records one Leads draft reference for an existing
// legal content asset. The matrix is a fixture client in this process.
//
// 线索、会话和跟进仍属于 Leads。本包不发布、不扣费、不启动回复机器人，也不写线索。
// 夹具成功不是发布成功，也不是白标经营链完成。
package matrixhandoff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	schemaV1 = "matrix-handoff/v1"

	statusDraft = "draft_recorded"
	copyDraft   = "已记下草稿引用"

	statusUnavailable = "unavailable"
	statusNotIngested = "not_ingested"

	kindResult   = "result"
	kindRevoke   = "revoke"
	kindReplay   = "replay"
	kindEdit     = "edit"
	kindWithdraw = "withdraw"
)

var (
	ErrForbidden   = errors.New("matrixhandoff: forbidden field")
	ErrCrossTenant = errors.New("matrixhandoff: cross-tenant actor")
	ErrNoContent   = errors.New("matrixhandoff: no content selected")
	ErrNotLegal    = errors.New("matrixhandoff: asset is not an existing legal asset")
	ErrClient      = errors.New("matrixhandoff: matrix client")
	ErrEventID     = errors.New("matrixhandoff: provider event id required")
	ErrEventKind   = errors.New("matrixhandoff: unknown event kind")
	ErrNotFound    = errors.New("matrixhandoff: draft not found")
)

// SourceIDs are lead, session, and follow-up ids. They are not a profile.
type SourceIDs struct {
	LeadID     string `json:"lead_id"`
	SessionID  string `json:"session_id"`
	FollowUpID string `json:"follow_up_id"`
}

// Selection is one existing legal asset inside one tenant.
// Phone, email, CRM profile, customer list, and message body are rejected.
// IntentScore, SalesRole, and ProductionAuthorized are not publish authority.
type Selection struct {
	ActorTenantID        string
	TenantID             string
	CampaignID           string
	AssetID              string
	ContentVersion       int
	ContentType          string
	LicenseExpiresAt     *time.Time
	BrandID              string
	MemberID             string
	CommercialDisclosure string
	UsageRestriction     string
	Purpose              string
	Legal                bool
	Source               SourceIDs
	Phone                string
	Email                string
	CRMProfile           string
	CRMNotes             string
	CustomerList         []string
	MessageBody          string
	IntentScore          int
	SalesRole            string
	StartReplyBot        bool
	WantCharge           bool
	ProductionAuthorized bool
}

// Draft is the stored reference. Live receipt and publish flags stay false.
type Draft struct {
	ID                   string
	TenantID             string
	CampaignID           string
	AssetID              string
	ContentVersion       int
	BrandID              string
	MemberID             string
	CommercialDisclosure string
	UsageRestriction     string
	Purpose              string
	Source               SourceIDs
	ReturnRef            string
	Status               string
	PublishStage         string
	Copy                 string
	FixtureAccepted      bool
	LiveReceipt          bool
	ChainComplete        bool
	PublishApproved      bool
	PublishExecuted      bool
	UGCRewardTriggered   bool
	ConversionROI        *float64
	MatrixPlan           string
	MatrixItem           string
	ExternalPost         string
	Reach                *int
	Likes                *int
	Leads                *int
	LastSourceVersion    int
}

// Event is a later fixture report or a permission event.
// Outcome and RestorePublish are ignored. They cannot publish or restore a grant.
type Event struct {
	ProviderEventID string
	ActorTenantID   string
	TenantID        string
	DraftID         string
	Kind            string
	SourceVersion   int
	Revision        int
	PublishStage    string
	MatrixPlan      string
	MatrixItem      string
	ExternalPost    string
	Phone           string
	Email           string
	CRMProfile      string
	CRMNotes        string
	CustomerList    []string
	MessageBody     string
	Outcome         string
	Reach           *int
	Likes           *int
	Leads           *int
	FixtureSuccess  bool
	RestorePublish  bool
}

// EventResult reports what the event did not do.
type EventResult struct {
	Idempotent         bool
	StaleIgnored       bool
	CreatedDraft       bool
	CreatedLead        bool
	PublishPermitted   bool
	PublishExecuted    bool
	LiveReceipt        bool
	ChainComplete      bool
	UGCRewardTriggered bool
}

// JumpResult is a top-nav or a selected-asset open. PlanCreated stays false.
type JumpResult struct {
	DraftCreated bool
	PlanCreated  bool
	Draft        *Draft
}

// Effects are writes caused by this package. Charged stays null.
type Effects struct {
	LeadWrites     int  `json:"lead_writes"`
	ReplyBotStarts int  `json:"reply_bot_starts"`
	ChargeWrites   int  `json:"charge_writes"`
	Charged        *int `json:"charged"`
	BillingPassed  bool `json:"billing_passed"`
}

// LeadPath is an existing Leads row. This package does not write it.
type LeadPath struct {
	Usable     bool
	LeadID     string
	SessionID  string
	FollowUpID string
}

// Notice is the notify-shaped output. Ids only.
type Notice struct {
	DraftID   string `json:"draft_id"`
	TenantID  string `json:"tenant_id"`
	AssetID   string `json:"asset_id"`
	Version   int    `json:"version"`
	ReturnRef string `json:"return_ref"`
}

// Labels are the evidence this build is allowed to report.
type Labels struct {
	ServiceProvider string
	Billing         string
	Production      string
	HumanAdoption   string
	Browser         string
}

// Channel is one social channel. A DM grant is not ingestion.
type Channel struct {
	ID      string
	DMGrant bool
}

// ChannelAssessment never reports success for direct messages.
type ChannelAssessment struct {
	ID         string
	Status     string
	Success    bool
	DMIngested bool
}

// Service stores draft references in memory.
type Service struct {
	mu              sync.RWMutex
	client          Client
	byKey           map[string]string
	byID            map[string]Draft
	order           []string
	events          map[string]struct{}
	revoked         map[string]bool
	allow           map[string]bool
	logs            []string
	leadWrites      int
	replyStarts     int
	chargeWrites    int
	ugcRewardWrites int
	charged         *int
	billingPassed   bool
	leadPathUsable  bool
	nowFn           func() time.Time
}

// New returns an empty handoff. A nil client fails new drafts and leaves the lead path usable.
func New(c Client) *Service {
	return &Service{
		client:         c,
		byKey:          map[string]string{},
		byID:           map[string]Draft{},
		events:         map[string]struct{}{},
		revoked:        map[string]bool{},
		allow:          map[string]bool{},
		leadPathUsable: true,
		nowFn:          func() time.Time { return time.Now().UTC() },
	}
}

// Evidence reports the five labels for this build. It never returns PASS.
func Evidence() Labels {
	return Labels{
		ServiceProvider: "NOT_VERIFIED",
		Billing:         "NOT_VERIFIED",
		Production:      "NOT_AUTHORIZED",
		HumanAdoption:   "UNKNOWN",
		Browser:         "NOT_RUN",
	}
}

// ClaimsEightPlatformDM is always false. This package does not ingest DMs.
func ClaimsEightPlatformDM() bool { return false }

// AssessChannel marks a channel without a DM grant unavailable.
// A grant is still not ingestion and not success.
func AssessChannel(ch Channel) ChannelAssessment {
	out := ChannelAssessment{ID: ch.ID, Success: false, DMIngested: false}
	if !ch.DMGrant {
		out.Status = statusUnavailable
		return out
	}
	out.Status = statusNotIngested
	return out
}

// Select stores one draft reference for a legal asset. The same key returns the
// stored draft and does not call the fixture again.
func (s *Service) Select(in Selection) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if s.nowFn != nil {
		now = s.nowFn()
	}
	if err := validateSelection(in, now); err != nil {
		return Draft{}, err
	}
	key := selectionKey(in)
	if id, ok := s.byKey[key]; ok {
		return cloneDraft(s.byID[id]), nil
	}
	if s.client == nil {
		return Draft{}, ErrClient
	}
	id := draftID(key)
	draft := Draft{
		ID:                   id,
		TenantID:             in.TenantID,
		CampaignID:           in.CampaignID,
		AssetID:              in.AssetID,
		ContentVersion:       in.ContentVersion,
		BrandID:              in.BrandID,
		MemberID:             in.MemberID,
		CommercialDisclosure: in.CommercialDisclosure,
		UsageRestriction:     in.UsageRestriction,
		Purpose:              in.Purpose,
		Source:               in.Source,
		ReturnRef:            "ret_" + id,
		Status:               statusDraft,
		PublishStage:         StageDraftSaved,
		Copy:                 copyDraft,
		UGCRewardTriggered:   false,
		ConversionROI:        nil,
	}
	ack, err := s.client.SubmitDraft(Payload{
		SourceIDs: draft.Source,
		AssetID:   draft.AssetID,
		Version:   draft.ContentVersion,
		ReturnRef: draft.ReturnRef,
	})
	if err != nil {
		return Draft{}, fmt.Errorf("%w: %v", ErrClient, err)
	}
	// Published and Outcome are not stored and do not approve a publish.
	_ = ack.Published
	_ = ack.Outcome
	draft.FixtureAccepted = ack.Accepted
	s.byKey[key] = id
	s.byID[id] = draft
	s.order = append(s.order, id)
	s.logs = append(s.logs, auditLine(draft))
	return cloneDraft(draft), nil
}

// Jump opens a selected asset, or does nothing when no asset was selected.
// It does not create a marketing plan.
func (s *Service) Jump(sel *Selection) (JumpResult, error) {
	if sel == nil {
		return JumpResult{}, nil
	}
	if err := forbiddenText(sel.Phone, sel.Email, sel.CRMProfile, sel.CRMNotes, sel.MessageBody, sel.CustomerList); err != nil {
		return JumpResult{}, err
	}
	if strings.TrimSpace(sel.AssetID) == "" {
		return JumpResult{}, nil
	}
	s.mu.RLock()
	before := len(s.order)
	s.mu.RUnlock()
	draft, err := s.Select(*sel)
	if err != nil {
		return JumpResult{}, err
	}
	s.mu.RLock()
	created := len(s.order) > before
	s.mu.RUnlock()
	return JumpResult{DraftCreated: created, PlanCreated: false, Draft: &draft}, nil
}

// ApplyEvent stores restricted refs on an existing draft, or latches revoke.
// A repeated provider event id does not create a lead or a draft.
// Stale receipts or out-of-order older revisions never overwrite an adopted newer version.
func (s *Service) ApplyEvent(ev Event) (EventResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !token(ev.ProviderEventID) {
		return EventResult{}, ErrEventID
	}
	if err := forbiddenText(ev.Phone, ev.Email, ev.CRMProfile, ev.CRMNotes, ev.MessageBody, ev.CustomerList); err != nil {
		return EventResult{}, err
	}
	if err := ValidateMetrics(ev.Reach, ev.Likes, ev.Leads); err != nil {
		return EventResult{}, err
	}
	if err := checkRef(ev.MatrixPlan); err != nil {
		return EventResult{}, err
	}
	if err := checkRef(ev.MatrixItem); err != nil {
		return EventResult{}, err
	}
	if err := checkRef(ev.ExternalPost); err != nil {
		return EventResult{}, err
	}
	switch ev.Kind {
	case kindResult, kindRevoke, kindReplay, kindEdit, kindWithdraw:
	default:
		return EventResult{}, ErrEventKind
	}
	if !token(ev.ActorTenantID) || !token(ev.TenantID) || ev.ActorTenantID != ev.TenantID {
		return EventResult{}, ErrCrossTenant
	}
	// Outcome and RestorePublish cannot publish or restore a revoked grant.
	_ = ev.Outcome
	_ = ev.RestorePublish
	if _, seen := s.events[ev.ProviderEventID]; seen {
		return s.eventResultLocked(ev.TenantID, true, false), nil
	}
	switch ev.Kind {
	case kindRevoke:
		s.revoked[ev.TenantID] = true
		s.allow[ev.TenantID] = false
	case kindResult:
		draft, ok := s.byID[ev.DraftID]
		if !ok {
			return EventResult{}, ErrNotFound
		}
		// Prevent stale receipt from overwriting an adopted newer version of the same campaign or same draft.
		if ev.Revision > 0 && ev.Revision < draft.ContentVersion {
			return EventResult{}, ErrStaleReceipt
		}
		if ev.SourceVersion > 0 && draft.LastSourceVersion > 0 && ev.SourceVersion < draft.LastSourceVersion {
			return EventResult{}, ErrStaleReceipt
		}
		if s.hasAdoptedNewerVersionLocked(draft.TenantID, draft.CampaignID, draft.ContentVersion) {
			return EventResult{}, ErrStaleReceipt
		}
		if ev.FixtureSuccess {
			draft.FixtureAccepted = true
		}
		if ev.PublishStage != "" {
			stage, _ := ClassifyPublishStage(ev.PublishStage, false)
			if CanAdvanceStage(draft.PublishStage, stage) {
				draft.PublishStage = stage
			}
		}
		if ev.MatrixPlan != "" {
			draft.MatrixPlan = ev.MatrixPlan
		}
		if ev.MatrixItem != "" {
			draft.MatrixItem = ev.MatrixItem
		}
		if ev.ExternalPost != "" {
			draft.ExternalPost = ev.ExternalPost
		}
		draft.Reach = copyMetric(ev.Reach, draft.Reach)
		draft.Likes = copyMetric(ev.Likes, draft.Likes)
		draft.Leads = copyMetric(ev.Leads, draft.Leads)
		if ev.SourceVersion > draft.LastSourceVersion {
			draft.LastSourceVersion = ev.SourceVersion
		}
		s.byID[ev.DraftID] = draft
	}
	s.events[ev.ProviderEventID] = struct{}{}
	return s.eventResultLocked(ev.TenantID, false, false), nil
}

func (s *Service) hasAdoptedNewerVersionLocked(tenantID, campaignID string, version int) bool {
	for _, d := range s.byID {
		if d.TenantID == tenantID && d.CampaignID == campaignID && d.ContentVersion > version {
			return true
		}
	}
	return false
}

func (s *Service) eventResultLocked(tenant string, idempotent, stale bool) EventResult {
	return EventResult{
		Idempotent:         idempotent,
		StaleIgnored:       stale,
		PublishPermitted:   s.allow[tenant],
		PublishExecuted:    false,
		LiveReceipt:        false,
		ChainComplete:      false,
		UGCRewardTriggered: false,
	}
}

// PublishPermitted is false unless a grant was stored. This package never stores one.
// Revoke clears the bit. Later events do not set it.
func (s *Service) PublishPermitted(tenant string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allow[tenant]
}

// PublishRevoked reports the revoke latch.
func (s *Service) PublishRevoked(tenant string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revoked[tenant]
}

// UGCRewardWrites reports customer UGC reward writes triggered by merchant publish (always 0).
func (s *Service) UGCRewardWrites() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ugcRewardWrites
}

// Effects returns the write counters. They stay at zero in this build.
func (s *Service) Effects() Effects {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Effects{
		LeadWrites:     s.leadWrites,
		ReplyBotStarts: s.replyStarts,
		ChargeWrites:   s.chargeWrites,
		Charged:        s.charged,
		BillingPassed:  s.billingPassed,
	}
}

// LeadPath reports that an existing lead, session, and follow-up stay usable.
func (s *Service) LeadPath(in LeadPath) LeadPath {
	s.mu.RLock()
	defer s.mu.RUnlock()
	in.Usable = s.leadPathUsable
	return in
}

// Drafts returns the stored references in insert order.
func (s *Service) Drafts() []Draft {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Draft, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, cloneDraft(s.byID[id]))
	}
	return out
}

// Document is the stored JSON. It has no message body.
func (s *Service) Document(id string) (string, error) {
	s.mu.RLock()
	draft, ok := s.byID[id]
	eff := Effects{
		LeadWrites:     s.leadWrites,
		ReplyBotStarts: s.replyStarts,
		ChargeWrites:   s.chargeWrites,
		Charged:        s.charged,
		BillingPassed:  s.billingPassed,
	}
	s.mu.RUnlock()
	if !ok {
		return "", ErrNotFound
	}
	ev := Evidence()
	stage := draft.PublishStage
	if stage == "" {
		stage, _ = ClassifyPublishStage(draft.Status, false)
	}
	raw, err := json.Marshal(storedDocument{
		Schema:             schemaV1,
		DraftID:            draft.ID,
		TenantID:           draft.TenantID,
		CampaignID:         draft.CampaignID,
		AssetID:            draft.AssetID,
		Version:            draft.ContentVersion,
		Purpose:            draft.Purpose,
		SourceIDs:          draft.Source,
		ReturnRef:          draft.ReturnRef,
		Status:             draft.Status,
		PublishStage:       stage,
		Copy:               draft.Copy,
		FixtureAccepted:    draft.FixtureAccepted,
		LiveReceipt:        draft.LiveReceipt,
		ChainComplete:      draft.ChainComplete,
		PublishApproved:    draft.PublishApproved,
		PublishExecuted:    draft.PublishExecuted,
		UGCRewardTriggered: false,
		ConversionROI:      nil,
		MatrixPlan:         draft.MatrixPlan,
		MatrixItem:         draft.MatrixItem,
		ExternalPost:       draft.ExternalPost,
		Reach:              draft.Reach,
		Likes:              draft.Likes,
		Leads:              draft.Leads,
		LeadWrites:         eff.LeadWrites,
		ReplyBotStarts:     eff.ReplyBotStarts,
		ChargeWrites:       eff.ChargeWrites,
		Charged:            eff.Charged,
		BillingPassed:      eff.BillingPassed,
		ServiceProvider:    ev.ServiceProvider,
		Billing:            ev.Billing,
		Production:         ev.Production,
		HumanAdoption:      ev.HumanAdoption,
		Browser:            ev.Browser,
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Notice returns ids only.
func (s *Service) Notice(id string) (Notice, error) {
	s.mu.RLock()
	draft, ok := s.byID[id]
	s.mu.RUnlock()
	if !ok {
		return Notice{}, ErrNotFound
	}
	return Notice{
		DraftID:   draft.ID,
		TenantID:  draft.TenantID,
		AssetID:   draft.AssetID,
		Version:   draft.ContentVersion,
		ReturnRef: draft.ReturnRef,
	}, nil
}

// Audit records an id-only line. messageBody is never copied.
func (s *Service) Audit(draftID, messageBody string) string {
	_ = messageBody
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, ok := s.byID[draftID]
	if !ok {
		return ""
	}
	line := auditLine(draft)
	s.logs = append(s.logs, line)
	return line
}

// Logs returns the id-only lines recorded so far.
func (s *Service) Logs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.logs))
	copy(out, s.logs)
	return out
}

type storedDocument struct {
	Schema             string    `json:"schema"`
	DraftID            string    `json:"draft_id"`
	TenantID           string    `json:"tenant_id"`
	CampaignID         string    `json:"campaign_id"`
	AssetID            string    `json:"asset_id"`
	Version            int       `json:"version"`
	Purpose            string    `json:"purpose"`
	SourceIDs          SourceIDs `json:"source_ids"`
	ReturnRef          string    `json:"return_ref"`
	Status             string    `json:"status"`
	PublishStage       string    `json:"publish_stage"`
	Copy               string    `json:"copy"`
	FixtureAccepted    bool      `json:"fixture_accepted"`
	LiveReceipt        bool      `json:"live_receipt"`
	ChainComplete      bool      `json:"chain_complete"`
	PublishApproved    bool      `json:"publish_approved"`
	PublishExecuted    bool      `json:"publish_executed"`
	UGCRewardTriggered bool      `json:"ugc_reward_triggered"`
	ConversionROI      *float64  `json:"conversion_roi"`
	MatrixPlan         string    `json:"matrix_plan"`
	MatrixItem         string    `json:"matrix_item"`
	ExternalPost       string    `json:"external_post"`
	Reach              *int      `json:"reach"`
	Likes              *int      `json:"likes"`
	Leads              *int      `json:"leads"`
	LeadWrites         int       `json:"lead_writes"`
	ReplyBotStarts     int       `json:"reply_bot_starts"`
	ChargeWrites       int       `json:"charge_writes"`
	Charged            *int      `json:"charged"`
	BillingPassed      bool      `json:"billing_passed"`
	ServiceProvider    string    `json:"service_provider"`
	Billing            string    `json:"billing"`
	Production         string    `json:"production"`
	HumanAdoption      string    `json:"human_adoption"`
	Browser            string    `json:"browser"`
}

func validateSelection(in Selection, now time.Time) error {
	if err := forbiddenText(in.Phone, in.Email, in.CRMProfile, in.CRMNotes, in.MessageBody, in.CustomerList); err != nil {
		return err
	}
	if err := ValidateContentType(in.ContentType); err != nil {
		return err
	}
	if err := ValidateLicenseExpiry(in.LicenseExpiresAt, now); err != nil {
		return err
	}
	if err := checkSource(in.Source); err != nil {
		return err
	}
	for _, decl := range []string{in.CommercialDisclosure, in.UsageRestriction} {
		if err := ValidateRestrictedDeclaration(decl); err != nil {
			return err
		}
	}
	if !token(in.ActorTenantID) || !token(in.TenantID) || in.ActorTenantID != in.TenantID {
		return ErrCrossTenant
	}
	if !in.Legal {
		return ErrNotLegal
	}
	if in.ContentVersion < 1 || !token(in.CampaignID) || !token(in.AssetID) || !token(in.Purpose) {
		return ErrNoContent
	}
	return nil
}

func forbiddenText(phone, email, profile, notes, body string, customers []string) error {
	if strings.TrimSpace(phone) != "" || strings.TrimSpace(email) != "" || strings.TrimSpace(profile) != "" || strings.TrimSpace(notes) != "" || strings.TrimSpace(body) != "" || len(customers) > 0 {
		return ErrForbidden
	}
	return nil
}

func checkSource(src SourceIDs) error {
	for _, id := range []string{src.LeadID, src.SessionID, src.FollowUpID} {
		if id == "" {
			continue
		}
		if !token(id) {
			return ErrForbidden
		}
	}
	return nil
}

func checkRef(s string) error {
	if s == "" || token(s) {
		return nil
	}
	return ErrForbidden
}

func selectionKey(in Selection) string {
	return strings.Join([]string{
		in.TenantID,
		in.CampaignID,
		in.AssetID,
		strconv.Itoa(in.ContentVersion),
		in.Purpose,
	}, "\x1f")
}

func draftID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "d_" + hex.EncodeToString(sum[:16])
}

func token(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func copyMetric(in, current *int) *int {
	if in == nil {
		return current
	}
	v := *in
	return &v
}

func cloneDraft(d Draft) Draft {
	d.Reach = copyMetric(d.Reach, nil)
	d.Likes = copyMetric(d.Likes, nil)
	d.Leads = copyMetric(d.Leads, nil)
	return d
}

func auditLine(d Draft) string {
	return "draft_id=" + d.ID + " tenant_id=" + d.TenantID + " asset_id=" + d.AssetID + " return_ref=" + d.ReturnRef
}
