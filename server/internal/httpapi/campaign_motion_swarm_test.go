package httpapi

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func seedSwarmCampaignLead(t *testing.T, h *harness, tenant, eventSuffix string) (leadID, contactID string) {
	t.Helper()
	intake := h.intake(sessionOwnerA, tenant, `{
		"source_app":"touch","source_ns":"landing","event_id":"evt-swarm-`+eventSuffix+`",
		"contact":{"name":"私密客户甲","phone":"13911112222","email":"private.client@merchant.cn"},
		"business_category":"merchant_customer","source_type":"touch_campaign",
		"source":{"source_app":"touch","source_ref":"camp-swarm-`+eventSuffix+`","auth_scope_snapshot":"secret-scope-`+eventSuffix+`"},
		"consent":{"source_submission_ref":"sub-swarm-`+eventSuffix+`","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":false}
	}`, 201)
	leadID, _ = intake["lead_id"].(string)
	contactID, _ = intake["contact_id"].(string)
	if leadID == "" || contactID == "" {
		t.Fatalf("seedSwarmCampaignLead failed: %v", intake)
	}
	return leadID, contactID
}

func seedPaidEntitlement(t *testing.T, h *harness, tenant string) {
	t.Helper()
	h.mustDo("PUT", "/api/v1/subscription/cache", sessionOwnerA, tenant, `{
		"plan":"crm_pro",
		"status":"active",
		"wallet_balance_cents":500000,
		"org_billing_account_id":"acct_org_a",
		"authorized_payer_account_id":"acct_auth_a",
		"crm_subscription_cents":9900,
		"merchant_deal_cents":0
	}`, 200)
}

