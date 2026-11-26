package httpapi

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/lightcopy"
	"github.com/bianjiefilm/leads-engine/server/internal/platformtask"
)

type stubToolDispatcher struct {
	mu            sync.Mutex
	dispatchCalls int32
	queryCalls    int32
	lastReq       lightcopy.HandoffDispatchRequest
	dispatchFn    func(req lightcopy.HandoffDispatchRequest) lightcopy.HandoffDispatchResult
	queryFn       func(tenantID, key, tool string) lightcopy.HandoffDispatchResult
}

func (d *stubToolDispatcher) Dispatch(_ context.Context, req lightcopy.HandoffDispatchRequest) lightcopy.HandoffDispatchResult {
	atomic.AddInt32(&d.dispatchCalls, 1)
	d.mu.Lock()
	d.lastReq = req
	fn := d.dispatchFn
	d.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return lightcopy.HandoffDispatchResult{
		Status:           lightcopy.StatusRecorded,
		Reason:           lightcopy.ReasonOpenExisting,
		TargetProjectRef: "ext_proj_" + req.Tool + "_1",
		ExternalTaskRef:  "ext_tsk_1",
	}
}

func (d *stubToolDispatcher) QueryStatus(_ context.Context, tenantID, key, tool string) lightcopy.HandoffDispatchResult {
	atomic.AddInt32(&d.queryCalls, 1)
	d.mu.Lock()
	fn := d.queryFn
	d.mu.Unlock()
	if fn != nil {
		return fn(tenantID, key, tool)
	}
	return lightcopy.HandoffDispatchResult{
		Status:           lightcopy.StatusRecorded,
		Reason:           lightcopy.ReasonOpenExisting,
		TargetProjectRef: "ext_proj_recovered_1",
		ExternalTaskRef:  "ext_tsk_recovered_1",
	}
}

