package todaynext

import (
	"strings"

	"github.com/bianjiefilm/leads-engine/server/internal/receptionguard"
	"github.com/bianjiefilm/leads-engine/server/internal/workbench"
)

// ActionType names the intent being checked on the Today/Next surface.
type ActionType string

const (
	ActionManualFollowUp    ActionType = "manual_follow_up"
	ActionChannelFollowUp   ActionType = "channel_follow_up"
	ActionWaitCustomer      ActionType = "wait_customer"
	ActionSaveDraft         ActionType = "save_draft"
	ActionCreateOpportunity ActionType = "create_opportunity"
	ActionAutoOutreach      ActionType = "auto_outreach"
	ActionAutoSOP           ActionType = "auto_sop"
	ActionAutoCall          ActionType = "auto_call"
	ActionCreateContact     ActionType = "create_contact"
)

// CustomerType distinguishes individual buyers from business accounts.
type CustomerType string

const (
	CustomerIndividual CustomerType = "individual"
	CustomerBusiness   CustomerType = "business"
)

// TodayActionInput carries the facts evaluated by ValidateTodayAction.
type TodayActionInput struct {
	TenantID                 string
	Action                   ActionType
	CustomerType             CustomerType
	Company                  string
	RequireCompany           bool
	Purpose                  string
	ConsentStatus            string
	MarketingAllowed         bool
	ConsentRevoked           bool
	ChannelReplyAllowed      bool
	ChannelIdentity          string
	SendReceiptStored        bool
	ClaimAutoOutreachSuccess bool
	TriggerAutoOutbound      bool
	TriggerAutoSOP           bool
	TriggerAutoCall          bool
	ForceOpportunity         bool
	AIScore                  int
	ManualOverride           bool
	Consent                  *receptionguard.ConsentRecord
}

// TodayActionDecision is the fail-closed outcome of ValidateTodayAction.
type TodayActionDecision struct {
	Allowed             bool     `json:"allowed"`
	Code                string   `json:"code"`
	Reason              string   `json:"reason"`
	Violations          []string `json:"violations,omitempty"`
	OutreachNotice      string   `json:"outreach_notice,omitempty"`
	AutoOutreachSuccess bool     `json:"auto_outreach_success"`
	AllowAutoCall       bool     `json:"allow_auto_call"`
	AllowAutoSOP        bool     `json:"allow_auto_sop"`
	ForceOpportunity    bool     `json:"force_opportunity"`
	CompanyRequired     bool     `json:"company_required"`
}

// IsNonSalesPurpose reports purposes that MUST NOT force opportunity creation.
func IsNonSalesPurpose(purpose string) bool {
	p := strings.TrimSpace(strings.ToLower(purpose))
	if workbench.RefuseSalesPush(p) {
		return true
	}
	switch p {
	case "ordinary_inquiry", "ordinary_faq", "general_inquiry", "support", "after_sale":
		return true
	default:
		return false
	}
}

