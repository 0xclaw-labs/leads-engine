package receptionguard

import "strings"

// SessionScope distinguishes regular independent-merchant CRM reception from
// dual-sided entrusted-order (guanlan-order / HUI-4428) sessions.
type SessionScope string

const (
	ScopeCRMReception   SessionScope = "crm_reception"
	ScopeEntrustedOrder SessionScope = "entrusted_order"
)

// InquiryKind classifies the visitor's consultation intent.
type InquiryKind string

const (
	InquiryOrdinaryFAQ    InquiryKind = "ordinary_faq"
	InquiryAfterSales     InquiryKind = "after_sales"
	InquirySalesConsult   InquiryKind = "sales_consult"
	InquiryEntrustedOrder InquiryKind = "entrusted_order"
)

// BoundaryAction identifies the action being checked against scope & consent boundaries.
type BoundaryAction string

const (
	BoundaryActionReply         BoundaryAction = "reply"
	BoundaryActionIngestMessage BoundaryAction = "ingest_message"
	BoundaryActionCreateLead    BoundaryAction = "create_lead"
	BoundaryActionTriggerSOP    BoundaryAction = "trigger_sop"
	BoundaryActionOutboundCall  BoundaryAction = "outbound_call"
)

// ConsentRecord captures the verifiable consent state for a contact/session.
type ConsentRecord struct {
	TenantID         string
	AllowContact     bool
	Purpose          string
	MarketingAllowed bool
	Revoked          bool
	NoticeVersion    string
}

// ScopeBoundaryInput carries the context checked by GuardScopeBoundary.
type ScopeBoundaryInput struct {
	TenantID                       string
	SessionScope                   SessionScope
	SourceApp                      string
	Action                         BoundaryAction
	InquiryKind                    InquiryKind
	PayloadText                    string
	ContainsEntrustedCustomerMsg   bool
	ContainsEntrustedContractorMsg bool
	ContainsInternalPricing        bool
	ForceLeadOnInquiry             bool
	ExplicitSalesIntent            bool
	ContactName                    string
	ContactPhone                   string
	Consent                        *ConsentRecord
}

// ScopeBoundaryDecision is the fail-closed result of GuardScopeBoundary.
type ScopeBoundaryDecision struct {
	Allowed    bool
	Violations []string
	Reason     string
}

// GuardScopeBoundary enforces:
//  1. Dual-sided entrusted-order isolation (ECO-CLEANUP-R3-20261125 / HUI-4428):
//     entrusted-order raw customer/contractor messages and internal pricing
//     MUST NEVER mix into regular CRM reception sessions.
//  2. Ordinary FAQ and after-sales inquiries MUST NEVER force Lead creation.
//  3. Missing or revoked marketing consent MUST block SOP and outbound calls.
func GuardScopeBoundary(in ScopeBoundaryInput) ScopeBoundaryDecision {
	tenantID := strings.TrimSpace(in.TenantID)
	scope := in.SessionScope
	if scope == "" {
		scope = ScopeCRMReception
	}
	sourceApp := strings.ToLower(strings.TrimSpace(in.SourceApp))

	var violations []string
	add := func(v string) {
		violations = appendUnique(violations, v)
	}

	if tenantID == "" {
		add("missing_tenant_id")
	}

	// 1. Dual-sided entrusted-order isolation boundary.
	hasEntrustedPayload := in.ContainsEntrustedCustomerMsg ||
		in.ContainsEntrustedContractorMsg ||
		in.ContainsInternalPricing ||
		in.InquiryKind == InquiryEntrustedOrder ||
		sourceApp == "guanlan-order" ||
		hasEntrustedOrderMarkers(in.PayloadText)

	if scope == ScopeCRMReception && hasEntrustedPayload {
		add("entrusted_order_isolation_breach")
	}
	if scope == ScopeEntrustedOrder {
		if in.Action == BoundaryActionCreateLead || in.Action == BoundaryActionTriggerSOP || in.Action == BoundaryActionOutboundCall {
			add("entrusted_order_crm_action_forbidden")
		}
	}

	// 2. Ordinary FAQ & after-sales inquiries NEVER force Lead creation.
	if in.ForceLeadOnInquiry {
		if in.InquiryKind == InquiryOrdinaryFAQ || in.InquiryKind == InquiryAfterSales || !in.ExplicitSalesIntent {
			add("ordinary_inquiry_no_forced_lead")
		}
	}
	if in.Action == BoundaryActionCreateLead {
		if in.InquiryKind == InquiryOrdinaryFAQ || in.InquiryKind == InquiryAfterSales {
			add("ordinary_inquiry_no_forced_lead")
		}
		if !in.ExplicitSalesIntent {
			add("explicit_sales_intent_required")
		}
		if in.Consent == nil ||
			strings.TrimSpace(in.Consent.TenantID) != tenantID ||
			strings.TrimSpace(in.Consent.Purpose) != "sales_followup" ||
			!in.Consent.AllowContact ||
			in.Consent.Revoked ||
			strings.TrimSpace(in.Consent.NoticeVersion) == "" ||
			strings.TrimSpace(in.ContactName) == "" ||
			strings.TrimSpace(in.ContactPhone) == "" {
			add("lead_authorization_required")
		}
	}

	// 3. Missing or revoked marketing consent blocks SOP & outbound calls.
	if in.Action == BoundaryActionTriggerSOP || in.Action == BoundaryActionOutboundCall {
		if in.Consent == nil || strings.TrimSpace(in.Consent.TenantID) != tenantID {
			add("marketing_consent_required")
		} else if in.Consent.Revoked {
			add("marketing_consent_revoked")
		} else if !in.Consent.MarketingAllowed || strings.TrimSpace(in.Consent.NoticeVersion) == "" {
			add("marketing_consent_required")
		}
	}

	if len(violations) > 0 {
		return ScopeBoundaryDecision{
			Allowed:    false,
			Violations: violations,
			Reason:     violations[0],
		}
	}
	return ScopeBoundaryDecision{
		Allowed: true,
		Reason:  "ok",
	}
}

func hasEntrustedOrderMarkers(text string) bool {
	n := foldText(text)
	return containsAny(n,
		"entrusted_order_raw",
		"contractor_raw_msg",
		"contractor_quote",
		"internal_settlement_price",
		"发包方底价",
		"接单方原话",
		"接单方底价",
		"委托单内部报价",
		"撮合底价",
		"供需双边原消息",
		"guanlan-order",
	)
}
