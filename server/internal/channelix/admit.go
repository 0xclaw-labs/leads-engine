// Package channelix admits one authorized comment or direct message.
//
// 发布授权不包含私信读取。评论和消息先是互动；只有明确的购买意向才建立线索候选。
// 没有真实渠道凭证时，验证状态保持 unverified，不把任何平台写成已支持。
// 本包不访问网络，也不代替接待核心发送回复。
package channelix

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/receptionguard"
)

const (
	CapCommentRead  = "comment.read"
	CapMessageRead  = "message.read"
	CapCommentReply = "comment.reply"
	CapMessageReply = "message.reply"
	CapReply        = "reply"
	CapPublish      = "publish"

	KindComment = "comment"
	KindMessage = "direct_message"

	PurposeSalesInquiry = "sales_inquiry"
	PurposeSupport      = "support"
	PurposeGeneralQA    = "general_qa"
	PurposeAftersales   = "aftersales"

	SpeakerLeads  = "leads_bot"
	SpeakerMatrix = "matrix_bot"
	SpeakerHuman  = "human"

	ReplyAuthorityReceptionGuard = "receptionguard"

	RefusalCapability    = "capability_missing"
	RefusalForgedTenant  = "forged_tenant"
	RefusalForgedSource  = "forged_source"
	RefusalSpeaker       = "speaker_conflict"
	RefusalSecondBot     = "second_reply_bot_forbidden"
	RefusalHumanTakeover = "human_takeover_active"
	RefusalRevoked       = "permission_revoked"
)

// Grant is the authorized connection snapshot. CredentialClaim is ignored:
// this build cannot verify a live channel credential.
type Grant struct {
	TenantID, Provider, AccountID, AppID string
	SubjectNS, MessageNS, PostNS         string
	Capabilities                         []string
	Revoked                              bool
	CredentialClaim                      string
}

// Event is one connector delivery. Target tenant and source fields must match
// the grant; the caller does not get to aim the event at another tenant.
type Event struct {
	TargetTenantID, Provider, AccountID, AppID string
	SubjectNS, MessageNS, PostNS               string
	EventID, Kind, SubjectID, Nickname, Phone  string
	Text                                       string
	ExplicitIntent                             bool
	Purpose                                    string
	Retracted                                  bool
	Score                                      int
	PhoneMarketingConsent                      bool
	SMSMarketingConsent                        bool
	LegacyRoute                                bool
}

// Stored is the row already kept for this dedup key.
type Stored struct {
	InteractionID  string
	CandidateID    string
	TextSHA256     string
	Retracted      bool
	PhoneMarketing bool
	SMSMarketing   bool
}

// Input is everything Admit may see.
type Input struct {
	Grant             Grant
	Event             Event
	Prior             *Stored
	Floor             string
	WantReply         bool
	ReplySpeaker      string
	ReceptionFence    *receptionguard.SessionFence
	ReceptionEpoch    int
	ReceptionApproved bool
	HumanProcessed    bool
}

// MinimalNotifyRef is the restricted Notify reference carrying IDs only.
// Customer message text, phone, email, and nickname are excluded by construction.
type MinimalNotifyRef struct {
	TenantID      string `json:"tenant_id"`
	Provider      string `json:"provider"`
	AccountID     string `json:"account_id"`
	InteractionID string `json:"interaction_id"`
	EventID       string `json:"event_id"`
	Path          string `json:"path"`
}

// SubjectRef is the channel subject. Nickname and phone are not identity.
type SubjectRef struct {
	TenantID, Provider, AccountID, SubjectID, Nickname, Phone string
}

// Result is the admission decision. AutoReach and Delivered stay false:
// a score or a reply draft does not send anything.
type Result struct {
	OK                    bool
	Refusal               string
	DedupKey              string
	InteractionID         string
	CandidateID           string
	TextSHA256            string
	CreateInteraction     bool
	Update                bool
	CreateCandidate       bool
	Idempotent            bool
	Retract               bool
	WithdrawCandidate     bool
	ChannelContact        bool
	PhoneMarketing        bool
	SMSMarketing          bool
	AutoReach             bool
	ReplyAllowed          bool
	ReplyRefusal          string
	ReplyAuthority        string
	SecondReplyBotStarted bool
	Delivered             bool
	HumanProcessed        bool
	Verification          string
	LiveProviders         []string
}

