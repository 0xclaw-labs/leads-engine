package jevintent

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFastPathZeroNetworkDeterministic(t *testing.T) {
	var upstreamCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	clf := NewClassifier(Config{
		Endpoint: srv.URL,
		APIKey:   "test-key",
		Logger:   log.New(io.Discard, "", 0),
	})

	cases := []struct {
		name            string
		text            string
		wantGrade       string
		wantNeedsHuman  bool
	}{
		{
			name:           "mobile phone lead capture -> high + human",
			text:           "有兴趣，我的手机号是13812345678，方便联系我",
			wantGrade:      GradeHigh,
			wantNeedsHuman: true,
		},
		{
			name:           "wechat lead capture -> high + human",
			text:           "请加我微信：sales_boss88 聊细节",
			wantGrade:      GradeHigh,
			wantNeedsHuman: true,
		},
		{
			name:           "explicit ordering commitment -> high + human",
			text:           "我们本周要采购 30 套，请今天发合同走流程",
			wantGrade:      GradeHigh,
			wantNeedsHuman: true,
		},
		{
			name:           "explicit refusal -> low + no human",
			text:           "不需要，别再打电话发短信了，退订",
			wantGrade:      GradeLow,
			wantNeedsHuman: false,
		},
		{
			name:           "after-sales / refund -> low + human",
			text:           "上周买的机器坏了需要售后维修",
			wantGrade:      GradeLow,
			wantNeedsHuman: true,
		},
		{
			name:           "empty text -> insufficient + no human",
			text:           "   ",
			wantGrade:      GradeInsufficient,
			wantNeedsHuman: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := clf.Evaluate(context.Background(), EvalRequest{
				SubjectKind:   "lead",
				Text:          tc.text,
				FallbackGrade: GradeInsufficient,
			})
			if res.Source != SourceFastPath {
				t.Fatalf("source = %s, want %s", res.Source, SourceFastPath)
			}
			if res.Grade != tc.wantGrade || res.NeedsHumanHandoff != tc.wantNeedsHuman {
				t.Fatalf("got (grade=%s, needs_human=%t), want (%s, %t)",
					res.Grade, res.NeedsHumanHandoff, tc.wantGrade, tc.wantNeedsHuman)
			}
		})
	}
	if upstreamCalls.Load() != 0 {
		t.Fatalf("fast-path must never call upstream JEV, got %d calls", upstreamCalls.Load())
	}
}