func TestCampaignMotionSwarmHandoffAndPrivacyGuard(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureCampaignMotion: true, captureLog: true})
	tenantA, _, _ := h.seed()
	seedPaidEntitlement(t, h, tenantA)
	leadID, _ := seedSwarmCampaignLead(t, h, tenantA, "privacy")
	digest := "sha256:" + strings.Repeat("12", 32)

	// 1) Restricted minimal context handoff with brand/product facts, assets, budget, payer, return_url, and parametric variants.
	body := `{
		"tenant_id": "` + tenantA + `",
		"campaign_goal": "冬季门店动效推广",
		"target_audience": "全国连锁餐饮加盟商",
		"approved_brand_facts": [
			{"key":"brand_style","value":"暖色调简洁动效规范","confirmed":true}
		],
		"approved_product_facts": [
			{"key":"product_highlight","value":"3分钟热饮出杯方案","confirmed":true}
		],
		"approved_assets": [
			{"id":"asset_logo_v2","authorized":true,"version":"v2.0","expires_at":"2028-01-01T00:00:00Z"},
			{"id":"asset_cup_v1","authorized":true,"version":"v1.4","expires_at":"2028-01-01T00:00:00Z"}
		],
		"cta": "预约品鉴",
		"target_channels": ["抖音","视频号","小红书"],
		"channel_variants": [
			{"channel":"抖音","aspect_ratio":"9:16","layout_preset":"vertical_hook","store_label":"上海静安店","asset_slot_overrides":{"logo":"asset_logo_v2"}},
			{"channel":"视频号","aspect_ratio":"16:9","layout_preset":"landscape_story","asset_slot_overrides":{"product":"asset_cup_v1"}},
			{"channel":"小红书","aspect_ratio":"3:4","layout_preset":"card_cover","cta_override":"到店打卡"}
		],
		"budget": {"budget_attribution_id":"budget_winter_01","max_budget_cents":2000},
		"payer": {"payer_kind":"organization","payer_account_id":"acct_org_a"},
		"return_url": "/campaigns/` + leadID + `/motion",
		"source_facts_version": "facts_2026_11_v1",
		"execution_preference": "night",
		"expires_at": "2028-01-01T00:00:00Z",
		"motion": {"project_id":"prj_winter_1","revision_id":"rev_winter_1","campaign_id":"` + leadID + `","digest":"` + digest + `"}
	}`
	saved := h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenantA, body, 200)
	export, _ := saved["export"].(map[string]any)
	exportText := jsonText(export)
	for _, leaked := range []string{"13911112222", "private.client@merchant.cn", "私密客户甲", "secret-scope-privacy"} {
		if strings.Contains(exportText, leaked) {
			t.Fatalf("CRM privacy leaked into export: %s", leaked)
		}
	}
	if saved["model_calls"].(float64) != 0 || saved["creative_project_writes"].(float64) != 0 {
		t.Fatalf("handoff must not call model or write creative project: %v", saved)
	}
	if export["return_url"] != "/campaigns/"+leadID+"/motion" || export["source_facts_version"] != "facts_2026_11_v1" {
		t.Fatalf("return_url/source_facts_version missing: %v", export)
	}

	// 2) Privacy smuggling in brand/product facts and return_url is rejected fail-closed (422 privacy_blocked).
	status, out, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenantA, `{
		"campaign_goal": "夹带手机号",
		"approved_brand_facts": [{"key":"contact","value":"联系张总 13899990000","confirmed":true}],
		"target_channels": ["抖音"],
		"budget_attribution_id": "budget_winter_01",
		"motion": {"project_id":"prj_winter_1","revision_id":"rev_winter_1","campaign_id":"`+leadID+`","digest":"`+digest+`"}
	}`)
	if status != 422 || stringOf(errCode(out)) != "privacy_blocked" {
		t.Fatalf("brand fact phone smuggling got %d %v, want 422 privacy_blocked", status, out)
	}

	// 3) Wrong tenant, expired timestamp, missing revision/digest fail closed.
	status, out, _ = h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenantA, `{
		"tenant_id": "tnt_other",
		"campaign_goal": "跨租户",
		"channels": ["抖音"],
		"budget_attribution_id": "budget_winter_01",
		"motion": {"project_id":"prj_winter_1","revision_id":"rev_winter_1","campaign_id":"`+leadID+`","digest":"`+digest+`"}
	}`)
	if status != 403 || stringOf(errCode(out)) != "foreign_tenant" {
		t.Fatalf("wrong tenant got %d %v, want 403 foreign_tenant", status, out)
	}

	status, out, _ = h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenantA, `{
		"expires_at": "2020-01-01T00:00:00Z",
		"campaign_goal": "已过期",
		"channels": ["抖音"],
		"budget_attribution_id": "budget_winter_01",
		"motion": {"project_id":"prj_winter_1","revision_id":"rev_winter_1","campaign_id":"`+leadID+`","digest":"`+digest+`"}
	}`)
	if status != 422 || stringOf(errCode(out)) != "variant_expired" {
		t.Fatalf("expired handoff got %d %v, want 422 variant_expired", status, out)
	}

	// 4) Server logs must never contain contact phone/email/chat secrets.
	logText := h.logs.String()
	for _, secret := range []string{"13911112222", "13899990000", "private.client@merchant.cn", "私密客户甲"} {
		if strings.Contains(logText, secret) {
			t.Fatalf("server log leaked CRM privacy %q", secret)
		}
	}
}

