// Package todaynext hardens the Today/Next action-first sales workflow (HUI-2598).
//
// It builds strictly on the existing workbench projection and receptionguard
// boundaries without creating a second CRM homepage, second task queue DB, or
// duplicate customer store.
package todaynext

import (
	"sort"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/receptionguard"
	"github.com/bianjiefilm/leads-engine/server/internal/workbench"
)

// ActionBucket is one of the 7 canonical action-first buckets on the Today desk.
type ActionBucket string

const (
	BucketHumanTakeoverNeeded          ActionBucket = "human_takeover_needed"
	BucketNewInquiry                   ActionBucket = "new_inquiry"
	BucketOverdue                      ActionBucket = "overdue"
	BucketDueToday                     ActionBucket = "due_today"
	BucketAIDraftPending               ActionBucket = "ai_draft_pending"
	BucketWaitingCustomer              ActionBucket = "waiting_customer"
	BucketMissingInfoOrPermissionError ActionBucket = "missing_info_or_permission_error"
)

// CanonicalBucketOrder returns the 7 action-first buckets in strict priority order.
func CanonicalBucketOrder() []ActionBucket {
	return []ActionBucket{
		BucketHumanTakeoverNeeded,
		BucketNewInquiry,
		BucketOverdue,
		BucketDueToday,
		BucketAIDraftPending,
		BucketWaitingCustomer,
		BucketMissingInfoOrPermissionError,
	}
}

// BucketPriority returns the deterministic rank for an ActionBucket (lower = higher priority).
// Priority: human_takeover_needed > new_inquiry > overdue > due_today >
// ai_draft_pending > waiting_customer > missing_info_or_permission_error.
func BucketPriority(b ActionBucket) int {
	switch NormalizeBucket(string(b)) {
	case BucketHumanTakeoverNeeded:
		return 0
	case BucketNewInquiry:
		return 1
	case BucketOverdue:
		return 2
	case BucketDueToday:
		return 3
	case BucketAIDraftPending:
		return 4
	case BucketWaitingCustomer:
		return 5
	case BucketMissingInfoOrPermissionError:
		return 6
	default:
		return 99
	}
}

// NormalizeBucket maps legacy workbench group/bucket names and canonical names
// onto one of the 7 canonical ActionBucket values.
func NormalizeBucket(raw string) ActionBucket {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(BucketHumanTakeoverNeeded), workbench.TodayHuman:
		return BucketHumanTakeoverNeeded
	case string(BucketNewInquiry), workbench.BucketUnprocessed:
		return BucketNewInquiry
	case string(BucketOverdue), workbench.BucketStale:
		return BucketOverdue
	case string(BucketDueToday), workbench.BucketNeedsSchedule:
		return BucketDueToday
	case string(BucketAIDraftPending), workbench.TodayAIDraft:
		return BucketAIDraftPending
	case string(BucketWaitingCustomer), workbench.BucketWaitingReply:
		return BucketWaitingCustomer
	case string(BucketMissingInfoOrPermissionError), workbench.TodayInfo, workbench.BucketAssignmentException:
		return BucketMissingInfoOrPermissionError
	default:
		return BucketMissingInfoOrPermissionError
	}
}

// NextStep models an advisory AI suggestion or an explicit manual next step.
// All side-effect switches stay false on the Today desk.
type NextStep struct {
	Kind             string `json:"kind"`
	Source           string `json:"source"`
	At               string `json:"at,omitempty"`
	Label            string `json:"label"`
	Editable         bool   `json:"editable"`
	Dismissible      bool   `json:"dismissible"`
	Dismissed        bool   `json:"dismissed,omitempty"`
	AutoCall         bool   `json:"auto_call"`
	AutoMessage      bool   `json:"auto_message"`
	AutoSOP          bool   `json:"auto_sop"`
	ForceOpportunity bool   `json:"force_opportunity"`
}