func TestJEVWireContractChoiceCriteriaAndNoulHandoff(t *testing.T) {
	var (
		mu       sync.Mutex
		gotAuth  string
		gotRaw   map[string]any
		callCnt  int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBytes, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(rawBytes, &parsed)
		mu.Lock()
		callCnt++
		gotAuth = r.Header.Get("Authorization")
		gotRaw = parsed
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "sys1_test_001",
			"model": "jev-1.14.0",
			"answers": {
				"intent_grade": {
					"type": "choice",
					"choice": "high",
					"confidence": 0.82,
					"probabilities": {"high": 0.86, "medium": 0.12, "low": 0.02}
				},
				"needs_human_handoff": {
					"type": "noul",
					"noul": 0.91
				}
			},
			"usage": {"input_tokens": 310, "output_tokens": 32}
		}`))
	}))
	defer srv.Close()

	clf := NewClassifier(Config{
		Endpoint:         srv.URL,
		APIKey:           "sk-live-secret-never-log",
		HandoffThreshold: 0.65,
		Logger:           log.New(io.Discard, "", 0),
	})

	grayText := "我们下个月开两家新分店，老板很认可你们的稳定性，想对比一下部署周期和定制支持。"
	res := clf.Evaluate(context.Background(), EvalRequest{
		SubjectKind:    "session",
		Text:           grayText,
		EvidenceCount:  2,
		FallbackGrade:  GradeInsufficient,
		FallbackReason: "rules insufficient",
	})

	if res.Source != SourceJEV {
		t.Fatalf("source = %s, want %s (fallback=%s)", res.Source, SourceJEV, res.FallbackReason)
	}
	if res.Grade != GradeHigh || res.Confidence != 0.82 {
		t.Fatalf("unexpected grade/confidence: %+v", res)
	}
	if !res.NeedsHumanHandoff || res.HandoffProbability != 0.91 {
		t.Fatalf("expected needs_human_handoff=true (0.91), got %+v", res)
	}
	if res.Model != "jev-1.14.0" {
		t.Fatalf("model = %q, want jev-1.14.0", res.Model)
	}

	// Verify exact live /v1/systemone wire contract!
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer sk-live-secret-never-log" {
		t.Fatalf("unexpected Authorization header: %q", gotAuth)
	}
	if gotRaw["model"] != "jev-1.14.0" {
		t.Fatalf("wire model = %v, want jev-1.14.0", gotRaw["model"])
	}
	questions, ok := gotRaw["questions"].(map[string]any)
	if !ok {
		t.Fatalf("missing questions map: %v", gotRaw)
	}
	qGrade := questions["intent_grade"].(map[string]any)
	if qGrade["type"] != "choice" {
		t.Fatalf("intent_grade.type = %v, want choice", qGrade["type"])
	}
	if _, forbidden := qGrade["options"]; forbidden {
		t.Fatal("wire contract violation: choice MUST NOT contain 'options'")
	}
	if _, forbidden := qGrade["choices"]; forbidden {
		t.Fatal("wire contract violation: choice MUST NOT contain 'choices'")
	}
	criteria, ok := qGrade["criteria"].(map[string]any)
	if !ok || criteria["high"] == "" || criteria["medium"] == "" || criteria["low"] == "" {
		t.Fatalf("choice criteria map invalid: %v", qGrade["criteria"])
	}
	qHandoff := questions["needs_human_handoff"].(map[string]any)
	if qHandoff["type"] != "noul" {
		t.Fatalf("needs_human_handoff.type = %v, want noul", qHandoff["type"])
	}
	hCrit, ok := qHandoff["criteria"].(map[string]any)
	if !ok || hCrit["true"] == "" || hCrit["false"] == "" {
		t.Fatalf("noul criteria map invalid: %v", qHandoff["criteria"])
	}
	for k, v := range questions {
		q := v.(map[string]any)
		if q["type"] == "score" {
			t.Fatalf("wire contract violation: question %s uses forbidden 'score' primitive", k)
		}
	}
}

func TestJEVHandoffBelowThresholdStaysFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "sys1_low_noul",
			"model": "jev-1.14.0",
			"answers": {
				"intent_grade": {"type": "choice", "choice": "medium", "confidence": 0.61, "probabilities": {"high":0.1,"medium":0.7,"low":0.2}},
				"needs_human_handoff": {"type": "noul", "noul": 0.28}
			}
		}`))
	}))
	defer srv.Close()

	clf := NewClassifier(Config{Endpoint: srv.URL, APIKey: "k", HandoffThreshold: 0.65, Logger: log.New(io.Discard, "", 0)})
	res := clf.Evaluate(context.Background(), EvalRequest{
		Text:          "你们这套工具支持苹果手机还是网页版？先随便看看。",
		FallbackGrade: GradeLow,
	})
	if res.Grade != GradeMedium || res.NeedsHumanHandoff {
		t.Fatalf("expected medium + needs_human=false, got %+v", res)
	}
}

