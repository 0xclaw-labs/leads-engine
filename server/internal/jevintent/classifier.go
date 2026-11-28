package jevintent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	GradeHigh         = "high"
	GradeMedium       = "medium"
	GradeLow          = "low"
	GradeInsufficient = "insufficient"

	SourceFastPath            = "fast_path"
	SourceJEV                 = "jev"
	SourceFallbackRules       = "fallback_rules"
	SourceShadowFallbackRules = "shadow_fallback_rules"

	DefaultHandoffThreshold = 0.65
)

// EvalRequest is the input to the Hybrid Fast-Path + JEV intent classifier.
type EvalRequest struct {
	SubjectKind    string
	Text           string
	EvidenceCount  int
	FallbackGrade  string // deterministic rule baseline grade (high/medium/low/insufficient)
	FallbackReason string // deterministic rule baseline reason
}

// EvalResult is the structured classification & human-handoff routing decision.
type EvalResult struct {
	Grade               string             `json:"grade"`
	Confidence          float64            `json:"confidence,omitempty"`
	Probabilities       map[string]float64 `json:"probabilities,omitempty"`
	NeedsHumanHandoff   bool               `json:"needs_human_handoff"`
	HandoffProbability  float64            `json:"handoff_probability,omitempty"`
	Source              string             `json:"source"`
	Reason              string             `json:"reason,omitempty"`
	Model               string             `json:"model,omitempty"`
	CacheHit            bool               `json:"cache_hit,omitempty"`
	SingleflightShared  bool               `json:"singleflight_shared,omitempty"`
	ShadowMode          bool               `json:"shadow_mode,omitempty"`
	ShadowGrade         string             `json:"shadow_grade,omitempty"`
	ShadowConfidence    float64            `json:"shadow_confidence,omitempty"`
	ShadowNeedsHuman    bool               `json:"shadow_needs_human,omitempty"`
	ShadowHandoffProb   float64            `json:"shadow_handoff_probability,omitempty"`
	CircuitOpen         bool               `json:"circuit_open,omitempty"`
	FallbackReason      string             `json:"fallback_reason,omitempty"`
	LatencyMs           int64              `json:"latency_ms,omitempty"`
}

// Config configures the Hybrid Fast-Path + JEV Classifier.
type Config struct {
	Endpoint         string
	APIKey           string
	Model            string
	Timeout          time.Duration
	HandoffThreshold float64
	ShadowMode       bool
	CacheCapacity    int
	CacheTTL         time.Duration
	Breaker          BreakerConfig
	Logger           *log.Logger
	NowFn            func() time.Time
}

// Classifier performs <1ms deterministic Fast-Path classification and routes
// gray-zone conversations to JEV (/v1/systemone) with singleflight, LRU cache,
// sliding-window circuit breaker, Shadow Mode, and 100% Fail-Safe fallback.
type Classifier struct {
	client           *Client
	model            string
	handoffThreshold float64
	shadowMode       bool
	cache            *lruCache
	sf               singleflightGroup
	breaker          *slidingBreaker
	logger           *log.Logger
	nowFn            func() time.Time
}

// NewClassifier creates a Hybrid Fast-Path + JEV classifier.
func NewClassifier(cfg Config) *Classifier {
	nowFn := cfg.NowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultModel
	}
	threshold := cfg.HandoffThreshold
	if threshold <= 0 || threshold >= 1 {
		threshold = DefaultHandoffThreshold
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Classifier{
		client:           NewClient(cfg.Endpoint, cfg.APIKey, model, cfg.Timeout),
		model:            model,
		handoffThreshold: threshold,
		shadowMode:       cfg.ShadowMode,
		cache:            newLRUCache(cfg.CacheCapacity, cfg.CacheTTL, nowFn),
		breaker:          newSlidingBreaker(cfg.Breaker, nowFn),
		logger:           logger,
		nowFn:            nowFn,
	}
}