// ValidateTodayAction enforces consent and non-coercion invariants on Today/Next:
//  1. Missing or revoked marketing consent NEVER shows auto-outreach success and
//     blocks automatic outbound/SOP triggers.
//  2. Even with marketing consent, auto-outreach success is forbidden without a
//     stored send receipt.
//  3. Ordinary inquiry, after-sales, and marketing-refused leads NEVER force
//     opportunity creation.
//  4. Individual customers do NOT require a company field.
//  5. AI score NEVER triggers automatic outbound calling.
func ValidateTodayAction(in TodayActionInput) TodayActionDecision {
	tenantID := strings.TrimSpace(in.TenantID)
	var violations []string
	add := func(v string) {
		for _, existing := range violations {
			if existing == v {
				return
			}
		}
		violations = append(violations, v)
	}

	if tenantID == "" {
		add("missing_tenant_id")
	}

	consentStatus := strings.TrimSpace(strings.ToLower(in.ConsentStatus))
	revoked := in.ConsentRevoked || consentStatus == "revoked" || consentStatus == "denied"
	marketingAllowed := in.MarketingAllowed && !revoked

	if in.Consent != nil {
		if strings.TrimSpace(in.Consent.TenantID) != tenantID {
			marketingAllowed = false
			add("consent_tenant_mismatch")
		}
		if in.Consent.Revoked {
			revoked = true
			marketingAllowed = false
		}
		if !in.Consent.MarketingAllowed {
			marketingAllowed = false
		}
	}

	outreachNotice := workbench.OutreachNotice(marketingAllowed, in.SendReceiptStored)
	if revoked {
		outreachNotice = "营销许可已撤销，已禁止自动触达"
	}

	// 1. Missing/revoked marketing consent blocks auto-outreach & auto-SOP, and
	//    NEVER allows claiming auto-outreach success.
	if in.ClaimAutoOutreachSuccess {
		if revoked {
			add("marketing_consent_revoked")
			add("auto_outreach_success_forbidden")
		} else if !marketingAllowed {
			add("marketing_consent_required")
			add("auto_outreach_success_forbidden")
		} else if !in.SendReceiptStored {
			add("send_receipt_required")
			add("auto_outreach_success_forbidden")
		}
	}

	if in.Action == ActionAutoOutreach || in.TriggerAutoOutbound {
		if revoked {
			add("marketing_consent_revoked")
		} else if !marketingAllowed {
			add("marketing_consent_required")
		} else if in.Consent != nil && strings.TrimSpace(in.Consent.NoticeVersion) == "" {
			add("marketing_consent_required")
		}
	}

	if in.Action == ActionAutoSOP || in.TriggerAutoSOP {
		if revoked {
			add("marketing_consent_revoked")
		} else if !marketingAllowed {
			add("marketing_consent_required")
		} else if in.Consent != nil {
			dec := receptionguard.GuardScopeBoundary(receptionguard.ScopeBoundaryInput{
				TenantID:     tenantID,
				SessionScope: receptionguard.ScopeCRMReception,
				Action:       receptionguard.BoundaryActionTriggerSOP,
				Consent:      in.Consent,
			})
			if !dec.Allowed {
				for _, v := range dec.Violations {
					add(v)
				}
			}
		}
	}

	// 2. AI score NEVER triggers auto-calling on the Today/Next surface.
	if in.Action == ActionAutoCall || in.TriggerAutoCall {
		add("ai_score_auto_call_forbidden")
		if revoked {
			add("marketing_consent_revoked")
		} else if !marketingAllowed {
			add("marketing_consent_required")
		}
	}

	// 3. Ordinary inquiry / after-sales / marketing_refused NEVER force opportunity creation.
	if in.ForceOpportunity || in.Action == ActionCreateOpportunity {
		if IsNonSalesPurpose(in.Purpose) {
			add("ordinary_inquiry_no_forced_opportunity")
		} else if in.ForceOpportunity {
			add("forced_opportunity_forbidden")
		}
	}

	// 4. Individual customers do NOT require a company field.
	companyRequired := in.CustomerType == CustomerBusiness && in.RequireCompany
	if in.CustomerType == CustomerIndividual && in.RequireCompany {
		add("individual_customer_company_not_required")
		companyRequired = false
	}

	// 5. In-channel follow-up still requires an authorized channel identity.
	if in.Action == ActionChannelFollowUp {
		if strings.TrimSpace(in.ChannelIdentity) == "" || !in.ChannelReplyAllowed {
			add("channel_reply_not_authorized")
		}
	}

	if len(violations) > 0 {
		return TodayActionDecision{
			Allowed:             false,
			Code:                violations[0],
			Reason:              violationReason(violations[0]),
			Violations:          violations,
			OutreachNotice:      outreachNotice,
			AutoOutreachSuccess: false,
			AllowAutoCall:       false,
			AllowAutoSOP:        false,
			ForceOpportunity:    false,
			CompanyRequired:     companyRequired,
		}
	}

	return TodayActionDecision{
		Allowed:             true,
		Code:                "ok",
		Reason:              "allowed",
		OutreachNotice:      outreachNotice,
		AutoOutreachSuccess: false,
		AllowAutoCall:       false,
		AllowAutoSOP:        marketingAllowed && !revoked,
		ForceOpportunity:    false,
		CompanyRequired:     companyRequired,
	}
}

func violationReason(code string) string {
	switch code {
	case "missing_tenant_id":
		return "缺少租户上下文，拒绝执行动作"
	case "consent_tenant_mismatch":
		return "许可记录与当前租户不匹配"
	case "marketing_consent_revoked":
		return "营销许可已撤销，禁止自动外发或自动 SOP"
	case "marketing_consent_required":
		return "缺少有效营销许可，禁止自动外发或自动 SOP"
	case "send_receipt_required":
		return "缺少真实发送回执，不能宣称自动触达已成功"
	case "auto_outreach_success_forbidden":
		return "未满足营销许可与真实回执，绝不展示自动触达已成功"
	case "ai_score_auto_call_forbidden":
		return "AI 分数绝不触发自动外呼"
	case "ordinary_inquiry_no_forced_opportunity":
		return "普通咨询、售后或拒绝营销绝不强制创建商机"
	case "forced_opportunity_forbidden":
		return "工作台不强制自动创建商机"
	case "individual_customer_company_not_required":
		return "个人客户不需要填写公司字段"
	case "channel_reply_not_authorized":
		return "缺少授权的渠道标识，不能安排渠道内跟进"
	default:
		return "动作违反 Today/Next 安全与许可边界"
	}
}
