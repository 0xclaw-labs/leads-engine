package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bianjiefilm/leads-engine/server/internal/platformtask"
)

func TestLightCopyFeatureOffIsInvisible(t *testing.T) {
	h := newHarness(t)
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "门店开业", "merchant_customer", "")
	// HUI-2626 盲评 P2①：capability 发现始终可达（200 + enabled:false），
	// 面板据此隐藏，详情页不再每次都打 404；写路径仍整体不可见（fail-closed 不变）。
	cap := h.mustDo("GET", "/api/v1/light-copy/capability", sessionOwnerA, tenant, "", 200)
	if enabled, ok := cap["enabled"].(bool); !ok || enabled {
		t.Fatalf("capability off must report enabled=false: %v", cap)
	}
	status, _, _ := h.do("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant, `{"body":"草稿"}`)
	if status != 404 {
		t.Fatalf("save = %d", status)
	}
	status, _, _ = h.do("GET", "/api/v1/opportunities/"+opp+"/light-copy", sessionOwnerA, tenant, "")
	if status != 404 {
		t.Fatalf("list = %d", status)
	}
}

func TestLightCopyManualDraftDoesNotSendOrInventText(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true, captureLog: true})
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "门店开业", "merchant_customer", "")
	secret := "手改回复不应进日志"

	cap := h.mustDo("GET", "/api/v1/light-copy/capability", sessionOwnerA, tenant, "", 200)
	if cap["model_ready"] == true || cap["live_charge"].(float64) != 0 {
		t.Fatalf("capability: %v", cap)
	}

	status, gen, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerA, tenant, `{"selected":["title"]}`)
	if status != 409 || gen["error"] != "model_unavailable" {
		t.Fatalf("generate = %d %v", status, gen)
	}
	if body, _ := gen["body"].(string); strings.Contains(body, "您好") || strings.Contains(body, "{{") {
		t.Fatalf("generate returned placeholder copy: %v", gen)
	}

	saved := h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/email", sessionOwnerA, tenant,
		`{"body":"  `+secret+`  "}`, 200)
	if saved["body"] != secret || saved["sent"] == true || saved["published"] == true || saved["marketing_permitted"] == true || saved["generated"] == true {
		t.Fatalf("saved: %v", saved)
	}
	edited := h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/email", sessionOwnerA, tenant,
		`{"body":"手改后的邮件"}`, 200)
	if edited["body"] != "手改后的邮件" || edited["user_confirmed"] == true || edited["content_version"].(float64) != 2 {
		t.Fatalf("edited: %v", edited)
	}
	confirmed := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/email/confirm", sessionOwnerA, tenant, `{}`, 200)
	if confirmed["user_confirmed"] != true || confirmed["sent"] == true || confirmed["published"] == true || confirmed["marketing_permitted"] == true {
		t.Fatalf("confirm: %v", confirmed)
	}
	status, _, _ = h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/email/send", sessionOwnerA, tenant, `{}`)
	if status != 404 {
		t.Fatalf("send route exists: %d", status)
	}

	var bad int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM light_copy_drafts WHERE sent!=0 OR published!=0 OR marketing_permitted!=0 OR generated!=0 OR live_charge!=0`).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Fatalf("stored a send, publish, consent, generation, or charge")
	}
	if strings.Contains(h.logs.String(), secret) {
		t.Fatalf("log leaked the draft")
	}

	status, _, _ = h.do("GET", "/api/v1/opportunities/"+opp+"/light-copy", sessionOwnerB, tenant, "")
	if status != 403 {
		t.Fatalf("cross tenant = %d", status)
	}
}

func TestLightCopyFactsAndHandoffStayInsideTheScene(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, _ := h.seed()
	intake := h.intake(sessionOwnerA, tenant, `{"source_app":"touch","source_ns":"landing","event_id":"evt-copy-1","contact":{"name":"不应进工具的联系人全文","phone":"13900001111","email":"secret@shop.cn"},"business_category":"merchant_customer","source_type":"touch_campaign","source":{"source_app":"touch","source_ref":"camp-9","auth_scope_snapshot":"profile-secret-9f3a"},"consent":{"source_submission_ref":"sub-copy","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":false}}`, 201)
	leadID, _ := intake["lead_id"].(string)
	if leadID == "" {
		t.Fatalf("intake: %v", intake)
	}

	status, facts, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/light-copy/marketing_brief/facts", sessionOwnerA, tenant, `{"selected":["activity_ref","contact_text"]}`)
	if status != 400 || facts["error"] != "unauthorized_field" {
		t.Fatalf("unauthorized = %d %v", status, facts)
	}
	raw := jsonText(facts)
	if strings.Contains(raw, "不应进工具") || strings.Contains(raw, "13900001111") || strings.Contains(raw, "profile-secret-9f3a") || strings.Contains(raw, "secret@shop.cn") {
		t.Fatalf("refusal leaked contact or profile: %s", raw)
	}

	okFacts := h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/light-copy/marketing_brief/facts", sessionOwnerA, tenant, `{"selected":["activity_ref"]}`, 200)
	packet, _ := okFacts["facts"].(map[string]any)
	if packet["activity_ref"] != "camp-9" || len(packet) != 1 {
		t.Fatalf("facts = %v", okFacts)
	}
	if strings.Contains(jsonText(okFacts), "不应进工具") || strings.Contains(jsonText(okFacts), "13900001111") || strings.Contains(jsonText(okFacts), "profile-secret-9f3a") || strings.Contains(jsonText(okFacts), "secret@shop.cn") {
		t.Fatalf("packet leaked contact: %v", okFacts)
	}

	opp := h.createOpp(t, sessionOwnerA, tenant, h.mustContact(t, tenant), "门店开业", "merchant_customer", "")
	status, price, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/facts", sessionOwnerA, tenant, `{"selected":["price_cents"]}`)
	if status != 400 || price["error"] != "price_expired" {
		t.Fatalf("price = %d %v", status, price)
	}

	h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant, `{"body":"可保存的回复"}`, 200)
	declined := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant,
		`{"tool":"product_image","idempotency_key":"decline-1","decline":true}`, 200)
	if declined["status"] != "declined" || declined["project_created"] == true {
		t.Fatalf("decline: %v", declined)
	}
	still := h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant, `{"body":"拒绝后继续手改"}`, 200)
	if still["body"] != "拒绝后继续手改" || still["sent"] == true {
		t.Fatalf("edit after decline: %v", still)
	}

	first := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant,
		`{"tool":"goboost","idempotency_key":"open-1","selected":["title"]}`, 200)
	if first["status"] != "failed" || first["reason"] != "tool_entry_unavailable" || first["project_created"] == true || first["regenerated"] == true {
		t.Fatalf("handoff: %v", first)
	}
	if first["source_id"] != opp || first["source_kind"] != "opportunity" {
		t.Fatalf("source dropped: %v", first)
	}
	second := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant,
		`{"tool":"goboost","idempotency_key":"open-1","selected":["title"]}`, 200)
	if second["id"] != first["id"] || second["regenerated"] == true {
		t.Fatalf("replay: %v", second)
	}
	var projects int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM light_copy_handoffs WHERE tenant_id=? AND idempotency_key='open-1'`, tenant).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if projects != 1 {
		t.Fatalf("handoff rows = %d", projects)
	}
	var generated int
	if err := h.api.St.DB.QueryRow(`SELECT generated FROM light_copy_drafts WHERE tenant_id=? AND subject_id=? AND kind='reply'`, tenant, opp).Scan(&generated); err != nil {
		t.Fatal(err)
	}
	if generated != 0 {
		t.Fatalf("failed handoff regenerated the draft")
	}

	status, unknown, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/handoff", sessionOwnerA, tenant,
		`{"tool":"video-engine","idempotency_key":"video-1","selected":["title"]}`)
	if status != 400 || unknown["error"] != "unknown_tool" {
		t.Fatalf("unknown tool = %d %v", status, unknown)
	}
}