// BreakerState exposes the current circuit breaker state for diagnostics/tests.
func (c *Classifier) BreakerState() BreakerState {
	return c.breaker.State()
}

// CacheLen exposes current LRU cache occupancy for diagnostics/tests.
func (c *Classifier) CacheLen() int {
	return c.cache.Len()
}

// Evaluate runs Hybrid Fast-Path first (<1ms); only gray-zone semantic text hits JEV.
// Every error/timeout/open-circuit path fails safe to the rule baseline.
func (c *Classifier) Evaluate(ctx context.Context, req EvalRequest) EvalResult {
	start := c.nowFn()
	fallbackGrade := normalizeGrade(req.FallbackGrade)
	fallbackReason := strings.TrimSpace(req.FallbackReason)

	// Stage 1: Deterministic Fast-Path (<1ms, zero network calls).
	if fp, hit := matchFastPath(req.Text); hit {
		fp.LatencyMs = maxInt64(0, c.nowFn().Sub(start).Milliseconds())
		return fp
	}

	// Stage 2: Gray-zone semantic evaluation via JEV (/v1/systemone).
	cacheKey := buildCacheKey(c.model, req.SubjectKind, req.Text)
	if cached, ok := c.cache.Get(cacheKey); ok {
		cached.CacheHit = true
		cached.LatencyMs = maxInt64(0, c.nowFn().Sub(start).Milliseconds())
		return c.finalize(cached, fallbackGrade, fallbackReason)
	}

	if !c.breaker.Allow() {
		return EvalResult{
			Grade:          fallbackGrade,
			Source:         SourceFallbackRules,
			Reason:         fallbackReason,
			Model:          c.model,
			ShadowMode:     c.shadowMode,
			CircuitOpen:    true,
			FallbackReason: "circuit_open",
			LatencyMs:      maxInt64(0, c.nowFn().Sub(start).Milliseconds()),
		}
	}

	res, err, shared := c.sf.Do(cacheKey, func() (EvalResult, error) {
		// Double-check cache inside singleflight in case a peer just populated it.
		if cached, ok := c.cache.Get(cacheKey); ok {
			cached.CacheHit = true
			return cached, nil
		}
		eval, callErr := c.callJEV(ctx, req)
		if callErr != nil {
			c.breaker.Record(false)
			return EvalResult{}, callErr
		}
		c.breaker.Record(true)
		c.cache.Set(cacheKey, eval)
		return eval, nil
	})
	elapsed := maxInt64(0, c.nowFn().Sub(start).Milliseconds())
	if err != nil {
		c.logger.Printf("jevintent: fail-safe fallback to rules (model=%s latency_ms=%d breaker=%s err=%v)",
			c.model, elapsed, c.breaker.State(), err)
		return EvalResult{
			Grade:          fallbackGrade,
			Source:         SourceFallbackRules,
			Reason:         fallbackReason,
			Model:          c.model,
			ShadowMode:     c.shadowMode,
			CircuitOpen:    c.breaker.State() == BreakerOpen,
			FallbackReason: err.Error(),
			LatencyMs:      elapsed,
		}
	}

	res.SingleflightShared = shared
	res.LatencyMs = elapsed
	return c.finalize(res, fallbackGrade, fallbackReason)
}

func (c *Classifier) finalize(jevRes EvalResult, fallbackGrade, fallbackReason string) EvalResult {
	if !c.shadowMode {
		return jevRes
	}
	c.logger.Printf("jevintent: shadow eval rule_grade=%s jev_grade=%s jev_conf=%.2f needs_human=%t noul=%.2f latency_ms=%d",
		fallbackGrade, jevRes.Grade, jevRes.Confidence, jevRes.NeedsHumanHandoff, jevRes.HandoffProbability, jevRes.LatencyMs)
	return EvalResult{
		Grade:              fallbackGrade,
		Source:             SourceShadowFallbackRules,
		Reason:             fallbackReason,
		Model:              jevRes.Model,
		CacheHit:           jevRes.CacheHit,
		SingleflightShared: jevRes.SingleflightShared,
		ShadowMode:         true,
		ShadowGrade:        jevRes.Grade,
		ShadowConfidence:   jevRes.Confidence,
		ShadowNeedsHuman:   jevRes.NeedsHumanHandoff,
		ShadowHandoffProb:  jevRes.HandoffProbability,
		LatencyMs:          jevRes.LatencyMs,
	}
}

