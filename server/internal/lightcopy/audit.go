package lightcopy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	priceClaimRe = regexp.MustCompile(`(?:[¥￥]\s*\d+(?:\.\d+)?|\d+(?:\.\d+)?\s*(?:元|块|折)|(?:立减|优惠|直降|特价|折后)\s*\d+(?:\.\d+)?)`)
)

var ungroundedPromiseMarkers = []string{
	"100%保证",
	"百分百保证",
	"绝对最低价",
	"全网最低",
	"保底成交",
	"库存仅剩",
	"现已发货",
	"已自动发送",
	"已正式发布",
	"已取得营销许可",
}

var aggressiveToneMarkers = []string{
	"不买就亏",
	"最后一天错过再无",
	"强制下单",
	"立刻转账",
}

var ctaMarkers = []string{
	"欢迎", "回复", "预约", "到店", "咨询", "了解", "联系", "确认", "沟通", "查看", "体验", "安排",
}

var honestUnknownMarkers = []string{
	"待确认", "待核实", "核实", "最新报价", "为准", "确认后", "未知", "另行确认", "沟通确认",
}

// CopyAuditReport records factual coverage, unsupported additions, placeholder
// detection, PII leak check, tone, and CTA verification for a generated draft.
type CopyAuditReport struct {
	SampleCategory      string   `json:"sample_category,omitempty"`
	Passed              bool     `json:"passed"`
	BlockingReason      string   `json:"blocking_reason,omitempty"`
	HasPlaceholder      bool     `json:"has_placeholder"`
	HasPIILeak          bool     `json:"has_pii_leak"`
	PIILeakKind         string   `json:"pii_leak_kind,omitempty"`
	OmittedFacts        []string `json:"omitted_facts,omitempty"`
	UnsupportedClaims   []string `json:"unsupported_claims,omitempty"`
	UnknownFields       []string `json:"unknown_fields,omitempty"`
	UnknownAcknowledged bool     `json:"unknown_acknowledged"`
	ToneOK              bool     `json:"tone_ok"`
	HasCTA              bool     `json:"has_cta"`
}

