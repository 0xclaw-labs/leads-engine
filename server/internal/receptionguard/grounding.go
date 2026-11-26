package receptionguard

import (
	"regexp"
	"strings"
	"time"
)

const (
	ActionAllow             = "allow"
	ActionClarifyOrEscalate = "clarify_or_escalate"

	FactPrice       = "price"
	FactInventory   = "inventory"
	FactOrderStatus = "order_status"
	FactSLAPromise  = "sla_promise"

	SafeClarifyFallback = "当前缺少已核实的有效依据，无法直接答复或作出承诺，已转为澄清或人工核对。"
)

// KnowledgeCitation represents one knowledge base entry backing a candidate reply.
type KnowledgeCitation struct {
	ID        string
	TenantID  string
	Kind      string // faq, policy, sla_policy, kb_doc
	Title     string
	Snippet   string
	Version   int
	Active    bool
	Withdrawn bool
	UpdatedAt time.Time
	ExpiresAt time.Time // zero means no expiry; non-zero must be strictly after Now
	CoversSLA bool
}

// ToolFact represents a real-time fact fetched from an authorized read-only business tool.
type ToolFact struct {
	ID         string
	TenantID   string
	Kind       string // price, inventory, order_status, sla_promise
	Value      string
	Authorized bool
	ReadOnly   bool
	TimedOut   bool
	ExpiresAt  time.Time // required non-zero and strictly after Now
}

// GroundingInput is evaluated before any AI or assisted reply may be approved or delivered.
type GroundingInput struct {
	TenantID      string
	SessionID     string
	VisitorText   string
	CandidateBody string
	Citations     []KnowledgeCitation
	Facts         []ToolFact
	UntrustedDocs []string
	Now           time.Time
}

// GroundingDecision is the fail-closed output of ValidateGroundedReply.
type GroundingDecision struct {
	Allowed           bool
	Action            string
	MissingFacts      []string
	Violations        []string
	ValidCitations    []KnowledgeCitation
	ValidFacts        []ToolFact
	InjectionDetected bool
	SafeFallbackText  string
}

var (
	rePriceAmount   = regexp.MustCompile(`[0-9０-９]+(?:\.[0-9０-９]+)?\s*元`)
	reChinesePrice  = regexp.MustCompile(`[零〇一二三四五六七八九十百千万两壹贰叁肆伍陆柒捌玖拾佰仟]+\s*元`)
	reCurrencyPrice = regexp.MustCompile(`[¥￥$]\s*[0-9０-９]`)
)

// ValidateGroundedReply enforces knowledge citation, live fact grounding,
// non-fabrication, cross-tenant citation rejection, and prompt-injection /
// privilege-escalation blocking.
func ValidateGroundedReply(in GroundingInput) GroundingDecision {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tenantID := strings.TrimSpace(in.TenantID)
	body := strings.TrimSpace(in.CandidateBody)

	dec := GroundingDecision{
		Action: ActionAllow,
	}
	block := func() GroundingDecision {
		dec.Allowed = false
		dec.Action = ActionClarifyOrEscalate
		dec.SafeFallbackText = SafeClarifyFallback
		return dec
	}

	if tenantID == "" {
		dec.Violations = appendUnique(dec.Violations, "missing_tenant_id")
		return block()
	}
	if body == "" {
		dec.Violations = appendUnique(dec.Violations, "empty_candidate_body")
		dec.MissingFacts = appendUnique(dec.MissingFacts, "knowledge_citation")
		return block()
	}

	// 1. Detect prompt injection / privilege escalation / unauthorized write requests.
	if hasUnauthorizedWriteIntent(in.VisitorText) || hasUnauthorizedWriteIntent(body) {
		dec.InjectionDetected = true
		dec.Violations = appendUnique(dec.Violations, "unauthorized_write_action")
	}
	if hasPrivilegeOrCrossTenantInjection(in.VisitorText) {
		dec.InjectionDetected = true
		dec.Violations = appendUnique(dec.Violations, "prompt_injection_attempt")
	}
	for _, doc := range in.UntrustedDocs {
		if hasPrivilegeOrCrossTenantInjection(doc) || hasUnauthorizedWriteIntent(doc) {
			dec.InjectionDetected = true
			dec.Violations = appendUnique(dec.Violations, "untrusted_doc_privilege_escalation")
		}
	}

	// 2. Validate citations: must be same-tenant, active, not withdrawn, non-expired.
	// Any cross-tenant citation is a hard fail-closed block.
	hardBlock := false
	for _, c := range in.Citations {
		if strings.TrimSpace(c.TenantID) == "" || strings.TrimSpace(c.TenantID) != tenantID {
			dec.Violations = appendUnique(dec.Violations, "cross_tenant_citation")
			hardBlock = true
			continue
		}
		if hasPrivilegeOrCrossTenantInjection(c.Snippet) {
			dec.InjectionDetected = true
			dec.Violations = appendUnique(dec.Violations, "untrusted_doc_privilege_escalation")
			hardBlock = true
			continue
		}
		if c.Withdrawn {
			dec.Violations = appendUnique(dec.Violations, "withdrawn_citation")
			continue
		}
		if !c.Active {
			dec.Violations = appendUnique(dec.Violations, "inactive_citation")
			continue
		}
		if !c.ExpiresAt.IsZero() && !c.ExpiresAt.After(now) {
			dec.Violations = appendUnique(dec.Violations, "expired_citation")
			continue
		}
		if strings.TrimSpace(c.ID) == "" {
			dec.Violations = appendUnique(dec.Violations, "invalid_citation_id")
			continue
		}
		dec.ValidCitations = append(dec.ValidCitations, c)
	}

	// 3. Validate read-only business tool facts: same-tenant, authorized, read-only, non-expired.
	validFactByKind := make(map[string]ToolFact)
	for _, f := range in.Facts {
		if strings.TrimSpace(f.TenantID) == "" || strings.TrimSpace(f.TenantID) != tenantID {
			dec.Violations = appendUnique(dec.Violations, "cross_tenant_fact")
			hardBlock = true
			continue
		}
		if !f.Authorized {
			dec.Violations = appendUnique(dec.Violations, "unauthorized_tool_fact")
			hardBlock = true
			continue
		}
		if !f.ReadOnly {
			dec.Violations = appendUnique(dec.Violations, "non_readonly_tool_fact")
			hardBlock = true
			continue
		}
		if f.TimedOut {
			dec.Violations = appendUnique(dec.Violations, "fact_timeout")
			continue
		}
		if f.ExpiresAt.IsZero() || !f.ExpiresAt.After(now) {
			dec.Violations = appendUnique(dec.Violations, "expired_fact")
			continue
		}
		if strings.TrimSpace(f.Value) == "" {
			dec.Violations = appendUnique(dec.Violations, "empty_fact_value")
			continue
		}
		kind := strings.TrimSpace(strings.ToLower(f.Kind))
		dec.ValidFacts = append(dec.ValidFacts, f)
		validFactByKind[kind] = f
	}

	if hardBlock || dec.InjectionDetected {
		return block()
	}

	// 4. Detect required factual claims from both visitor question and candidate body.
	requiredFacts := detectFactualClaims(in.VisitorText + "\n" + body)
	for _, kind := range requiredFacts {
		switch kind {
		case FactPrice, FactInventory, FactOrderStatus:
			// Live transactional claims MUST come from an authorized, non-expired read-only ToolFact.
			if _, ok := validFactByKind[kind]; !ok {
				dec.MissingFacts = appendUnique(dec.MissingFacts, kind)
			}
		case FactSLAPromise:
			_, hasFact := validFactByKind[FactSLAPromise]
			if !hasFact && !hasValidSLACitation(dec.ValidCitations) {
				dec.MissingFacts = appendUnique(dec.MissingFacts, FactSLAPromise)
			}
		}
	}

	// 5. Ordinary replies still require at least one valid same-tenant active citation or tool fact.
	if len(requiredFacts) == 0 && len(dec.ValidCitations) == 0 && len(dec.ValidFacts) == 0 {
		dec.MissingFacts = appendUnique(dec.MissingFacts, "knowledge_citation")
	}

	if len(dec.MissingFacts) > 0 {
		return block()
	}

	dec.Allowed = true
	dec.Action = ActionAllow
	return dec
}