// CapView is the public capability statement for this build.
type CapView struct {
	Verification              string
	LiveProviders             []string
	PublishImpliesMessageRead bool
}

// Admit decides what to keep. It never grants phone or SMS marketing from a
// missing number, never treats a high score as permission to reach out, never
// resurrects a retracted event or revoked marketing consent on late/legacy
// deliveries, and delegates human-takeover reply arbitration solely to receptionguard.
func Admit(in Input) Result {
	res := Result{
		DedupKey:              dedupKey(in.Grant.TenantID, in.Grant.Provider, in.Grant.AccountID, in.Event.EventID),
		TextSHA256:            hashText(in.Event.Text),
		ReplyAuthority:        ReplyAuthorityReceptionGuard,
		SecondReplyBotStarted: false,
		Verification:          "unverified",
		LiveProviders:         nil,
	}
	if in.Grant.Revoked {
		res.Refusal = RefusalRevoked
		return res
	}
	if strings.TrimSpace(in.Event.TargetTenantID) == "" || in.Event.TargetTenantID != in.Grant.TenantID {
		res.Refusal = RefusalForgedTenant
		return res
	}
	if in.Event.Provider != in.Grant.Provider || in.Event.AccountID != in.Grant.AccountID || in.Event.AppID != in.Grant.AppID ||
		in.Event.SubjectNS != in.Grant.SubjectNS || in.Event.MessageNS != in.Grant.MessageNS || in.Event.PostNS != in.Grant.PostNS {
		res.Refusal = RefusalForgedSource
		return res
	}
	need := capForKind(in.Event.Kind)
	if need == "" || !hasCap(in.Grant, need) {
		res.Refusal = RefusalCapability
		return res
	}
	res.OK = true
	res.ChannelContact = true
	res.HumanProcessed = in.HumanProcessed

	// Once an event is retracted (either now or previously), never restore
	// marketing consent, never resurrect a lead candidate, and never allow reply.
	if in.Event.Retracted {
		res.Retract = true
		res.PhoneMarketing = false
		res.SMSMarketing = false
		if in.WantReply {
			res.ReplyAllowed = false
			res.ReplyRefusal = RefusalRevoked
		}
		if in.Prior != nil {
			res.InteractionID = in.Prior.InteractionID
			res.CandidateID = in.Prior.CandidateID
			res.WithdrawCandidate = in.Prior.CandidateID != "" && !in.Prior.Retracted
			res.Idempotent = in.Prior.Retracted
			return res
		}
		res.CreateInteraction = true
		return res
	}
	if in.Prior != nil && in.Prior.Retracted {
		// Late edit/replay/legacy-route arrival after retraction stays latched
		// retracted: no duplicate lead, no update, no marketing consent restoration.
		res.InteractionID = in.Prior.InteractionID
		res.CandidateID = in.Prior.CandidateID
		res.Retract = true
		res.Idempotent = true
		res.PhoneMarketing = false
		res.SMSMarketing = false
		if in.WantReply {
			res.ReplyAllowed = false
			res.ReplyRefusal = RefusalRevoked
		}
		return res
	}

	res.PhoneMarketing = strings.TrimSpace(in.Event.Phone) != "" && in.Event.PhoneMarketingConsent
	res.SMSMarketing = strings.TrimSpace(in.Event.Phone) != "" && in.Event.SMSMarketingConsent
	res.ReplyAllowed, res.ReplyRefusal = decideReply(in)

	if in.Prior != nil {
		res.InteractionID = in.Prior.InteractionID
		res.CandidateID = in.Prior.CandidateID
		// Never let a late legacy-route redelivery overwrite existing state or
		// restore previously absent/revoked marketing consent.
		if in.Event.LegacyRoute {
			res.PhoneMarketing = in.Prior.PhoneMarketing && res.PhoneMarketing
			res.SMSMarketing = in.Prior.SMSMarketing && res.SMSMarketing
			res.Idempotent = true
			return res
		}
		if in.Prior.TextSHA256 == res.TextSHA256 {
			res.Idempotent = true
			return res
		}
		res.Update = true
		if res.CandidateID == "" && explicitSales(in.Event) {
			res.CreateCandidate = true
		}
		return res
	}
	res.CreateInteraction = true
	res.CreateCandidate = explicitSales(in.Event)
	return res
}