// AuditGeneratedCopy verifies generated copy against the projected minimal
// brief across campaign/product/service samples:
//   - refuses template placeholders (BlockingReason = placeholder_refused)
//   - refuses PII leakage (BlockingReason = pii_detected)
//   - refuses invented prices when price_cents is absent or marked unknown
//     (BlockingReason = ungrounded_claim_refused)
//   - refuses absolute/delivery claims not present in confirmed facts
//     (BlockingReason = ungrounded_claim_refused)
//   - reports omitted confirmed facts, tone compliance, and CTA presence.
func AuditGeneratedCopy(brief BriefProjection, generatedText string) CopyAuditReport {
	text := strings.TrimSpace(generatedText)
	report := CopyAuditReport{
		SampleCategory:      brief.SampleCategory,
		Passed:              true,
		UnknownFields:       append([]string(nil), brief.UnknownFields...),
		UnknownAcknowledged: len(brief.UnknownFields) == 0,
		ToneOK:              true,
	}

	if isPlaceholder(text) {
		report.HasPlaceholder = true
		report.Passed = false
		report.BlockingReason = ReasonPlaceholder
	}
	if hasPII, kind := ContainsSensitivePII(text); hasPII {
		report.HasPIILeak = true
		report.PIILeakKind = kind
		report.Passed = false
		if report.BlockingReason == "" {
			report.BlockingReason = ReasonPIIDetected
		}
	}

	// Build a corpus of confirmed fact strings so claims explicitly backed by
	// confirmed facts are not falsely flagged as unsupported.
	var factCorpus strings.Builder
	keys := make([]string, 0, len(brief.Facts))
	for k := range brief.Facts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&factCorpus, " %v ", brief.Facts[k])
	}
	corpus := factCorpus.String()

	// 1. Check omitted confirmed facts.
	for _, k := range keys {
		switch k {
		case "price_cents":
			cents, ok := toInt64(brief.Facts[k])
			if !ok {
				continue
			}
			if !containsPrice(text, cents) {
				report.OmittedFacts = append(report.OmittedFacts, "price_cents")
			}
		case "title", "activity_ref", "scene", "offer", "next_step":
			val := strings.TrimSpace(fmt.Sprintf("%v", brief.Facts[k]))
			if val == "" {
				continue
			}
			if !factMentioned(text, val) {
				report.OmittedFacts = append(report.OmittedFacts, k)
			}
		}
	}

	// 2. Check ungrounded price claims when no confirmed non-expired price is in Facts.
	_, hasValidPrice := brief.Facts["price_cents"]
	if !hasValidPrice {
		// Allow price mentions only if the exact price token already appeared in a confirmed text fact (e.g. offer).
		matches := priceClaimRe.FindAllString(text, -1)
		for _, m := range matches {
			trimmed := strings.TrimSpace(m)
			if trimmed != "" && !strings.Contains(corpus, trimmed) {
				report.UnsupportedClaims = append(report.UnsupportedClaims, "ungrounded_price:"+trimmed)
				report.Passed = false
				if report.BlockingReason == "" {
					report.BlockingReason = ReasonUngroundedClaim
				}
			}
		}
	}

	// 3. Check ungrounded absolute/delivery/inventory promises.
	for _, marker := range ungroundedPromiseMarkers {
		if strings.Contains(text, marker) && !strings.Contains(corpus, marker) {
			report.UnsupportedClaims = append(report.UnsupportedClaims, "ungrounded_promise:"+marker)
			report.Passed = false
			if report.BlockingReason == "" {
				report.BlockingReason = ReasonUngroundedClaim
			}
		}
	}

	// 4. Unknown field honesty check.
	if len(brief.UnknownFields) > 0 {
		for _, m := range honestUnknownMarkers {
			if strings.Contains(text, m) {
				report.UnknownAcknowledged = true
				break
			}
		}
	}

	// 5. Tone check.
	for _, m := range aggressiveToneMarkers {
		if strings.Contains(text, m) {
			report.ToneOK = false
			break
		}
	}

	// 6. CTA check.
	for _, m := range ctaMarkers {
		if strings.Contains(text, m) {
			report.HasCTA = true
			break
		}
	}
	if !report.HasCTA {
		if nextStep, ok := brief.Facts["next_step"].(string); ok && strings.TrimSpace(nextStep) != "" && factMentioned(text, nextStep) {
			report.HasCTA = true
		}
	}

	return report
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func containsPrice(text string, cents int64) bool {
	if strings.Contains(text, fmt.Sprintf("%d", cents)) {
		return true
	}
	yuanWhole := cents / 100
	rem := cents % 100
	if rem == 0 {
		return strings.Contains(text, fmt.Sprintf("%d", yuanWhole))
	}
	return strings.Contains(text, fmt.Sprintf("%d.%02d", yuanWhole, rem)) ||
		strings.Contains(text, strings.TrimRight(fmt.Sprintf("%d.%02d", yuanWhole, rem), "0"))
}

// factMentioned checks whether a confirmed fact value (or its main non-trivial
// token) appears in generated copy.
func factMentioned(text, val string) bool {
	if strings.Contains(text, val) {
		return true
	}
	// Tokenize on common punctuation / whitespace and require at least one
	// meaningful segment (>=2 runes) to be present when full phrase is reworded.
	fields := strings.FieldsFunc(val, func(r rune) bool {
		return r == ' ' || r == '，' || r == ',' || r == '。' || r == '、' || r == '/' || r == '-'
	})
	matched := 0
	candidates := 0
	for _, tok := range fields {
		tok = strings.TrimSpace(tok)
		if len([]rune(tok)) < 2 {
			continue
		}
		candidates++
		if strings.Contains(text, tok) {
			matched++
		}
	}
	return candidates > 0 && matched > 0
}