// WorkItem is one self-contained row in the deterministic Today/Next queue.
// It carries canonical source provenance, confirmed context, consent state,
// current owner, allowed contact channels, last interaction, and an editable/
// dismissible suggested next step where a manual Next Action always wins.
type WorkItem struct {
	ID               string                     `json:"id"`
	TenantID         string                     `json:"tenant_id,omitempty"`
	RecordKind       string                     `json:"record_kind"`
	LeadID           string                     `json:"lead_id,omitempty"`
	OpportunityID    string                     `json:"opportunity_id,omitempty"`
	SessionID        string                     `json:"session_id,omitempty"`
	Bucket           ActionBucket               `json:"bucket"`
	Priority         int                        `json:"priority"`
	SourceCampaign   string                     `json:"source_campaign,omitempty"`
	SourceChannel    string                     `json:"source_channel,omitempty"`
	SourceForm       string                     `json:"source_form,omitempty"`
	SourceAt         string                     `json:"source_at,omitempty"`
	ConfirmedContext workbench.ConfirmedContext `json:"confirmed_context"`
	ConsentState     string                     `json:"consent_state"`
	MarketingAllowed bool                       `json:"marketing_allowed"`
	ConsentRevoked   bool                       `json:"consent_revoked"`
	OwnerLabel       string                     `json:"owner_label,omitempty"`
	AssigneeMemberID string                     `json:"-"`
	AllowedChannels  []string                   `json:"allowed_channels"`
	LastInteraction  workbench.Interaction      `json:"last_interaction"`
	SuggestedNext    *NextStep                  `json:"suggested_next,omitempty"`
	ManualNext       *NextStep                  `json:"manual_next,omitempty"`
	EffectiveNext    NextStep                   `json:"effective_next"`
	DraftBody        string                     `json:"draft_body,omitempty"`
	DraftEditable    bool                       `json:"draft_editable,omitempty"`
	DraftDismissible bool                       `json:"draft_dismissible,omitempty"`
	DueAt            string                     `json:"due_at,omitempty"`
	CreatedAt        string                     `json:"created_at,omitempty"`
	AIScore          int                        `json:"ai_score,omitempty"`
	OutreachNotice   string                     `json:"outreach_notice,omitempty"`
}

// ResolveEffectiveNextAction computes the active next step for a WorkItem.
// A manual Next Action strictly overrides any AI suggestion; AI score never
// arms auto-calling, auto-messaging, auto-SOP, or forced opportunity creation.
func ResolveEffectiveNextAction(suggested, manual *NextStep, aiScore int) NextStep {
	_ = aiScore
	if manual != nil && (strings.TrimSpace(manual.Kind) != "" || strings.TrimSpace(manual.At) != "") {
		kind := strings.TrimSpace(manual.Kind)
		if kind == "" {
			kind = "manual_follow_up"
		}
		label := strings.TrimSpace(manual.Label)
		if label == "" {
			label = nextStepLabel(kind)
		}
		return NextStep{
			Kind:             kind,
			Source:           "manual",
			At:               strings.TrimSpace(manual.At),
			Label:            label,
			Editable:         true,
			Dismissible:      false,
			Dismissed:        false,
			AutoCall:         false,
			AutoMessage:      false,
			AutoSOP:          false,
			ForceOpportunity: false,
		}
	}
	if suggested != nil && !suggested.Dismissed && strings.TrimSpace(suggested.Kind) != "" {
		kind := strings.TrimSpace(suggested.Kind)
		label := strings.TrimSpace(suggested.Label)
		if label == "" {
			label = nextStepLabel(kind)
		}
		src := strings.TrimSpace(suggested.Source)
		if src == "" || src == "manual" {
			src = "ai_suggestion"
		}
		return NextStep{
			Kind:             kind,
			Source:           src,
			At:               strings.TrimSpace(suggested.At),
			Label:            label,
			Editable:         true,
			Dismissible:      true,
			Dismissed:        false,
			AutoCall:         false,
			AutoMessage:      false,
			AutoSOP:          false,
			ForceOpportunity: false,
		}
	}
	return NextStep{
		Kind:             "none",
		Source:           "manual",
		Label:            nextStepLabel("none"),
		Editable:         true,
		Dismissible:      false,
		Dismissed:        suggested != nil && suggested.Dismissed,
		AutoCall:         false,
		AutoMessage:      false,
		AutoSOP:          false,
		ForceOpportunity: false,
	}
}