// BuildMinimalNotifyRef returns an id-only Notify reference without sensitive
// customer text or contact fields.
func BuildMinimalNotifyRef(tenantID, provider, accountID, interactionID, eventID string) (MinimalNotifyRef, error) {
	path, err := InteractionPath(interactionID, "")
	if err != nil {
		return MinimalNotifyRef{}, err
	}
	if !token(tenantID, 128) || !token(provider, 64) || !token(accountID, 128) || !token(eventID, 128) {
		return MinimalNotifyRef{}, errors.New("channelix: invalid minimal notify ref id")
	}
	return MinimalNotifyRef{
		TenantID:      tenantID,
		Provider:      provider,
		AccountID:     accountID,
		InteractionID: interactionID,
		EventID:       eventID,
		Path:          path,
	}, nil
}

// SharesIdentity is true only for the same tenant, provider, account, and subject.
func SharesIdentity(a, b SubjectRef) bool {
	return a.TenantID != "" && a.TenantID == b.TenantID &&
		a.Provider == b.Provider && a.AccountID == b.AccountID &&
		a.SubjectID != "" && a.SubjectID == b.SubjectID
}

// AuditLine is safe for logs. The customer text argument is never copied.
func AuditLine(tenant, provider, account, eventID, text string) string {
	_ = text
	return "tenant=" + tenant + " provider=" + provider + " account=" + account + " event=" + eventID + " text=redacted"
}

// InteractionPath returns an id-only path. Customer text is rejected.
func InteractionPath(id, text string) (string, error) {
	if text != "" {
		return "", errors.New("customer text is not a path")
	}
	if !token(id, 128) {
		return "", errors.New("interaction id")
	}
	return "/channel-interactions/" + id, nil
}

// Capability reports that no live provider is verified in this build.
func Capability() CapView {
	return CapView{Verification: "unverified"}
}

func decideReply(in Input) (bool, string) {
	if !in.WantReply {
		return false, ""
	}
	// Never allow a second reply bot (e.g. matrix_bot) to claim reply authority.
	if in.ReplySpeaker != "" && in.ReplySpeaker != SpeakerLeads && in.ReplySpeaker != SpeakerHuman {
		return false, RefusalSecondBot
	}
	if !hasReplyCapForKind(in.Grant, in.Event.Kind) {
		return false, RefusalCapability
	}
	// Human takeover / outbound arbitration belongs solely to receptionguard (HUI-1688).
	if in.ReceptionFence != nil {
		snap := in.ReceptionFence.Snapshot()
		epoch := in.ReceptionEpoch
		if epoch <= 0 {
			epoch = snap.Epoch
		}
		source := receptionguard.SourceAI
		if in.ReplySpeaker == SpeakerHuman {
			source = receptionguard.SourceHuman
		}
		dec := in.ReceptionFence.AuthorizeOutbound(receptionguard.OutboundRequest{
			TenantID:   in.Grant.TenantID,
			SessionID:  snap.SessionID,
			ReplyEpoch: epoch,
			Channel:    receptionguard.ChannelText,
			Source:     source,
			Approved:   in.ReceptionApproved,
		})
		if !dec.Allowed {
			return false, dec.Reason
		}
	}
	if in.Floor != "" && in.Floor != SpeakerLeads {
		return false, RefusalSpeaker
	}
	return true, ""
}

func hasReplyCapForKind(g Grant, kind string) bool {
	if hasCap(g, CapReply) {
		return true
	}
	switch kind {
	case KindComment:
		return hasCap(g, CapCommentReply)
	case KindMessage:
		return hasCap(g, CapMessageReply)
	default:
		return false
	}
}

func explicitSales(ev Event) bool {
	return ev.ExplicitIntent && ev.Purpose == PurposeSalesInquiry
}

func capForKind(kind string) string {
	switch kind {
	case KindComment:
		return CapCommentRead
	case KindMessage:
		return CapMessageRead
	default:
		return ""
	}
}

func hasCap(g Grant, cap string) bool {
	for _, item := range g.Capabilities {
		if item == cap {
			return true
		}
	}
	return false
}

func dedupKey(tenant, provider, account, eventID string) string {
	return tenant + "/" + provider + "/" + account + "/" + eventID
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func token(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