func (h *harness) mustContact(t *testing.T, tenant string) string {
	t.Helper()
	out := h.mustDo("GET", "/api/v1/contacts", sessionOwnerA, tenant, "", 200)
	items, _ := out["items"].([]any)
	if len(items) == 0 {
		t.Fatal("no contact")
	}
	id, _ := items[0].(map[string]any)["id"].(string)
	return id
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// ---- HUI-1682: the platform task is the only live text seam (2026-10-11). ----

type stubTextGenerator struct {
	last  platformtask.GenerateInput
	calls int
	fn    func(in platformtask.GenerateInput) platformtask.GenerateResult
}

func (s *stubTextGenerator) Generate(_ context.Context, in platformtask.GenerateInput) platformtask.GenerateResult {
	s.calls++
	s.last = in
	return s.fn(in)
}

func TestLightCapabilityReportsConfiguredSeam(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, _ := h.seed()
	cap := h.mustDo("GET", "/api/v1/light-copy/capability", sessionOwnerA, tenant, "", 200)
	if cap["model_ready"] != false || cap["provider"] != nil {
		t.Fatalf("unconfigured capability must stay honest: %v", cap)
	}
	if cap["enabled"] != true {
		t.Fatalf("capability on must report enabled=true: %v", cap)
	}
	h.api.LightCopyPhraser = &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		return platformtask.GenerateResult{TaskID: "t", BillingVerdict: "not_completed"}
	}}
	ready := h.mustDo("GET", "/api/v1/light-copy/capability", sessionOwnerA, tenant, "", 200)
	if ready["model_ready"] != true || ready["provider"] != "platformtask:text.generate" {
		t.Fatalf("configured capability = %v", ready)
	}
	if ready["live_charge"].(float64) != 0 {
		t.Fatalf("live_charge must stay 0: %v", ready)
	}
}