func TestSingleflightAndBoundedLRUCache(t *testing.T) {
	var upstreamCalls atomic.Int32
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		<-gate
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "sys1_sf",
			"model": "jev-1.14.0",
			"answers": {
				"intent_grade": {"type": "choice", "choice": "medium", "confidence": 0.72},
				"needs_human_handoff": {"type": "noul", "noul": 0.40}
			}
		}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	clf := NewClassifier(Config{
		Endpoint:      srv.URL,
		APIKey:        "k",
		CacheCapacity: 2,
		CacheTTL:      120 * time.Second,
		Logger:        log.New(io.Discard, "", 0),
		NowFn: func() time.Time {
			nowMu.Lock()
			defer nowMu.Unlock()
			return now
		},
	})

	const workers = 12
	var wg sync.WaitGroup
	results := make([]EvalResult, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = clf.Evaluate(context.Background(), EvalRequest{
				SubjectKind:   "lead",
				Text:          "我们在评估同类方案，下季度看情况决定。",
				FallbackGrade: GradeInsufficient,
			})
		}(i)
	}
	time.Sleep(30 * time.Millisecond)
	close(gate)
	wg.Wait()

	if upstreamCalls.Load() != 1 {
		t.Fatalf("singleflight must coalesce %d concurrent calls into 1 upstream call, got %d", workers, upstreamCalls.Load())
	}
	for i, r := range results {
		if r.Grade != GradeMedium {
			t.Fatalf("worker %d grade = %s, want medium", i, r.Grade)
		}
	}

	// Second call hits LRU cache -> still 1 upstream call!
	cached := clf.Evaluate(context.Background(), EvalRequest{
		SubjectKind:   "lead",
		Text:          "我们在评估同类方案，下季度看情况决定。",
		FallbackGrade: GradeInsufficient,
	})
	if !cached.CacheHit || upstreamCalls.Load() != 1 {
		t.Fatalf("expected cache hit with 1 total upstream call, got hit=%v calls=%d", cached.CacheHit, upstreamCalls.Load())
	}

	// Insert 2 more distinct keys -> capacity=2 evicts oldest.
	_ = clf.Evaluate(context.Background(), EvalRequest{Text: "灰区问题二，想看看成功案例", FallbackGrade: GradeInsufficient})
	_ = clf.Evaluate(context.Background(), EvalRequest{Text: "灰区问题三，团队规模五十人适合吗", FallbackGrade: GradeInsufficient})
	if clf.CacheLen() != 2 {
		t.Fatalf("bounded LRU cache len = %d, want 2", clf.CacheLen())
	}

	// Advance clock past 120s TTL -> entry expires and re-queries upstream.
	nowMu.Lock()
	now = now.Add(125 * time.Second)
	nowMu.Unlock()
	callsBeforeExpiry := upstreamCalls.Load()
	expired := clf.Evaluate(context.Background(), EvalRequest{Text: "灰区问题三，团队规模五十人适合吗", FallbackGrade: GradeInsufficient})
	if expired.CacheHit || upstreamCalls.Load() != callsBeforeExpiry+1 {
		t.Fatalf("expected TTL expiry to miss cache, hit=%v calls=%d", expired.CacheHit, upstreamCalls.Load())
	}
}