func TestLightCopyThreeSamplesAuditAndUnknownPriceGuard(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, contact := h.seed()

	// 1. Campaign sample (活动): minimal confirmed facts -> passes audit and records revisions.
	intake := h.intake(sessionOwnerA, tenant, `{"source_app":"touch","source_ns":"landing","event_id":"evt-lc-camp-1","contact":{"name":"王女士","phone":"13800138001","email":"wang@shop.cn"},"business_category":"merchant_customer","source_type":"touch_campaign","source":{"source_app":"touch","source_ref":"双十二门店体验周","auth_scope_snapshot":"secret-scope"},"consent":{"source_submission_ref":"sub-lc-1","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":false}}`, 201)
	campID := intake["lead_id"].(string)

	var lastPrompt string
	h.api.LightCopyPhraser = &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		lastPrompt = in.Prompt
		return platformtask.GenerateResult{
			TaskID:         "tsk_camp_1",
			Text:           "欢迎了解双十二门店体验周，本次到店体验活动可领取门店体验礼，欢迎回复预约到店时段。",
			CostCents:      1,
			BillingVerdict: "recorded",
		}
	}}

	campBody := `{
		"sample_category": "campaign",
		"selected": ["activity_ref"],
		"facts": [
			{"key":"scene","value":"到店体验活动","confirmed":true,"authorized":true},
			{"key":"offer","value":"领取门店体验礼","confirmed":true,"authorized":true},
			{"key":"next_step","value":"预约到店时段","confirmed":true,"authorized":true}
		]
	}`
	campResp := h.mustDo("POST", "/api/v1/campaigns/"+campID+"/light-copy/marketing_brief/generate", sessionOwnerA, tenant, campBody, 200)
	if strings.Contains(lastPrompt, "13800138001") || strings.Contains(lastPrompt, "王女士") || strings.Contains(lastPrompt, "secret-scope") {
		t.Fatalf("campaign prompt leaked contact/scope: %q", lastPrompt)
	}
	campDraft := campResp["draft"].(map[string]any)
	if campDraft["task_id"] != "tsk_camp_1" || campDraft["billing_verdict"] != "recorded" || campDraft["input_facts_hash"] == "" {
		t.Fatalf("campaign draft missing provenance: %v", campDraft)
	}
	if campDraft["sent"] == true || campDraft["published"] == true || campDraft["marketing_permitted"] == true {
		t.Fatalf("campaign draft must never claim sent/published/marketing_permitted: %v", campDraft)
	}
	campAudit := campResp["audit"].(map[string]any)
	if campAudit["passed"] != true || campAudit["has_cta"] != true || campAudit["tone_ok"] != true {
		t.Fatalf("campaign audit = %v", campAudit)
	}

	// 2. Product sample (商品) with expired price -> unknown marking, blocks invented price & placeholder, accepts honest unknown.
	oppProduct := h.createOpp(t, sessionOwnerA, tenant, contact, "恒温护发吹风机", "merchant_customer", "")
	expiredAt := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	productReq := fmt.Sprintf(`{
		"sample_category": "product",
		"selected": ["title"],
		"allow_unknown_marking": true,
		"facts": [
			{"key":"offer","value":"支持门店现场试用","confirmed":true,"authorized":true},
			{"key":"price_cents","price_cents":59900,"valid_until":%q,"confirmed":true,"authorized":true}
		]
	}`, expiredAt)

	// Model invents price 299元 when price is expired -> blocked fail-closed, receipt preserved.
	h.api.LightCopyPhraser = &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		lastPrompt = in.Prompt
		return platformtask.GenerateResult{
			TaskID: "tsk_prod_invented", Text: "恒温护发吹风机现特价299元，支持门店现场试用，欢迎预约体验。",
			CostCents: 1, BillingVerdict: "recorded",
		}
	}}
	status, inventedResp, _ := h.do("POST", "/api/v1/opportunities/"+oppProduct+"/light-copy/reply/generate", sessionOwnerA, tenant, productReq)
	if status != 409 || inventedResp["error"] != "ungrounded_claim_refused" {
		t.Fatalf("invented price = %d %v", status, inventedResp)
	}
	if strings.Contains(lastPrompt, "59900") || !strings.Contains(lastPrompt, "price_cents: status=unknown") {
		t.Fatalf("product prompt leaked expired price or missed unknown marker: %q", lastPrompt)
	}
	if inventedResp["model"].(map[string]any)["task_id"] != "tsk_prod_invented" {
		t.Fatalf("missing honest billing receipt: %v", inventedResp)
	}

	// Model uses template placeholder -> blocked fail-closed.
	h.api.LightCopyPhraser = &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		return platformtask.GenerateResult{
			TaskID: "tsk_prod_ph", Text: "恒温护发吹风机最新报价为[待填写]，支持门店现场试用。",
			CostCents: 1, BillingVerdict: "recorded",
		}
	}}
	status, phResp, _ := h.do("POST", "/api/v1/opportunities/"+oppProduct+"/light-copy/reply/generate", sessionOwnerA, tenant, productReq)
	if status != 409 || phResp["error"] != "placeholder_refused" {
		t.Fatalf("placeholder = %d %v", status, phResp)
	}

	// Model honestly acknowledges unknown price -> succeeds and records unknown_fields.
	h.api.LightCopyPhraser = &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		return platformtask.GenerateResult{
			TaskID: "tsk_prod_ok", Text: "恒温护发吹风机支持门店现场试用，最新报价待核实并以门店最新确认为准，欢迎预约体验。",
			CostCents: 1, BillingVerdict: "recorded",
		}
	}}
	prodOK := h.mustDo("POST", "/api/v1/opportunities/"+oppProduct+"/light-copy/reply/generate", sessionOwnerA, tenant, productReq, 200)
	prodDraft := prodOK["draft"].(map[string]any)
	unknowns := prodDraft["unknown_fields"].([]any)
	if len(unknowns) != 1 || unknowns[0] != "price_cents" {
		t.Fatalf("product unknown_fields = %v", unknowns)
	}
	if prodOK["audit"].(map[string]any)["unknown_acknowledged"] != true {
		t.Fatalf("product audit should acknowledge unknown: %v", prodOK["audit"])
	}

	// 3. Service sample (服务) with valid price -> passes, and blocks ungrounded promises or PII leak.
	oppService := h.createOpp(t, sessionOwnerA, tenant, contact, "空调深度清洗服务", "merchant_customer", "")
	validUntil := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	serviceReq := fmt.Sprintf(`{
		"sample_category": "service",
		"selected": ["title"],
		"facts": [
			{"key":"scene","value":"换季深度保养咨询","confirmed":true,"authorized":true},
			{"key":"next_step","value":"确认上门时间","confirmed":true,"authorized":true},
			{"key":"price_cents","price_cents":39900,"valid_until":%q,"confirmed":true,"authorized":true}
		]
	}`, validUntil)

	h.api.LightCopyPhraser = &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		return platformtask.GenerateResult{
			TaskID: "tsk_srv_bad", Text: "空调深度清洗服务399元，100%保证效果且已自动发送预约确认。",
			CostCents: 1, BillingVerdict: "recorded",
		}
	}}
	status, badSrv, _ := h.do("POST", "/api/v1/opportunities/"+oppService+"/light-copy/reply/generate", sessionOwnerA, tenant, serviceReq)
	if status != 409 || badSrv["error"] != "ungrounded_claim_refused" {
		t.Fatalf("service ungrounded promise = %d %v", status, badSrv)
	}

	h.api.LightCopyPhraser = &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		return platformtask.GenerateResult{
			TaskID: "tsk_srv_ok", Text: "针对换季深度保养咨询，空调深度清洗服务当前确认报价为399元，欢迎联系确认上门时间。",
			CostCents: 1, BillingVerdict: "recorded",
		}
	}}
	srvOK := h.mustDo("POST", "/api/v1/opportunities/"+oppService+"/light-copy/reply/generate", sessionOwnerA, tenant, serviceReq, 200)
	if srvOK["audit"].(map[string]any)["passed"] != true {
		t.Fatalf("service audit = %v", srvOK["audit"])
	}
}