func TestLightGenerateRecordsModelProvenanceAndCharge(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "门店开业", "merchant_customer", "")
	gen := &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		return platformtask.GenerateResult{TaskID: "tsk_lc_ok", Text: "根据已核实事实写好的开业回复", CostCents: 1, BillingVerdict: "recorded"}
	}}
	h.api.LightCopyPhraser = gen

	out := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerA, tenant, `{"selected":["title"]}`, 200)
	draft := out["draft"].(map[string]any)
	if draft["body"] != "根据已核实事实写好的开业回复" || draft["generated"] != true || draft["origin"] != "model" {
		t.Fatalf("draft provenance = %v", draft)
	}
	if draft["live_charge"].(float64) != 1 || draft["content_version"].(float64) != 1 {
		t.Fatalf("draft charge/version = %v", draft)
	}
	if draft["user_confirmed"] == true || draft["sent"] == true || draft["published"] == true || draft["marketing_permitted"] == true {
		t.Fatalf("generated draft must stay unconfirmed and unsent: %v", draft)
	}
	model := out["model"].(map[string]any)
	if model["task_id"] != "tsk_lc_ok" || model["billing_verdict"] != "recorded" || model["cost_cents"].(float64) != 1 {
		t.Fatalf("model receipt = %v", model)
	}
	if gen.calls != 1 {
		t.Fatalf("generator calls = %d", gen.calls)
	}
	if !strings.Contains(gen.last.Prompt, "门店开业") || strings.Contains(gen.last.Prompt, "{{") {
		t.Fatalf("prompt = %q", gen.last.Prompt)
	}
	if !strings.HasPrefix(gen.last.IdempotencyKey, "lightcopy:opportunity:"+opp+":reply:") {
		t.Fatalf("idempotency key = %q", gen.last.IdempotencyKey)
	}

	// Confirm keeps provenance and the recorded charge; it still does not send.
	confirmed := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/confirm", sessionOwnerA, tenant, `{}`, 200)
	if confirmed["user_confirmed"] != true || confirmed["generated"] != true || confirmed["origin"] != "model" || confirmed["live_charge"].(float64) != 1 {
		t.Fatalf("confirm erased provenance: %v", confirmed)
	}

	// A manual edit resets provenance and charge.
	edited := h.mustDo("PUT", "/api/v1/opportunities/"+opp+"/light-copy/reply", sessionOwnerA, tenant, `{"body":"人工改稿"}`, 200)
	if edited["generated"] == true || edited["origin"] != "manual" || edited["live_charge"].(float64) != 0 {
		t.Fatalf("manual edit must reset provenance: %v", edited)
	}

	var bad int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM light_copy_drafts WHERE sent!=0 OR published!=0 OR marketing_permitted!=0`).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Fatalf("a draft row claims send, publish, or consent")
	}
	var inconsistent int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM light_copy_drafts WHERE generated!=0 AND (origin!='model' OR live_charge<=0)`).Scan(&inconsistent); err != nil {
		t.Fatal(err)
	}
	if inconsistent != 0 {
		t.Fatalf("generated provenance without a recorded charge")
	}
}