func hasValidSLACitation(citations []KnowledgeCitation) bool {
	for _, c := range citations {
		if c.CoversSLA || strings.EqualFold(strings.TrimSpace(c.Kind), "sla_policy") {
			return true
		}
	}
	return false
}

func detectFactualClaims(text string) []string {
	var kinds []string
	n := foldText(text)

	if rePriceAmount.MatchString(text) || reChinesePrice.MatchString(text) || reCurrencyPrice.MatchString(text) ||
		containsAny(n, "价格", "价目", "报价", "价钱", "费用", "收费", "单价", "标价", "多少钱", "售价", "优惠价", "折后价", "price", "cost", "fee", "quote") {
		kinds = appendUnique(kinds, FactPrice)
	}
	if containsAny(n, "库存", "还有货", "有货", "缺货", "现货", "余量", "inventory", "stock", "instock", "outofstock") {
		kinds = appendUnique(kinds, FactInventory)
	}
	if containsAny(n, "订单状态", "物流", "运单", "订单号", "已发货", "发货了吗", "配送中", "已签收", "orderstatus", "tracking", "shipment") {
		kinds = appendUnique(kinds, FactOrderStatus)
	}
	if containsAny(n, "承诺", "保证", "必达", "赔付", "赔偿", "违约金", "100%", "百分百", "永久免费", "保底", "小时内必", "分钟内必", "sla", "guaranteed", "guarantee", "promise") {
		kinds = appendUnique(kinds, FactSLAPromise)
	}
	return kinds
}

func hasPrivilegeOrCrossTenantInjection(text string) bool {
	n := foldText(text)
	return containsAny(n,
		"ignoreprevious", "ignoreallinstructions", "systemprompt", "revealprompt",
		"忽略之前", "忽略以上", "你现在是管理员", "你现在是系统", "切换租户", "跨租户", "切到租户",
		"switchtenant", "crosstenant", "overridetenant", "othermerchant",
		"setrole", "grantadmin", "escalateprivilege", "bypassguard",
		"droptable", "toolcall", "functioncall", "executesql",
	)
}

func hasUnauthorizedWriteIntent(text string) bool {
	n := foldText(text)
	return containsAny(n,
		"帮我退款", "我要退款", "直接退款", "立刻退款", "给我改价", "帮我改价", "直接改价",
		"取消订单", "删除订单", "修改权限", "提升权限",
		"refundnow", "issueredund", "issuerefund", "chargeback", "modifyprice", "cancelorder",
	)
}

func foldText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func appendUnique(list []string, item string) []string {
	for _, existing := range list {
		if existing == item {
			return list
		}
	}
	return append(list, item)
}