func TestLightCopyPIIGuardRejectsAndStripsSensitiveMaterial(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "秋季会员日", "merchant_customer", "")

	gen := &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		return platformtask.GenerateResult{TaskID: "tsk_pii", Text: "正文", CostCents: 1, BillingVerdict: "recorded"}
	}}
	h.api.LightCopyPhraser = gen

	// Strict mode rejects phone list, sales note, contact fulltext, unauthorized portrait before meter runs.
	badPayloads := []struct {
		name string
		body string
		code string
	}{
		{"phone_list_key", `{"facts":[{"key":"phone_list","value":"13800138000,13900139000","confirmed":true,"authorized":true}]}`, "unauthorized_field"},
		{"sales_note_key", `{"facts":[{"key":"sales_note","value":"内部压价策略","confirmed":true,"authorized":true}]}`, "unauthorized_field"},
		{"smuggled_phone_list", `{"facts":[{"key":"scene","value":"通知名单 13800138000 13911112222","confirmed":true,"authorized":true}]}`, "pii_detected"},
		{"smuggled_internal_note", `{"facts":[{"key":"offer","value":"内部备注：不要给赠品","confirmed":true,"authorized":true}]}`, "pii_detected"},
		{"ungranted_asset", `{"selected":["title"],"authorized_assets":[{"asset_id":"ast_1","grant_ref":""}]}`, "asset_unauthorized"},
		{"expired_asset", fmt.Sprintf(`{"selected":["title"],"authorized_assets":[{"asset_id":"ast_1","grant_ref":"grt_1","expires_at":%q}]}`, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)), "asset_expired"},
	}
	for _, tc := range badPayloads {
		status, resp, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerA, tenant, tc.body)
		if status != 400 || resp["error"] != tc.code {
			t.Fatalf("%s: got %d %v, want 400 %s", tc.name, status, resp, tc.code)
		}
	}
	if gen.calls != 0 {
		t.Fatalf("PII/unauthorized gate must block before model meter: calls=%d", gen.calls)
	}

	// Explicit strip_pii mode strips phone numbers and internal note lines cleanly.
	stripped := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/facts", sessionOwnerA, tenant, `{
		"strip_pii": true,
		"facts": [
			{"key":"scene","value":"秋季会员到店活动 13800138000\n内部备注：底价99","confirmed":true,"authorized":true}
		]
	}`, 200)
	if stripped["stripped_pii"] != true {
		t.Fatalf("expected stripped_pii=true: %v", stripped)
	}
	raw := jsonText(stripped)
	if strings.Contains(raw, "13800138000") || strings.Contains(raw, "内部备注") || !strings.Contains(raw, "秋季会员到店活动") {
		t.Fatalf("stripped brief invalid: %s", raw)
	}
}

func TestLightCopyHumanEditProtectedFromLateAsyncGeneration(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "门店周年庆", "merchant_customer", "")

	// Step 1: Model generates v1 so we can also verify historical provenance survives manual edits.
	h.api.LightCopyPhraser = &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		return platformtask.GenerateResult{
			TaskID: "tsk_v1", Text: "欢迎参加门店周年庆活动，欢迎预约到店。", CostCents: 1, BillingVerdict: "recorded",
		}
	}}
	v1Resp := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerA, tenant, `{"selected":["title"]}`, 200)
	if v1Resp["draft"].(map[string]any)["content_version"].(float64) != 1 {
		t.Fatalf("expected v1: %v", v1Resp)
	}

	// Step 2: Start a slow async model generation anchored on v1 while a human saves v2 concurrently.
	started := make(chan struct{})
	release := make(chan struct{})
	h.api.LightCopyPhraser = &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		close(started)
		<-release
		return platformtask.GenerateResult{
			TaskID: "tsk_late_v2", Text: "晚到的异步模型回复，绝不能覆盖人工改稿", CostCents: 1, BillingVerdict: "recorded",
		}
	}}

	type genOutcome struct {
		status int
		body   map[string]any
	}
	done := make(chan genOutcome, 1)
	go func() {
		st, b, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerA, tenant, `{"selected":["title"],"expected_revision":1}`)
		done <- genOutcome{status: st, body: b}
	}()

	<-started

	// Human edits and saves v2 with expected_revision=1.
	humanEdit := h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant,
		`{"body":"人工精修后的最终草稿","expected_revision":1}`, 200)
	if humanEdit["content_version"].(float64) != 2 || humanEdit["body"] != "人工精修后的最终草稿" {
		t.Fatalf("human edit = %v", humanEdit)
	}

	// Release slow async model reply -> must be rejected with 409 draft_modified_by_human.
	close(release)
	out := <-done
	if out.status != 409 || out.body["error"] != "draft_modified_by_human" {
		t.Fatalf("late async generate must not overwrite human edit: %d %v", out.status, out.body)
	}
	if out.body["current_version"].(float64) != 2 || out.body["model"].(map[string]any)["task_id"] != "tsk_late_v2" {
		t.Fatalf("conflict response missing version/receipt: %v", out.body)
	}

	// Verify stored draft is still the human edit v2.
	listed := h.mustDo("GET", "/api/v1/opportunities/"+opp+"/light-copy", sessionOwnerA, tenant, "", 200)
	items := listed["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["body"] != "人工精修后的最终草稿" {
		t.Fatalf("human draft was overwritten: %v", items)
	}

	// Verify stale expected_revision on PUT, confirm, and generate all fail with 409 revision_conflict (and 0 meter calls).
	staleGen := &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		return platformtask.GenerateResult{TaskID: "should_not_run", Text: "x", CostCents: 1, BillingVerdict: "recorded"}
	}}
	h.api.LightCopyPhraser = staleGen

	st, putConf, _ := h.do("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant, `{"body":"旧版本写","expected_revision":1}`)
	if st != 409 || putConf["error"] != "revision_conflict" {
		t.Fatalf("stale PUT = %d %v", st, putConf)
	}
	st, confConf, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/confirm", sessionOwnerA, tenant, `{"expected_revision":1}`)
	if st != 409 || confConf["error"] != "revision_conflict" {
		t.Fatalf("stale confirm = %d %v", st, confConf)
	}
	st, genConf, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerA, tenant, `{"selected":["title"],"expected_revision":1}`)
	if st != 409 || genConf["error"] != "revision_conflict" {
		t.Fatalf("stale generate = %d %v", st, genConf)
	}
	if staleGen.calls != 0 {
		t.Fatalf("stale expected_revision reached model meter: calls=%d", staleGen.calls)
	}

	// Verify revisions audit preserves both v1 (model + task_id + live_charge=1) and v2 (manual).
	revs := h.mustDo("GET", "/api/v1/opportunities/"+opp+"/light-copy/reply/revisions", sessionOwnerA, tenant, "", 200)
	revItems := revs["items"].([]any)
	if len(revItems) != 2 {
		t.Fatalf("expected 2 revisions, got %v", revItems)
	}
	r1 := revItems[0].(map[string]any)
	r2 := revItems[1].(map[string]any)
	if r1["task_id"] != "tsk_v1" || r1["live_charge"].(float64) != 1 || r1["origin"] != "model" {
		t.Fatalf("v1 revision lost model provenance: %v", r1)
	}
	if r2["origin"] != "manual" || r2["body"] != "人工精修后的最终草稿" {
		t.Fatalf("v2 revision mismatch: %v", r2)
	}
}

func TestLightCopyHandoffIdempotentConcurrentAndToolCoverage(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "新品主图与口播推广", "merchant_customer", "")
	h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant, `{"body":"新品草稿正文"}`, 200)

	disp := &stubToolDispatcher{}
	h.api.LightCopyToolDispatcher = disp

	quoteExp := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	assetExp := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)

	// Concurrent 12 clicks with the same idempotency key -> Dispatch runs ONCE, 1 row in DB.
	reqBody := fmt.Sprintf(`{
		"tool": "swarm",
		"idempotency_key": "idem-concurrent-swarm-1",
		"selected": ["title"],
		"facts": [{"key":"scene","value":"新品口播场景","confirmed":true,"authorized":true}],
		"authorized_assets": [{"asset_id":"ast_sku_1","asset_kind":"image","grant_ref":"grt_sku_1","license_ref":"lic_sku_1","expires_at":%q}],
		"permission_ref": "perm_tenantA_01",
		"payer_ref": "payer_merchant_01",
		"quote_expires_at": %q
	}`, assetExp, quoteExp)

	const workers = 12
	var wg sync.WaitGroup
	ids := make([]string, workers)
	statuses := make([]int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			st, resp, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant, reqBody)
			statuses[idx] = st
			if id, ok := resp["id"].(string); ok {
				ids[idx] = id
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		if statuses[i] != 200 || ids[i] == "" || ids[i] != ids[0] {
			t.Fatalf("worker %d: status=%d id=%q want 200 %q", i, statuses[i], ids[i], ids[0])
		}
	}
	if calls := atomic.LoadInt32(&disp.dispatchCalls); calls != 1 {
		t.Fatalf("concurrent duplicate clicks dispatched %d times, want 1", calls)
	}
	var count int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM light_copy_handoffs WHERE tenant_id=? AND idempotency_key='idem-concurrent-swarm-1'`, tenant).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("db handoff rows = %d, want 1", count)
	}

	// Replaying the same idempotency_key with a different tool/payer must fail closed with 409 idempotency_key_conflict.
	tampered := strings.Replace(reqBody, `"tool": "swarm"`, `"tool": "aicut"`, 1)
	st, confResp, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant, tampered)
	if st != 409 || confResp["error"] != "idempotency_key_conflict" {
		t.Fatalf("tampered replay = %d %v", st, confResp)
	}

	// All supported professional ecosystem tools work via restricted handoff without creating in-CRM projects.
	for _, tool := range []string{"goboost", "product_image", "digital_human", "aicut"} {
		body := fmt.Sprintf(`{
			"tool": %q,
			"idempotency_key": "idem-tool-%s",
			"selected": ["title"],
			"permission_ref": "perm_1",
			"payer_ref": "payer_1"
		}`, tool, tool)
		resp := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant, body, 200)
		if resp["status"] != "recorded" || resp["project_created"] == true || resp["target_project_ref"] == "" {
			t.Fatalf("tool %s handoff = %v", tool, resp)
		}
	}
}