// SortWorkItems returns a new slice ordered deterministically by user action
// priority (NOT raw CRM table order):
//
//	human_takeover_needed > new_inquiry > overdue > due_today >
//	ai_draft_pending > waiting_customer > missing_info_or_permission_error
//
// Within each bucket:
//   - overdue / due_today order by earliest DueAt first (items with DueAt before those without).
//   - other buckets order by most recent interaction / source / creation timestamp first.
//   - ID breaks any remaining ties deterministically.
func SortWorkItems(items []WorkItem) []WorkItem {
	out := make([]WorkItem, len(items))
	for i, item := range items {
		out[i] = normalizeWorkItem(item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		switch a.Bucket {
		case BucketOverdue, BucketDueToday:
			aTS, aOK := parseRFC3339(firstNonEmpty(a.DueAt, a.EffectiveNext.At))
			bTS, bOK := parseRFC3339(firstNonEmpty(b.DueAt, b.EffectiveNext.At))
			if aOK != bOK {
				return aOK
			}
			if aOK && !aTS.Equal(bTS) {
				return aTS.Before(bTS)
			}
		default:
			aTS, aOK := parseRFC3339(firstNonEmpty(a.LastInteraction.At, a.SourceAt, a.CreatedAt))
			bTS, bOK := parseRFC3339(firstNonEmpty(b.LastInteraction.At, b.SourceAt, b.CreatedAt))
			if aOK != bOK {
				return aOK
			}
			if aOK && !aTS.Equal(bTS) {
				return aTS.After(bTS)
			}
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.LeadID != b.LeadID {
			return a.LeadID < b.LeadID
		}
		return a.SessionID < b.SessionID
	})
	return out
}

// ProjectFromWorkbench converts the single-source workbench.Result.Today map
// into a deterministic action-first sorted WorkItem queue.
func ProjectFromWorkbench(res workbench.Result, leads []workbench.LeadView) []WorkItem {
	leadByID := make(map[string]workbench.LeadView, len(leads))
	for _, l := range leads {
		leadByID[l.ID] = l
	}
	var items []WorkItem
	for _, group := range []string{
		workbench.TodayHuman,
		workbench.TodayNewInquiry,
		workbench.TodayOverdue,
		workbench.TodayDue,
		workbench.TodayAIDraft,
		workbench.TodayWaiting,
		workbench.TodayInfo,
	} {
		for _, raw := range res.Today[group] {
			items = append(items, workItemFromWorkbenchItem(group, raw, leadByID))
		}
	}
	return SortWorkItems(items)
}

func workItemFromWorkbenchItem(group string, raw workbench.Item, leadByID map[string]workbench.LeadView) WorkItem {
	bucket := NormalizeBucket(group)
	lead, hasLead := leadByID[raw.LeadID]

	consentState := "unknown"
	marketingAllowed := false
	consentRevoked := false
	dueAt := raw.Next.At
	createdAt := ""
	aiScore := 0

	if hasLead {
		consentState = normalizeConsentState(lead.ConsentStatus, lead.MarketingSMSOrPhone)
		marketingAllowed = lead.MarketingSMSOrPhone
		consentRevoked = strings.EqualFold(strings.TrimSpace(lead.ConsentStatus), "revoked") ||
			strings.EqualFold(strings.TrimSpace(lead.ConsentStatus), "denied")
		if dueAt == "" {
			dueAt = lead.ManualNextAt
		}
		createdAt = lead.CreatedAt
		aiScore = lead.AIScore
	} else if len(raw.AllowedContacts) > 0 {
		for _, ch := range raw.AllowedContacts {
			if ch == "sms" || ch == "phone" {
				marketingAllowed = true
				consentState = "granted"
			}
		}
	}

	var suggested, manual *NextStep
	step := &NextStep{
		Kind:        raw.Next.Kind,
		Source:      raw.Next.Source,
		At:          raw.Next.At,
		Label:       raw.Next.Label,
		Editable:    true,
		Dismissible: raw.Next.Source != "manual",
	}
	if raw.Next.Source == "manual" {
		manual = step
	} else {
		suggested = step
	}

	allowed := make([]string, 0, len(raw.AllowedContacts))
	allowed = append(allowed, raw.AllowedContacts...)

	wi := WorkItem{
		ID:               raw.ID,
		TenantID:         raw.TenantID,
		RecordKind:       raw.Kind,
		LeadID:           raw.LeadID,
		OpportunityID:    raw.OpportunityID,
		SessionID:        raw.SessionID,
		Bucket:           bucket,
		SourceCampaign:   raw.Source.Activity,
		SourceChannel:    raw.Source.Channel,
		SourceForm:       raw.Source.Form,
		SourceAt:         raw.Source.At,
		ConfirmedContext: cloneConfirmedContext(raw.Context),
		ConsentState:     consentState,
		MarketingAllowed: marketingAllowed,
		ConsentRevoked:   consentRevoked,
		OwnerLabel:       raw.OwnerLabel,
		AssigneeMemberID: raw.Assignee,
		AllowedChannels:  allowed,
		LastInteraction:  raw.LastInteraction,
		SuggestedNext:    suggested,
		ManualNext:       manual,
		DraftBody:        raw.DraftBody,
		DraftEditable:    strings.TrimSpace(raw.DraftBody) != "",
		DraftDismissible: strings.TrimSpace(raw.DraftBody) != "",
		DueAt:            dueAt,
		CreatedAt:        createdAt,
		AIScore:          aiScore,
		OutreachNotice:   raw.OutreachNotice,
	}
	return normalizeWorkItem(wi)
}

func normalizeWorkItem(item WorkItem) WorkItem {
	item.Bucket = NormalizeBucket(string(item.Bucket))
	item.Priority = BucketPriority(item.Bucket)
	if item.AllowedChannels == nil {
		item.AllowedChannels = []string{}
	} else {
		cp := make([]string, 0, len(item.AllowedChannels))
		for _, ch := range item.AllowedChannels {
			ch = strings.TrimSpace(ch)
			if ch == "" {
				continue
			}
			// Never expose sms/phone marketing channels when consent is revoked or missing.
			if (ch == "sms" || ch == "phone") && (item.ConsentRevoked || !item.MarketingAllowed) {
				continue
			}
			cp = append(cp, ch)
		}
		item.AllowedChannels = cp
	}
	if item.SuggestedNext != nil {
		s := *item.SuggestedNext
		s.Editable = true
		s.Dismissible = true
		s.AutoCall = false
		s.AutoMessage = false
		s.AutoSOP = false
		s.ForceOpportunity = false
		if strings.TrimSpace(s.Source) == "" || s.Source == "manual" {
			s.Source = "ai_suggestion"
		}
		if strings.TrimSpace(s.Label) == "" && strings.TrimSpace(s.Kind) != "" {
			s.Label = nextStepLabel(s.Kind)
		}
		item.SuggestedNext = &s
	}
	if item.ManualNext != nil {
		m := *item.ManualNext
		m.Source = "manual"
		m.Editable = true
		m.Dismissible = false
		m.Dismissed = false
		m.AutoCall = false
		m.AutoMessage = false
		m.AutoSOP = false
		m.ForceOpportunity = false
		if strings.TrimSpace(m.Label) == "" && strings.TrimSpace(m.Kind) != "" {
			m.Label = nextStepLabel(m.Kind)
		}
		item.ManualNext = &m
	}
	item.EffectiveNext = ResolveEffectiveNextAction(item.SuggestedNext, item.ManualNext, item.AIScore)
	if item.DraftBody != "" {
		item.DraftEditable = true
		item.DraftDismissible = true
		item.DraftBody = receptionguard.ScrubSensitiveText(item.DraftBody)
	}
	item.LastInteraction.Summary = receptionguard.ScrubSensitiveText(item.LastInteraction.Summary)
	if strings.TrimSpace(item.ConsentState) == "" {
		item.ConsentState = normalizeConsentState("", item.MarketingAllowed)
	}
	if item.ConsentRevoked || !item.MarketingAllowed {
		if strings.Contains(item.OutreachNotice, "自动触达已成功") || strings.TrimSpace(item.OutreachNotice) == "" {
			if item.ConsentRevoked {
				item.OutreachNotice = "营销许可已撤销，已禁止自动触达"
			} else {
				item.OutreachNotice = "没有营销许可"
			}
		}
	} else if strings.Contains(item.OutreachNotice, "自动触达已成功") {
		item.OutreachNotice = "没有触达回执"
	}
	return item
}

func cloneConfirmedContext(c workbench.ConfirmedContext) workbench.ConfirmedContext {
	out := workbench.ConfirmedContext{
		ContactID: c.ContactID,
		Customer:  c.Customer,
		Business:  c.Business,
	}
	if len(c.Facts) > 0 {
		out.Facts = append([]string(nil), c.Facts...)
	}
	if len(c.Ask) > 0 {
		out.Ask = append([]string(nil), c.Ask...)
	}
	return out
}

func normalizeConsentState(raw string, marketingAllowed bool) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "granted":
		return "granted"
	case "pending":
		return "pending"
	case "denied":
		return "denied"
	case "revoked":
		return "revoked"
	default:
		if marketingAllowed {
			return "granted"
		}
		return "missing"
	}
}

func nextStepLabel(kind string) string {
	switch strings.TrimSpace(kind) {
	case "manual_follow_up":
		return "按手工安排跟进"
	case "channel_follow_up":
		return "在授权渠道内跟进"
	case "suggest_schedule":
		return "安排下一次跟进"
	case "wait_customer":
		return "等待客户回复"
	case "confirm_draft":
		return "确认或修改草稿"
	case "write_reply_draft":
		return "先写回复草稿"
	default:
		return "无系统动作"
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func parseRFC3339(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, true
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	return ts, err == nil
}