func TestCampaignMotionMultiChannelVariantsNonLinearGuardAndAttribution(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureCampaignMotion: true})
	tenantA, _, _ := h.seed()
	seedPaidEntitlement(t, h, tenantA)
	leadID, _ := seedSwarmCampaignLead(t, h, tenantA, "variants")
	digest := "sha256:" + strings.Repeat("34", 32)

	handoffBody := `{
		"campaign_goal": "多渠道参数化变体",
		"target_channels": ["抖音","视频号","小红书","快手"],
		"approved_assets": [{"id":"asset_1","authorized":true,"version":"v1"}],
		"budget": {"budget_attribution_id":"budget_multi","max_budget_cents":1000},
		"payer": {"payer_kind":"organization","payer_account_id":"acct_org_a"},
		"source_facts_version": "facts_v9",
		"motion": {"project_id":"prj_multi_1","revision_id":"rev_multi_1","campaign_id":"` + leadID + `","digest":"` + digest + `"}
	}`
	h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenantA, handoffBody, 200)

	// 1) Baseline render (1 high-cost model call) + 3 parametric channel variants (0 high-cost model calls each).
	h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/render-backflow", sessionOwnerA, tenantA, `{
		"idempotency_key":"rnd-key-1","campaign_id":"`+leadID+`","creative_id":"crv_main",
		"project_id":"prj_multi_1","revision_id":"rev_multi_1","digest":"`+digest+`",
		"render_id":"rnd_douyin","variant_id":"var_douyin","channel":"抖音",
		"source_facts_version":"facts_v9","render_mode":"baseline",
		"high_cost_model_calls":1,"billed_generation_count":1,"cost_cents":400,
		"billing_receipt_id":"rcpt_1","payer_ref":"acct_org_a","budget_attribution_id":"budget_multi"
	}`, 200)

	for i, ch := range []string{"视频号", "小红书", "快手"} {
		h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/render-backflow", sessionOwnerA, tenantA, fmt.Sprintf(`{
			"idempotency_key":"rnd-key-p-%d","campaign_id":"%s","creative_id":"crv_main",
			"project_id":"prj_multi_1","revision_id":"rev_multi_1","digest":"%s",
			"render_id":"rnd_p_%d","variant_id":"var_p_%d","channel":"%s",
			"source_facts_version":"facts_v9","render_mode":"parametric_variant",
			"high_cost_model_calls":0,"billed_generation_count":0,"cost_cents":50,
			"billing_receipt_id":"rcpt_p_%d","payer_ref":"acct_org_a","budget_attribution_id":"budget_multi"
		}`, i, leadID, digest, i, i, ch, i), 200)
	}

	// 2) Parametric variant trying to bill linear high-cost model calls is blocked (422).
	status, out, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/render-backflow", sessionOwnerA, tenantA, `{
		"idempotency_key":"rnd-key-spike","campaign_id":"`+leadID+`","creative_id":"crv_spike",
		"project_id":"prj_multi_1","revision_id":"rev_multi_1","digest":"`+digest+`",
		"render_id":"rnd_spike","variant_id":"var_spike","channel":"视频号",
		"source_facts_version":"facts_v9","render_mode":"parametric_variant",
		"high_cost_model_calls":1,"billed_generation_count":1,"cost_cents":400,
		"billing_receipt_id":"rcpt_spike","payer_ref":"acct_org_a","budget_attribution_id":"budget_multi"
	}`)
	if status != 422 || stringOf(errCode(out)) != "linear_model_call_blocked" {
		t.Fatalf("linear model call spike got %d %v, want 422 linear_model_call_blocked", status, out)
	}

	// 3) Duplicate billing receipt across another variant is blocked (409).
	status, out, _ = h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/render-backflow", sessionOwnerA, tenantA, `{
		"idempotency_key":"rnd-key-dup-rcpt","campaign_id":"`+leadID+`","creative_id":"crv_other",
		"project_id":"prj_multi_1","revision_id":"rev_multi_1","digest":"`+digest+`",
		"render_id":"rnd_dup","variant_id":"var_dup","channel":"抖音",
		"source_facts_version":"facts_v9","render_mode":"parametric_variant",
		"high_cost_model_calls":0,"billed_generation_count":0,"cost_cents":50,
		"billing_receipt_id":"rcpt_1","payer_ref":"acct_org_a","budget_attribution_id":"budget_multi"
	}`)
	if status != 409 || stringOf(errCode(out)) != "duplicate_charge" {
		t.Fatalf("duplicate charge got %d %v, want 409 duplicate_charge", status, out)
	}

	// 4) Exceeding budget cap is blocked (422 budget_exceeded).
	status, out, _ = h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/render-backflow", sessionOwnerA, tenantA, `{
		"idempotency_key":"rnd-key-over","campaign_id":"`+leadID+`","creative_id":"crv_over",
		"project_id":"prj_multi_1","revision_id":"rev_multi_1","digest":"`+digest+`",
		"render_id":"rnd_over","variant_id":"var_over","channel":"快手",
		"source_facts_version":"facts_v9","render_mode":"parametric_variant",
		"high_cost_model_calls":0,"billed_generation_count":0,"cost_cents":600,
		"billing_receipt_id":"rcpt_over","payer_ref":"acct_org_a","budget_attribution_id":"budget_multi"
	}`)
	if status != 422 || stringOf(errCode(out)) != "budget_exceeded" {
		t.Fatalf("over budget got %d %v, want 422 budget_exceeded", status, out)
	}

	// 5) Accept two renders and record Leads-side conversion attribution; verify no creative project writeback.
	h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/renders/crv_main/decision", sessionOwnerA, tenantA, `{"status":"accepted","variant_id":"var_douyin"}`, 200)
	h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/renders/crv_main/decision", sessionOwnerA, tenantA, `{"status":"accepted","variant_id":"var_p_0"}`, 200)

	// Refusal if trying to write conversion effectiveness back to creative project.
	status, out, _ = h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/conversion", sessionOwnerA, tenantA, `{"leads":20,"conversions":5,"revenue_cents":88000,"writeback_creative_fact":true}`)
	if status != 422 || stringOf(errCode(out)) != "creative_writeback_forbidden" {
		t.Fatalf("writeback creative fact got %d %v, want 422 creative_writeback_forbidden", status, out)
	}

	convResp := h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/conversion", sessionOwnerA, tenantA, `{"leads":20,"conversions":5,"revenue_cents":88000}`, 200)
	if convResp["creative_project_writes"].(float64) != 0 {
		t.Fatalf("conversion updated creative project: %v", convResp)
	}
	if strings.Contains(jsonText(convResp["export"]), "88000") {
		t.Fatalf("conversion leaked into export_json: %v", convResp["export"])
	}
	attr, _ := convResp["attribution"].(map[string]any)
	if attr["authority"] != "leads_only" || attr["total_high_cost_model_calls"].(float64) != 1 || attr["unique_channels"].(float64) != 4 {
		t.Fatalf("attribution mismatch: %v", attr)
	}
	if attr["cost_per_accepted_creative_cents"].(float64) != 275 || attr["cost_per_channel_variant_cents"].(float64) != 137 {
		t.Fatalf("cost metrics mismatch (550/2=275, 550/4=137): %v", attr)
	}
	renders, _ := convResp["render_candidates"].([]any)
	if len(renders) != 4 {
		t.Fatalf("expected 4 renders, got %d", len(renders))
	}
	firstRender, _ := renders[0].(map[string]any)
	for _, k := range []string{"campaign_id", "creative_id", "project_id", "revision_id", "render_id", "variant_id", "channel", "source_facts_version"} {
		if stringOf(firstRender[k]) == "" {
			t.Fatalf("missing attribution key %s in render candidate: %v", k, firstRender)
		}
	}
}

func TestCampaignMotionRepeatMethodPrivateTemplateBoundary(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureCampaignMotion: true})
	tenantA, _, _ := h.seed()
	leadID, _ := seedSwarmCampaignLead(t, h, tenantA, "repeat")
	digest := "sha256:" + strings.Repeat("56", 32)

	h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenantA, `{
		"campaign_goal": "门店复用",
		"channels": ["抖音","视频号"],
		"budget_attribution_id": "budget_rep",
		"source_facts_version": "facts_rep_v1",
		"motion": {"project_id":"prj_rep_1","revision_id":"rev_rep_1","campaign_id":"`+leadID+`","digest":"`+digest+`"}
	}`, 200)

	// Private customer work CANNOT become public template or cross-customer template.
	for _, bad := range []string{
		`{"mode":"same_campaign_different_store","name":"私作转公","template_id":"tpl_priv_1","template_visibility":"private_customer_work","public_template":true}`,
		`{"mode":"same_product_different_channel","name":"自动晋升","template_id":"tpl_priv_2","template_visibility":"private_customer_work","promote_to_public":true}`,
		`{"mode":"same_campaign_different_store","name":"跨租户","template_id":"tpl_priv_3","template_visibility":"private_customer_work","share_cross_tenant":true}`,
		`{"mode":"cross_customer_template","name":"私作跨客","template_id":"tpl_priv_4","template_visibility":"private_customer_work"}`,
	} {
		status, out, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/repeat-methods", sessionOwnerA, tenantA, bad)
		if status != 403 || stringOf(errCode(out)) != "private_template_forbidden" {
			t.Fatalf("private template violation got %d %v, want 403 private_template_forbidden (body=%s)", status, out, bad)
		}
	}

	// Authorized team template without grant id is rejected.
	status, out, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/repeat-methods", sessionOwnerA, tenantA, `{
		"mode":"cross_customer_template","name":"缺授权团队模","template_id":"tpl_team_1","template_visibility":"authorized_team_template"
	}`)
	if status != 403 || stringOf(errCode(out)) != "repeat_method_forbidden" {
		t.Fatalf("missing team grant got %d %v, want 403 repeat_method_forbidden", status, out)
	}

	// Valid repeat methods succeed and private customer work stays public_template=false.
	savedPriv := h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/repeat-methods", sessionOwnerA, tenantA, `{
		"mode":"same_campaign_different_store","name":"同活动不同门店","template_id":"tpl_store_1",
		"template_visibility":"private_customer_work","parametric_slots":{"store_label":"深圳南山店"}
	}`, 200)
	rm, _ := savedPriv["repeat_method"].(map[string]any)
	if rm["public_template"] != false {
		t.Fatalf("private customer work must have public_template=false: %v", rm)
	}

	h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/repeat-methods", sessionOwnerA, tenantA, `{
		"mode":"cross_customer_template","name":"授权团队模板","template_id":"tpl_team_ok",
		"template_visibility":"authorized_team_template","team_grant_id":"grant_team_99","grant_expires_at":"2028-01-01T00:00:00Z"
	}`, 200)

	h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/repeat-methods", sessionOwnerA, tenantA, `{
		"mode":"cross_customer_template","name":"平台标准模板","template_id":"tpl_plat_1",
		"template_visibility":"platform_template","public_template":true
	}`, 200)
}

func TestCampaignMotionMXOPSEntitlementAndAgentAntiEscalation(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureCampaignMotion: true})
	tenantA, _, _ := h.seed()
	leadID, _ := seedSwarmCampaignLead(t, h, tenantA, "entitlement")
	digest := "sha256:" + strings.Repeat("78", 32)
	path := "/api/v1/campaigns/" + leadID + "/motion-handoff"

	reqWithPref := func(pref, extra string) string {
		e := ""
		if extra != "" {
			e = "," + extra
		}
		return `{
			"campaign_goal": "策略核验",
			"channels": ["抖音"],
			"budget_attribution_id": "budget_ent",
			"execution_preference": "` + pref + `",
			"motion": {"project_id":"prj_ent_1","revision_id":"rev_ent_1","campaign_id":"` + leadID + `","digest":"` + digest + `"}` + e + `
		}`
	}

	// 1) Free account (no active paid plan) cannot access internal or unverified priority/night/idle.
	status, out, _ := h.do("POST", path, sessionOwnerA, tenantA, reqWithPref("internal", ""))
	if status != 403 || stringOf(errCode(out)) != "internal_privilege_forbidden" {
		t.Fatalf("free internal got %d %v, want 403 internal_privilege_forbidden", status, out)
	}
	for _, pref := range []string{"priority", "night", "idle"} {
		status, out, _ = h.do("POST", path, sessionOwnerA, tenantA, reqWithPref(pref, ""))
		if status != 403 || stringOf(errCode(out)) != "entitlement_denied" {
			t.Fatalf("free %s got %d %v, want 403 entitlement_denied", pref, status, out)
		}
	}

	// Global resource config is rejected.
	status, out, _ = h.do("POST", path, sessionOwnerA, tenantA, reqWithPref("immediate", `"gpu_pool":"h100_pool"`))
	if status != 403 || stringOf(errCode(out)) != "global_resource_forbidden" {
		t.Fatalf("global resource config got %d %v, want 403 global_resource_forbidden", status, out)
	}

	// 2) Active paid entitlement unlocks night/idle/priority for owner, still forbids internal.
	seedPaidEntitlement(t, h, tenantA)
	okResp := h.mustDo("POST", path, sessionOwnerA, tenantA, reqWithPref("night", ""), 200)
	respText := jsonText(okResp)
	for _, forbidden := range []string{"gpu_pool", "cluster_capacity", "global_queue_depth", "worker_pool", "internal_priority"} {
		if strings.Contains(respText, forbidden) {
			t.Fatalf("response exposed global resource field %q: %s", forbidden, respText)
		}
	}
	status, out, _ = h.do("POST", path, sessionOwnerA, tenantA, reqWithPref("internal", ""))
	if status != 403 || stringOf(errCode(out)) != "internal_privilege_forbidden" {
		t.Fatalf("paid internal got %d %v, want 403 internal_privilege_forbidden", status, out)
	}

	// 3) Grant agent access and assign lead to agent; agent STILL cannot escalate to priority or night->immediate.
	h.mustDo("POST", "/api/v1/admin/agent-grants", sessionOwnerA, tenantA, `{"principal_ref":"`+principalAgentA+`"}`, 201)
	var agentMemberID string
	if err := h.api.St.DB.QueryRow(`SELECT id FROM members WHERE tenant_id=? AND principal_ref=?`, tenantA, principalAgentA).Scan(&agentMemberID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.api.St.DB.Exec(`UPDATE leads SET assigned_member_id=? WHERE id=? AND tenant_id=?`, agentMemberID, leadID, tenantA); err != nil {
		t.Fatal(err)
	}

	status, out, _ = h.do("POST", path, sessionAgentA, tenantA, reqWithPref("priority", ""))
	if status != 403 || stringOf(errCode(out)) != "agent_escalation_forbidden" {
		t.Fatalf("agent priority got %d %v, want 403 agent_escalation_forbidden", status, out)
	}
	status, out, _ = h.do("POST", path, sessionAgentA, tenantA, reqWithPref("immediate", ""))
	if status != 403 || stringOf(errCode(out)) != "agent_escalation_forbidden" {
		t.Fatalf("agent escalation night->immediate got %d %v, want 403 agent_escalation_forbidden", status, out)
	}
	// Agent keeping same/lower preference (idle) is allowed.
	h.mustDo("POST", path, sessionAgentA, tenantA, reqWithPref("idle", ""), 200)
}

func TestCampaignMotionNightQueueWaitingReplayAndNonBlockingCRM(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureCampaignMotion: true, featureFollowups: true})
	tenantA, _, _ := h.seed()
	seedPaidEntitlement(t, h, tenantA)
	leadID, contactID := seedSwarmCampaignLead(t, h, tenantA, "nightq")
	digest := "sha256:" + strings.Repeat("9a", 32)

	// 1) Handoff with night preference and pinned authorized asset versions.
	h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenantA, `{
		"campaign_goal": "夜间闲时渲染素材包",
		"approved_assets": [
			{"id":"asset_brand_1","authorized":true,"version":"v4.2","expires_at":"2028-01-01T00:00:00Z"}
		],
		"target_channels": ["抖音","视频号"],
		"budget": {"budget_attribution_id":"budget_night","max_budget_cents":2000},
		"execution_preference": "night",
		"motion": {"project_id":"prj_night_1","revision_id":"rev_night_1","campaign_id":"`+leadID+`","digest":"`+digest+`"}
	}`, 200)

	// 2) Night queue backflow with nullable eta_seconds: null; auto retains pinned asset versions.
	nightBackflowBody := `{
		"idempotency_key": "q-night-1",
		"job_id": "job_night_001",
		"creative_id": "crv_night_001",
		"campaign_id": "` + leadID + `",
		"project_id": "prj_night_1",
		"revision_id": "rev_night_1",
		"digest": "` + digest + `",
		"execution_preference": "night",
		"phase": "scheduled_night",
		"queue_state": "waiting_window",
		"wait_reason": "night_window_scheduled",
		"next_window": "2026-11-28T01:00:00Z/2026-11-28T06:00:00Z",
		"eta_seconds": null,
		"policy_version": "mxops-policy-v2026.11"
	}`
	qResp := h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/queue-backflow", sessionOwnerA, tenantA, nightBackflowBody, 200)
	jq, _ := qResp["job_queue_view"].(map[string]any)
	if jq == nil || jq["queue_state"] != "waiting_window" || jq["policy_version"] != "mxops-policy-v2026.11" {
		t.Fatalf("job_queue_view wrong: %v", jq)
	}
	if val, exists := jq["eta_seconds"]; !exists || val != nil {
		t.Fatalf("eta_seconds must be explicit JSON null (not 0): exists=%v val=%v", exists, val)
	}
	retained, _ := jq["retained_asset_versions"].([]any)
	if len(retained) != 1 || retained[0].(map[string]any)["asset_id"] != "asset_brand_1" || retained[0].(map[string]any)["version"] != "v4.2" {
		t.Fatalf("night waiting lost retained authorized asset version: %v", retained)
	}
	ws, _ := qResp["wait_stratification"].(map[string]any)
	if ws["cloud_artifact_waiting"] != true || ws["human_followup_pending"] != false || ws["crm_work_blocked"] != false || ws["waiting_kind"] != "cloud_artifact" {
		t.Fatalf("wait_stratification wrong: %v", ws)
	}

	// 3) GPU night queueing MUST NOT block normal CRM sales follow-up work.
	h.mustDo("POST", "/api/v1/follow-ups", sessionOwnerA, tenantA, `{
		"contact_id": "`+contactID+`",
		"lead_id": "`+leadID+`",
		"note": "GPU夜间排队期间，销售正常电话跟进并记录需求"
	}`, 201)

	// Re-open page (GET) recovers full queue state, retained asset versions, and dual-track wait kind ("both").
	recovered := h.mustDo("GET", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenantA, "", 200)
	rWS, _ := recovered["wait_stratification"].(map[string]any)
	if rWS["cloud_artifact_waiting"] != true || rWS["human_followup_pending"] != true || rWS["crm_work_blocked"] != false || rWS["waiting_kind"] != "both" {
		t.Fatalf("recovered dual-track wait_stratification wrong: %v", rWS)
	}

	// 4) Concurrent idempotent replay (8 goroutines) updates the same creative/job and NEVER creates a second project.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, out, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/queue-backflow", sessionOwnerA, tenantA, nightBackflowBody)
			if st != 200 {
				errs <- fmt.Errorf("concurrent replay status=%d out=%v", st, out)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var queueRows, handoffRows int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM campaign_motion_queue_states WHERE tenant_id=? AND campaign_id=?`, tenantA, leadID).Scan(&queueRows); err != nil {
		t.Fatal(err)
	}
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM campaign_motion_handoffs WHERE tenant_id=? AND campaign_id=?`, tenantA, leadID).Scan(&handoffRows); err != nil {
		t.Fatal(err)
	}
	if queueRows != 1 || handoffRows != 1 {
		t.Fatalf("replay duplicated rows: queueRows=%d handoffRows=%d", queueRows, handoffRows)
	}

	// Reusing same idempotency_key with conflicting payload is rejected (409).
	conflictBody := strings.Replace(nightBackflowBody, `"wait_reason": "night_window_scheduled"`, `"wait_reason": "tampered_reason"`, 1)
	status, out, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/queue-backflow", sessionOwnerA, tenantA, conflictBody)
	if status != 409 || stringOf(errCode(out)) != "idempotency_conflict" {
		t.Fatalf("idempotency conflict got %d %v, want 409 idempotency_conflict", status, out)
	}

	// Attempting to attach a second project_id is rejected (409 project_mismatch).
	secondProjectBody := `{
		"idempotency_key": "q-second-proj",
		"job_id": "job_night_002",
		"creative_id": "crv_night_002",
		"campaign_id": "` + leadID + `",
		"project_id": "prj_second_evil",
		"revision_id": "rev_night_1",
		"digest": "` + digest + `",
		"phase": "queued",
		"queue_state": "queued",
		"wait_reason": "busy",
		"eta_seconds": null,
		"policy_version": "mxops-policy-v2026.11"
	}`
	status, out, _ = h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/queue-backflow", sessionOwnerA, tenantA, secondProjectBody)
	if status != 409 || stringOf(errCode(out)) != "project_mismatch" {
		t.Fatalf("second project got %d %v, want 409 project_mismatch", status, out)
	}

	// 5) Auto outreach / auto publish / auto conversion backflow is forbidden (422).
	autoBody := `{
		"idempotency_key": "q-auto-bad",
		"job_id": "job_night_001",
		"creative_id": "crv_night_001",
		"campaign_id": "` + leadID + `",
		"project_id": "prj_night_1",
		"revision_id": "rev_night_1",
		"digest": "` + digest + `",
		"phase": "completed",
		"queue_state": "succeeded",
		"wait_reason": "none",
		"eta_seconds": 0,
		"policy_version": "mxops-policy-v2026.11",
		"auto_outreach": true
	}`
	status, out, _ = h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/queue-backflow", sessionOwnerA, tenantA, autoBody)
	if status != 422 || stringOf(errCode(out)) != "auto_side_effect_forbidden" {
		t.Fatalf("auto_outreach got %d %v, want 422 auto_side_effect_forbidden", status, out)
	}

	// 6) Clean queue completion updates the same creative/job and triggers ZERO outreach/publish/conversion.
	doneResp := h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/queue-backflow", sessionOwnerA, tenantA, `{
		"idempotency_key": "q-night-done",
		"job_id": "job_night_001",
		"creative_id": "crv_night_001",
		"campaign_id": "`+leadID+`",
		"project_id": "prj_night_1",
		"revision_id": "rev_night_1",
		"digest": "`+digest+`",
		"phase": "completed",
		"queue_state": "succeeded",
		"wait_reason": "none",
		"eta_seconds": 0,
		"policy_version": "mxops-policy-v2026.11"
	}`, 200)
	if doneResp["piece_sent"] != false || doneResp["auto_outreach_triggered"] != false || doneResp["auto_published"] != false || doneResp["auto_conversion_recorded"] != false {
		t.Fatalf("queue completion triggered forbidden side effect: %v", doneResp)
	}
}

func TestCampaignMotionCrossTenantAndRevocationFailClosed(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureCampaignMotion: true})
	tenantA, tenantB, _ := h.seed()
	leadID, _ := seedSwarmCampaignLead(t, h, tenantA, "revoke")
	digest := "sha256:" + strings.Repeat("ef", 32)

	handoffBody := `{
		"campaign_goal": "撤权测试",
		"channels": ["抖音"],
		"budget_attribution_id": "budget_rev",
		"motion": {"project_id":"prj_rev_1","revision_id":"rev_rev_1","campaign_id":"` + leadID + `","digest":"` + digest + `"}
	}`
	h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenantA, handoffBody, 200)

	// Cross-tenant (Tenant B) fails closed on every sub-route.
	for _, ep := range []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/v1/campaigns/" + leadID + "/motion-handoff", ""},
		{"POST", "/api/v1/campaigns/" + leadID + "/motion-handoff", handoffBody},
		{"POST", "/api/v1/campaigns/" + leadID + "/motion-handoff/queue-backflow", `{}`},
		{"POST", "/api/v1/campaigns/" + leadID + "/motion-handoff/render-backflow", `{}`},
		{"POST", "/api/v1/campaigns/" + leadID + "/motion-handoff/repeat-methods", `{}`},
		{"POST", "/api/v1/campaigns/" + leadID + "/motion-handoff/revoke", `{"reason":"cross"}`},
	} {
		st, _, _ := h.do(ep.method, ep.path, sessionOwnerB, tenantB, ep.body)
		if st != 404 && st != 403 {
			t.Fatalf("cross-tenant %s %s got %d, want 404/403", ep.method, ep.path, st)
		}
	}

	// Revoke authorization in Tenant A.
	rev := h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff/revoke", sessionOwnerA, tenantA, `{"reason":"活动结束撤权"}`, 200)
	if rev["revoked"] != true {
		t.Fatalf("expected revoked=true: %v", rev)
	}

	// After revocation, new handoff, queue backflow, render backflow, and repeat method all fail closed (403 revoked).
	for _, ep := range []struct {
		path string
		body string
	}{
		{"/api/v1/campaigns/" + leadID + "/motion-handoff", handoffBody},
		{"/api/v1/campaigns/" + leadID + "/motion-handoff/queue-backflow", `{}`},
		{"/api/v1/campaigns/" + leadID + "/motion-handoff/render-backflow", `{}`},
		{"/api/v1/campaigns/" + leadID + "/motion-handoff/repeat-methods", `{}`},
	} {
		st, out, _ := h.do("POST", ep.path, sessionOwnerA, tenantA, ep.body)
		if st != 403 || stringOf(errCode(out)) != "revoked" {
			t.Fatalf("post-revocation %s got %d %v, want 403 revoked", ep.path, st, out)
		}
	}

	// GET remains readable and honestly surfaces revoked=true.
	got := h.mustDo("GET", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenantA, "", 200)
	if got["revoked"] != true {
		t.Fatalf("GET after revocation must report revoked=true: %v", got)
	}
}