func TestCircuitBreakerAndFailSafeFallback(t *testing.T) {
	var failUpstream atomic.Bool
	failUpstream.Store(true)
	var upstreamCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if failUpstream.Load() {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"upstream down"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "sys1_recovered",
			"model": "jev-1.14.0",
			"answers": {
				"intent_grade": {"type": "choice", "choice": "high", "confidence": 0.90},
				"needs_human_handoff": {"type": "noul", "noul": 0.85}
			}
		}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)
	clf := NewClassifier(Config{
		Endpoint: srv.URL,
		APIKey:   "k",
		Timeout:  200 * time.Millisecond,
		Breaker: BreakerConfig{
			Window:              60 * time.Second,
			MinRequests:         3,
			ErrorRateThreshold:  0.50,
			ConsecutiveFailures: 3,
			Cooldown:            30 * time.Second,
		},
		Logger: log.New(io.Discard, "", 0),
		NowFn:  func() time.Time { return now },
	})

	// 3 consecutive failures -> each fails safe to rule baseline, then opens breaker.
	for i := 0; i < 3; i++ {
		res := clf.Evaluate(context.Background(), EvalRequest{
			Text:           "灰区咨询_" + string(rune('A'+i)),
			FallbackGrade:  GradeMedium,
			FallbackReason: "default rules",
		})
		if res.Source != SourceFallbackRules || res.Grade != GradeMedium {
			t.Fatalf("attempt %d must fail-safe to fallback rules: %+v", i, res)
		}
	}
	if clf.BreakerState() != BreakerOpen {
		t.Fatalf("breaker state = %s, want open", clf.BreakerState())
	}

	// While open: 0 upstream calls, immediate fail-safe fallback with CircuitOpen=true!
	callsWhenOpened := upstreamCalls.Load()
	blocked := clf.Evaluate(context.Background(), EvalRequest{
		Text:           "灰区咨询_blocked",
		FallbackGrade:  GradeLow,
		FallbackReason: "rules low",
	})
	if !blocked.CircuitOpen || blocked.Source != SourceFallbackRules || blocked.Grade != GradeLow {
		t.Fatalf("expected circuit_open fail-safe fallback, got %+v", blocked)
	}
	if upstreamCalls.Load() != callsWhenOpened {
		t.Fatalf("open circuit must not call upstream: before=%d after=%d", callsWhenOpened, upstreamCalls.Load())
	}

	// Advance past 30s cooldown -> half-open probe succeeds -> breaker closes!
	failUpstream.Store(false)
	now = now.Add(31 * time.Second)
	recovered := clf.Evaluate(context.Background(), EvalRequest{
		Text:          "灰区咨询_recovered",
		FallbackGrade: GradeInsufficient,
	})
	if recovered.Source != SourceJEV || recovered.Grade != GradeHigh || !recovered.NeedsHumanHandoff {
		t.Fatalf("expected half-open recovery via JEV, got %+v", recovered)
	}
	if clf.BreakerState() != BreakerClosed {
		t.Fatalf("breaker state after recovery = %s, want closed", clf.BreakerState())
	}
}

func TestShortTimeoutFailSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	clf := NewClassifier(Config{
		Endpoint: srv.URL,
		APIKey:   "k",
		Timeout:  40 * time.Millisecond,
		Logger:   log.New(io.Discard, "", 0),
	})
	res := clf.Evaluate(context.Background(), EvalRequest{
		Text:           "我们在对比几家供应商，暂时还没定。",
		FallbackGrade:  GradeMedium,
		FallbackReason: "rule fallback",
	})
	if res.Source != SourceFallbackRules || res.Grade != GradeMedium {
		t.Fatalf("timeout must fail-safe to rules, got %+v", res)
	}
	if !strings.Contains(res.FallbackReason, "deadline") && !strings.Contains(res.FallbackReason, "Timeout") && !strings.Contains(res.FallbackReason, "timeout") {
		t.Fatalf("expected timeout fallback reason, got %q", res.FallbackReason)
	}
}

func TestShadowModeCallsJEVWithoutAlteringRuleDecision(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "sys1_shadow",
			"model": "jev-1.14.0",
			"answers": {
				"intent_grade": {"type": "choice", "choice": "high", "confidence": 0.77},
				"needs_human_handoff": {"type": "noul", "noul": 0.88}
			}
		}`))
	}))
	defer srv.Close()

	clf := NewClassifier(Config{
		Endpoint:   srv.URL,
		APIKey:     "k",
		ShadowMode: true,
		Logger:     log.New(io.Discard, "", 0),
	})
	res := clf.Evaluate(context.Background(), EvalRequest{
		Text:           "你们的方案跟行业大厂比优势在哪？老板下周开会讨论。",
		FallbackGrade:  GradeInsufficient,
		FallbackReason: "rules baseline",
	})
	if calls.Load() != 1 {
		t.Fatalf("shadow mode must still invoke JEV for observation, calls=%d", calls.Load())
	}
	if !res.ShadowMode || res.Source != SourceShadowFallbackRules {
		t.Fatalf("unexpected shadow result flags: %+v", res)
	}
	if res.Grade != GradeInsufficient || res.NeedsHumanHandoff {
		t.Fatalf("shadow mode must preserve rule fallback decision, got %+v", res)
	}
	if res.ShadowGrade != GradeHigh || !res.ShadowNeedsHuman || res.ShadowHandoffProb != 0.88 {
		t.Fatalf("shadow telemetry missing: %+v", res)
	}
}