func TestLightGenerateHonestFailures(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "门店开业", "merchant_customer", "")
	cases := []struct {
		name    string
		result  platformtask.GenerateResult
		status  int
		errCode string
	}{
		{"not_completed", platformtask.GenerateResult{TaskID: "tsk_nc", BillingVerdict: "not_completed"}, 409, "generation_not_completed"},
		{"recorded_empty_text", platformtask.GenerateResult{TaskID: "tsk_empty", CostCents: 1, BillingVerdict: "recorded", Text: "   "}, 409, "model_output_unusable"},
		{"placeholder", platformtask.GenerateResult{TaskID: "tsk_ph", CostCents: 1, BillingVerdict: "recorded", Text: "您好{{name}}，这是模板占位"}, 409, "placeholder_refused"},
	}
	for _, tc := range cases {
		gen := &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult { return tc.result }}
		h.api.LightCopyPhraser = gen
		status, got, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerA, tenant, `{"selected":["title"]}`)
		if status != tc.status || got["error"] != tc.errCode {
			t.Fatalf("%s = %d %v", tc.name, status, got)
		}
		model := got["model"].(map[string]any)
		if model["billing_verdict"] != tc.result.BillingVerdict || model["task_id"] != tc.result.TaskID {
			t.Fatalf("%s receipt = %v", tc.name, model)
		}
		if model["cost_cents"].(float64) != float64(tc.result.CostCents) {
			t.Fatalf("%s cost = %v", tc.name, model["cost_cents"])
		}
		var rows int
		if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM light_copy_drafts WHERE tenant_id=? AND subject_id=? AND kind='reply'`, tenant, opp).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 0 {
			t.Fatalf("%s wrote a draft row on failure", tc.name)
		}
	}
}

func TestLightGenerateFactGateRunsBeforeTheMeter(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "门店开业", "merchant_customer", "")
	gen := &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		return platformtask.GenerateResult{TaskID: "t", BillingVerdict: "recorded", Text: "正文", CostCents: 1}
	}}
	h.api.LightCopyPhraser = gen

	status, got, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerA, tenant, `{"selected":["contact_text"]}`)
	if status != 400 || got["error"] != "unauthorized_field" {
		t.Fatalf("unauthorized = %d %v", status, got)
	}
	status, got, _ = h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerA, tenant, `{"selected":[]}`)
	if status != 400 || got["error"] != "nothing_selected" {
		t.Fatalf("nothing = %d %v", status, got)
	}
	if gen.calls != 0 {
		t.Fatalf("fact gate must run before the metered call: %d", gen.calls)
	}
}

func TestLightGenerateCrossTenantRefused(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true})
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "门店开业", "merchant_customer", "")
	gen := &stubTextGenerator{fn: func(in platformtask.GenerateInput) platformtask.GenerateResult {
		return platformtask.GenerateResult{TaskID: "t", BillingVerdict: "recorded", Text: "正文", CostCents: 1}
	}}
	h.api.LightCopyPhraser = gen
	status, _, _ := h.do("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerB, tenant, `{"selected":["title"]}`)
	if status != 403 {
		t.Fatalf("cross tenant generate = %d", status)
	}
	if gen.calls != 0 {
		t.Fatalf("cross tenant reached the meter")
	}
}

// Open() wires the light-copy seam from the three platform task keys, with the
// capability/model taken from the text-specific env overrides.
func TestLightGenerateThroughOpenWiredStandInTask(t *testing.T) {
	var mu sync.Mutex
	prompts := map[string]string{}
	taskSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body struct {
				IdempotencyKey string         `json:"idempotency_key"`
				Params         map[string]any `json:"params"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			mu.Lock()
			prompts[body.IdempotencyKey] = body.Params["prompt"].(string)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "tsk_open_1", "status": "QUEUED"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task_id": "tsk_open_1", "status": "SUCCEEDED",
			"result_json": `{"text":"按真实任务返回的正文"}`,
		})
	}))
	defer taskSrv.Close()

	h := newHarnessOpts(t, harnessOpts{featureLightCopy: true, platformTaskBase: taskSrv.URL})
	if h.api.LightCopyPhraser == nil || h.api.ReceptionPhraser == nil {
		t.Fatalf("Open did not wire the task seams")
	}
	if h.cfg.TaskTextCapability != "leads.text.standin" {
		t.Fatalf("text capability = %q", h.cfg.TaskTextCapability)
	}
	tenant, _, contact := h.seed()
	opp := h.createOpp(t, sessionOwnerA, tenant, contact, "门店开业", "merchant_customer", "")
	cap := h.mustDo("GET", "/api/v1/light-copy/capability", sessionOwnerA, tenant, "", 200)
	if cap["model_ready"] != true || cap["provider"] != "platformtask:leads.text.standin" {
		t.Fatalf("capability = %v", cap)
	}
	out := h.mustDo("POST", "/api/v1/opportunities/"+opp+"/light-copy/reply/generate", sessionOwnerA, tenant, `{"selected":["title"]}`, 200)
	if out["draft"].(map[string]any)["body"] != "按真实任务返回的正文" {
		t.Fatalf("generate = %v", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 1 {
		t.Fatalf("prompts = %v", prompts)
	}
	for key, prompt := range prompts {
		if !strings.Contains(prompt, "门店开业") {
			t.Fatalf("prompt %q lost the allowed fact", key)
		}
	}
}