func TestLightCopyHandoffBackflowCrossTenantRevokeExpiredAndUnknownRecovery(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "高端摄影套餐", "merchant_customer", "")
	h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant, `{"body":"人工保存的初始草稿"}`, 200)

	// 1. Unknown status on initial handoff -> safe read-only recovery without re-dispatching.
	disp := &stubToolDispatcher{
		dispatchFn: func(req lightcopy.HandoffDispatchRequest) lightcopy.HandoffDispatchResult {
			return lightcopy.HandoffDispatchResult{
				Status: lightcopy.StatusUnknown,
				Reason: lightcopy.ReasonHandoffUnknown,
			}
		},
		queryFn: func(tenantID, key, tool string) lightcopy.HandoffDispatchResult {
			return lightcopy.HandoffDispatchResult{
				Status:           lightcopy.StatusRecorded,
				Reason:           lightcopy.ReasonOpenExisting,
				TargetProjectRef: "gb_proj_recovered_42",
				ExternalTaskRef:  "gb_tsk_42",
			}
		},
	}
	h.api.LightCopyToolDispatcher = disp

	futureExp := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	unknownHandoff := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant, fmt.Sprintf(`{
		"tool": "goboost",
		"idempotency_key": "idem-unknown-1",
		"selected": ["title"],
		"permission_ref": "perm_gb_1",
		"payer_ref": "payer_gb_1",
		"quote_expires_at": %q
	}`, futureExp), 200)
	if unknownHandoff["status"] != "unknown" {
		t.Fatalf("expected unknown status: %v", unknownHandoff)
	}
	handoffID := unknownHandoff["id"].(string)

	// Backflow while still unknown -> 409 handoff_not_active.
	st, notActive, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/handoffs/"+handoffID+"/returns", sessionOwnerA, tenant,
		`{"idempotency_key":"ret-early","asset_ref":"ast_gb_1","task_ref":"tsk_gb_1","summary":"成品候选"}`)
	if st != 409 || notActive["error"] != "handoff_not_active" {
		t.Fatalf("backflow before recovery = %d %v", st, notActive)
	}

	// Recover via read-only QueryStatus -> Dispatch count stays 1, Query count becomes 1, status becomes recorded.
	recovered := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/handoffs/"+handoffID+"/recover", sessionOwnerA, tenant, `{}`, 200)
	if recovered["status"] != "recorded" || recovered["target_project_ref"] != "gb_proj_recovered_42" || recovered["project_created"] == true {
		t.Fatalf("recovered = %v", recovered)
	}
	if atomic.LoadInt32(&disp.dispatchCalls) != 1 || atomic.LoadInt32(&disp.queryCalls) != 1 {
		t.Fatalf("recovery must query only: dispatch=%d query=%d", disp.dispatchCalls, disp.queryCalls)
	}

	// 2. Controlled backflow: rejects PII/unknown fields; accepts clean asset/task candidate idempotently without overwriting draft.
	st, badField, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/handoffs/"+handoffID+"/returns", sessionOwnerA, tenant,
		`{"idempotency_key":"ret-bad-1","asset_ref":"ast_1","phone":"13800138000"}`)
	if st != 400 || badField["error"] != "unauthorized_field" {
		t.Fatalf("backflow unknown field = %d %v", st, badField)
	}
	st, badPII, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/handoffs/"+handoffID+"/returns", sessionOwnerA, tenant,
		`{"idempotency_key":"ret-bad-2","asset_ref":"ast_1","summary":"回流联系人13800138000"}`)
	if st != 400 || badPII["error"] != "pii_detected" {
		t.Fatalf("backflow PII summary = %d %v", st, badPII)
	}

	retBody := `{"idempotency_key":"ret-ok-1","asset_ref":"ast_gb_99","task_ref":"tsk_gb_99","candidate_kind":"video_ref","summary":"GoBoost 15秒口播候选"}`
	ret1 := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/handoffs/"+handoffID+"/returns", sessionOwnerA, tenant, retBody, 200)
	cand1 := ret1["candidate"].(map[string]any)
	if ret1["idempotent_replay"] != false || ret1["draft_overwritten"] != false || cand1["asset_ref"] != "ast_gb_99" || cand1["sent"] == true || cand1["published"] == true || cand1["marketing_permitted"] == true {
		t.Fatalf("return candidate = %v", ret1)
	}
	ret2 := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/handoffs/"+handoffID+"/returns", sessionOwnerA, tenant, retBody, 200)
	if ret2["idempotent_replay"] != true || ret2["candidate"].(map[string]any)["id"] != cand1["id"] {
		t.Fatalf("return replay = %v", ret2)
	}

	// Verify handoff list surfaces the returned candidate and draft body is untouched.
	handoffList := h.mustDo("GET", "/api/v1/opportunities/"+opp+"/light-copy/handoffs", sessionOwnerA, tenant, "", 200)
	hItems := handoffList["items"].([]any)
	if len(hItems) != 1 {
		t.Fatalf("handoff list = %v", hItems)
	}
	returns := hItems[0].(map[string]any)["returns"].([]any)
	if len(returns) != 1 || returns[0].(map[string]any)["asset_ref"] != "ast_gb_99" {
		t.Fatalf("handoff returns = %v", returns)
	}
	draftList := h.mustDo("GET", "/api/v1/opportunities/"+opp+"/light-copy", sessionOwnerA, tenant, "", 200)
	if draftList["items"].([]any)[0].(map[string]any)["body"] != "人工保存的初始草稿" {
		t.Fatalf("backflow mutated manual draft: %v", draftList)
	}

	// 3. Cross-tenant fail-closed on handoff, recover, return, revoke.
	for _, ep := range []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/v1/opportunities/" + opp + "/light-copy/handoffs", ""},
		{"POST", "/api/v1/opportunities/" + opp + "/light-copy/handoffs/" + handoffID + "/returns", retBody},
		{"POST", "/api/v1/opportunities/" + opp + "/light-copy/handoffs/" + handoffID + "/recover", `{}`},
		{"POST", "/api/v1/opportunities/" + opp + "/light-copy/revoke", `{}`},
	} {
		st, _, _ := h.do(ep.method, ep.path, sessionOwnerB, tenant, ep.body)
		if st != 403 {
			t.Fatalf("cross tenant %s %s = %d, want 403", ep.method, ep.path, st)
		}
	}

	// 4. Expired quote on new handoff and on backflow fail-closed.
	pastExp := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	st, expHandoff, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant, fmt.Sprintf(`{
		"tool": "aicut",
		"idempotency_key": "idem-expired-quote",
		"selected": ["title"],
		"quote_expires_at": %q
	}`, pastExp))
	if st != 400 || expHandoff["error"] != "quote_expired" {
		t.Fatalf("expired quote handoff = %d %v", st, expHandoff)
	}
	// Simulate quote expiring on existing handoff -> backflow must fail with 409 quote_expired.
	if _, err := h.api.St.DB.Exec(`UPDATE light_copy_handoffs SET quote_expires_at=? WHERE id=?`, pastExp, handoffID); err != nil {
		t.Fatal(err)
	}
	st, expRet, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/handoffs/"+handoffID+"/returns", sessionOwnerA, tenant,
		`{"idempotency_key":"ret-after-exp","asset_ref":"ast_late","task_ref":"tsk_late"}`)
	if st != 409 || expRet["error"] != "quote_expired" {
		t.Fatalf("backflow after quote expiry = %d %v", st, expRet)
	}
	// Restore quote expiry to test revocation cleanly.
	if _, err := h.api.St.DB.Exec(`UPDATE light_copy_handoffs SET quote_expires_at=? WHERE id=?`, futureExp, handoffID); err != nil {
		t.Fatal(err)
	}

	// 5a. Single-handoff revoke blocks replay/backflow/recover for that handoff.
	singleHandoff := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant, fmt.Sprintf(`{
		"tool": "product_image",
		"idempotency_key": "idem-single-revoke",
		"selected": ["title"],
		"permission_ref": "perm_pi_1",
		"payer_ref": "payer_pi_1",
		"quote_expires_at": %q
	}`, futureExp), 200)
	singleID := singleHandoff["id"].(string)
	singleRev := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/handoffs/"+singleID+"/revoke", sessionOwnerA, tenant, `{}`, 200)
	if singleRev["status"] != "revoked" || singleRev["revoked_at"] == "" {
		t.Fatalf("single handoff revoke = %v", singleRev)
	}
	st, replayRev, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant, fmt.Sprintf(`{
		"tool": "product_image",
		"idempotency_key": "idem-single-revoke",
		"selected": ["title"],
		"permission_ref": "perm_pi_1",
		"payer_ref": "payer_pi_1",
		"quote_expires_at": %q
	}`, futureExp))
	if st != 403 || replayRev["error"] != "permission_revoked" {
		t.Fatalf("replay revoked handoff = %d %v", st, replayRev)
	}
	st, retSingleRev, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/handoffs/"+singleID+"/returns", sessionOwnerA, tenant,
		`{"idempotency_key":"ret-single-rev","asset_ref":"ast_sr","task_ref":"tsk_sr"}`)
	if st != 403 || retSingleRev["error"] != "permission_revoked" {
		t.Fatalf("backflow to revoked handoff = %d %v", st, retSingleRev)
	}

	// 5b. Subject-level revocation fail-closed: blocks new handoff and backflow while preserving editable manual draft.
	revokedResp := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/revoke", sessionOwnerA, tenant, `{"reason":"customer_withdrew_consent"}`, 200)
	if revokedResp["revoked"] != true || revokedResp["drafts_kept"] != true {
		t.Fatalf("revoke = %v", revokedResp)
	}
	st, newAfterRevoke, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant, `{
		"tool":"aicut","idempotency_key":"idem-after-revoke","selected":["title"]
	}`)
	if st != 403 || newAfterRevoke["error"] != "permission_revoked" {
		t.Fatalf("handoff after revoke = %d %v", st, newAfterRevoke)
	}
	st, retAfterRevoke, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/handoffs/"+handoffID+"/returns", sessionOwnerA, tenant,
		`{"idempotency_key":"ret-after-revoke","asset_ref":"ast_after","task_ref":"tsk_after"}`)
	if st != 403 || retAfterRevoke["error"] != "permission_revoked" {
		t.Fatalf("backflow after revoke = %d %v", st, retAfterRevoke)
	}
	// Draft remains manually editable after revocation.
	manualAfterRevoke := h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant, `{"body":"撤权后仍可手改草稿"}`, 200)
	if manualAfterRevoke["body"] != "撤权后仍可手改草稿" || manualAfterRevoke["sent"] == true {
		t.Fatalf("manual edit after revoke = %v", manualAfterRevoke)
	}
}