func (c *Classifier) callJEV(ctx context.Context, req EvalRequest) (EvalResult, error) {
	subjectKind := strings.TrimSpace(req.SubjectKind)
	if subjectKind == "" {
		subjectKind = "lead"
	}
	evCount := req.EvidenceCount
	if evCount <= 0 {
		evCount = 1
	}

	sysReq := SystemOneRequest{
		Model: c.model,
		State: map[string]any{
			"subject_kind":   subjectKind,
			"evidence_count": evCount,
			"conversation":   truncateRunes(strings.TrimSpace(req.Text), 2000),
		},
		Questions: map[string]QuestionSpec{
			"intent_grade": {
				Type:         "choice",
				Instructions: "Classify the commercial purchase intent level of this customer lead or reception conversation into high, medium, or low.",
				Criteria: map[string]string{
					"high":   "Customer shows strong buying readiness, confirms budget/timeline/quantity, requests quote/contract/demo to close soon, or leaves contact info for immediate follow-up.",
					"medium": "Customer is evaluating options, asking about pricing, features, comparisons, cases, or deployment feasibility without a firm purchase commitment yet.",
					"low":    "General FAQ inquiry, casual browsing, unrelated chatter, pure after-sales support, or explicit disinterest/refusal.",
				},
			},
			"needs_human_handoff": {
				Type:         "noul",
				Instructions: "Does this conversation require timely human sales or specialist handoff?",
				Criteria: map[string]string{
					"true":  "Customer has complex custom requirements, pricing/contract negotiation, strong purchase intent, complaint/escalation, or explicitly asks for a human representative.",
					"false": "Simple routine FAQ, casual low-intent browsing, or customer explicitly asks not to be contacted.",
				},
			},
		},
	}

	resp, err := c.client.Evaluate(ctx, sysReq)
	if err != nil {
		return EvalResult{}, err
	}

	gradeAns, ok := resp.Answers["intent_grade"]
	if !ok {
		return EvalResult{}, fmt.Errorf("jevintent: missing intent_grade answer")
	}
	grade := strings.ToLower(strings.TrimSpace(gradeAns.Choice))
	switch grade {
	case GradeHigh, GradeMedium, GradeLow:
	default:
		return EvalResult{}, fmt.Errorf("jevintent: invalid choice %q", gradeAns.Choice)
	}

	var handoffProb float64
	var needsHuman bool
	if handoffAns, ok := resp.Answers["needs_human_handoff"]; ok && handoffAns.Noul != nil {
		handoffProb = *handoffAns.Noul
		if handoffProb < 0 || handoffProb > 1 {
			return EvalResult{}, fmt.Errorf("jevintent: out-of-range noul %.4f", handoffProb)
		}
		needsHuman = handoffProb >= c.handoffThreshold
	}

	modelUsed := strings.TrimSpace(resp.Model)
	if modelUsed == "" {
		modelUsed = c.model
	}

	reason := fmt.Sprintf("JEV 语义意向分级=%s(confidence=%.2f)", grade, gradeAns.Confidence)
	if needsHuman {
		reason += fmt.Sprintf("，建议转人工跟进(noul=%.2f)", handoffProb)
	}

	return EvalResult{
		Grade:              grade,
		Confidence:         gradeAns.Confidence,
		Probabilities:      cloneProbMap(gradeAns.Probabilities),
		NeedsHumanHandoff:  needsHuman,
		HandoffProbability: handoffProb,
		Source:             SourceJEV,
		Reason:             reason,
		Model:              modelUsed,
	}, nil
}

// --- Fast-Path deterministic rules (<1ms, zero network) ----------------------

var (
	// China 11-digit mobile phone number not embedded inside a longer digit string.
	cnMobileRe = regexp.MustCompile(`(?:^|[^0-9])(1[3-9][0-9]{9})(?:[^0-9]|$)`)
	// Explicit WeChat handle sharing e.g. "微信：zhangsan_88" or "加我vx: abc12345"
	wechatIDRe = regexp.MustCompile(`(?i)(?:微信|微信号|vx|v信)\s*[:：是叫]?\s*[a-z0-9_-]{5,}`)
)

func matchFastPath(text string) (EvalResult, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return EvalResult{
			Grade:             GradeInsufficient,
			Confidence:        1.0,
			NeedsHumanHandoff: false,
			Source:            SourceFastPath,
			Reason:            "fast_path: 空文本，信息不足。",
		}, true
	}

	folded := foldText(trimmed)

	// 1) Explicit marketing refusal / do-not-contact (highest safety priority).
	if containsAny(folded,
		"不要再打", "别打电话", "不要发短信", "别发短信", "不需要",
		"别联系", "拒绝营销", "退订", "拉黑", "别骚扰", "不要联系",
	) {
		return EvalResult{
			Grade:             GradeLow,
			Confidence:        1.0,
			NeedsHumanHandoff: false,
			Source:            SourceFastPath,
			Reason:            "fast_path: 客户明确拒绝营销或联系。",
		}, true
	}

	// 2) Explicit lead capture (mobile phone / WeChat) or firm ordering commitment.
	hasPhone := cnMobileRe.MatchString(trimmed)
	hasWeChat := wechatIDRe.MatchString(trimmed) || containsAny(folded, "加我微信", "加个微信", "留个电话", "留电话", "打我电话")
	hasOrderCommit := containsAny(folded,
		"采购", "发合同", "签合同", "签约", "下单", "预算已批",
		"今天打款", "打款", "立即购买", "马上买", "付定金", "交定金", "转账",
	)
	if hasPhone || hasWeChat || hasOrderCommit {
		reason := "fast_path: 明确下单或购买承诺，需人工优先跟进。"
		if hasPhone || hasWeChat {
			reason = "fast_path: 客户主动留资（手机号/微信），高意向需人工及时跟进。"
		}
		return EvalResult{
			Grade:             GradeHigh,
			Confidence:        1.0,
			NeedsHumanHandoff: true,
			HandoffProbability: 1.0,
			Source:            SourceFastPath,
			Reason:            reason,
		}, true
	}

	// 3) Explicit after-sales / refund / complaint (not new purchase, but needs human).
	if containsAny(folded, "维修", "坏了", "售后", "退货", "退款", "保修", "投诉") {
		return EvalResult{
			Grade:              GradeLow,
			Confidence:         1.0,
			NeedsHumanHandoff:  true,
			HandoffProbability: 1.0,
			Source:             SourceFastPath,
			Reason:             "fast_path: 售后/退款/投诉诉求，非新购意向，建议转人工处理。",
		}, true
	}

	return EvalResult{}, false
}

func buildCacheKey(model, subjectKind, text string) string {
	h := sha256.New()
	h.Write([]byte(model))
	h.Write([]byte("|"))
	h.Write([]byte(strings.TrimSpace(subjectKind)))
	h.Write([]byte("|"))
	h.Write([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(h.Sum(nil))
}

func normalizeGrade(g string) string {
	switch strings.ToLower(strings.TrimSpace(g)) {
	case GradeHigh, GradeMedium, GradeLow, GradeInsufficient:
		return strings.ToLower(strings.TrimSpace(g))
	default:
		return GradeInsufficient
	}
}

func foldText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func containsAny(blob string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(blob, foldText(n)) {
			return true
		}
	}
	return false
}

func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

func cloneProbMap(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
