package httpapi

import (
	"strings"
	"testing"
)

func TestCampaignMotionOffIsInvisible(t *testing.T) {
	h := newHarness(t)
	tenant, _, _ := h.seed()
	status, _, _ := h.do("GET", "/api/v1/campaign-motion/capability", sessionOwnerA, tenant, "")
	if status != 404 {
		t.Fatalf("capability = %d", status)
	}
	status, _, _ = h.do("POST", "/api/v1/campaigns/lead_missing/motion-handoff", sessionOwnerA, tenant, `{}`)
	if status != 404 {
		t.Fatalf("post = %d", status)
	}
}

func TestCampaignMotionExportStripsPrivacyAndDoesNotProduce(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureCampaignMotion: true})
	tenant, _, _ := h.seed()
	intake := h.intake(sessionOwnerA, tenant, `{
		"source_app":"touch","source_ns":"landing","event_id":"evt-motion-1",
		"contact":{"name":"不应进动效的联系人","phone":"13900001111","email":"secret@shop.cn"},
		"business_category":"merchant_customer","source_type":"touch_campaign",
		"source":{"source_app":"touch","source_ref":"camp-motion","auth_scope_snapshot":"profile-secret-motion"},
		"consent":{"source_submission_ref":"sub-motion","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":false}
	}`, 201)
	leadID, _ := intake["lead_id"].(string)
	if leadID == "" {
		t.Fatalf("intake: %v", intake)
	}
	digest := "sha256:" + strings.Repeat("ab", 32)
	body := `{
		"campaign_goal":"春季上新目标 13900002222",
		"target_audience":"门店店主 secret.customer@example.com",
		"approved_facts":[
			{"key":"product","value":"冷萃。聊天原文-不应出现-XYZ","confirmed":true},
			{"key":"recipe","value":"未确认配方-不应出现","confirmed":false}
		],
		"assets":[
			{"id":"asset_ok_1","authorized":true},
			{"id":"asset-secret-unauth","authorized":false}
		],
		"cta":"到店试喝",
		"channels":["抖音","视频号","小红书"],
		"budget_attribution_id":"budget_spring_1",
		"motion":{"project_id":"prj_motion_1","revision_id":"rev_motion_1","campaign_id":"` + leadID + `","digest":"` + digest + `"},
		"chat":"聊天原文-不应出现-XYZ",
		"phone":"13900002222",
		"email":"secret.customer@example.com",
		"unauthorized_leads":[{"id":"lead-unauth-9f3a","name":"未授权客户甲","phone":"13900003333"}],
		"customer_privacy":{"id_card":"PRIVACY-4401","address":"秘密住址-不应出现"},
		"conversion":{"leads":4,"conversions":1,"revenue_cents":987654321},
		"variants":[{"channel":"抖音","prompt":"PROMPT-SECRET-不应出现"}]
	}`
	saved := h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenant, body, 200)
	export, _ := saved["export"].(map[string]any)
	raw := jsonText(export)
	for _, secret := range []string{
		"13900002222", "13900001111", "secret.customer@example.com", "secret@shop.cn",
		"聊天原文-不应出现-XYZ", "未确认配方-不应出现", "asset-secret-unauth",
		"lead-unauth-9f3a", "未授权客户甲", "PRIVACY-4401", "秘密住址-不应出现",
		"PROMPT-SECRET-不应出现", "987654321", "不应进动效的联系人", "profile-secret-motion",
	} {
		if strings.Contains(raw, secret) {
			t.Fatalf("export contains %s: %s", secret, raw)
		}
	}
	if saved["model_calls"].(float64) != 0 || saved["creative_project_writes"].(float64) != 0 || saved["piece_generated"] == true || saved["piece_sent"] == true {
		t.Fatalf("response claims production: %v", saved)
	}
	channels, _ := export["channels"].([]any)
	variants, _ := export["variants"].([]any)
	if len(channels) != 3 || len(variants) != 3 {
		t.Fatalf("channels=%v variants=%v", channels, variants)
	}
	local, _ := saved["local_conversion"].(map[string]any)
	if local["revenue_cents"].(float64) != 987654321 {
		t.Fatalf("local conversion lost: %v", saved["local_conversion"])
	}
	notice, _ := saved["notice"].(string)
	for _, phrase := range []string{"已生成", "已送出", "已经生成", "已经送出"} {
		if strings.Contains(notice, phrase) {
			t.Fatalf("notice claims a finished piece: %s", notice)
		}
	}

	var exportJSON, localJSON string
	var modelCalls, writes, generated, sent, rows int
	if err := h.api.St.DB.QueryRow(`
		SELECT export_json, local_conversion_json, model_calls, creative_project_writes, piece_generated, piece_sent
		FROM campaign_motion_handoffs WHERE tenant_id=? AND campaign_id=?`, tenant, leadID).
		Scan(&exportJSON, &localJSON, &modelCalls, &writes, &generated, &sent); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(exportJSON, "987654321") || strings.Contains(exportJSON, "13900002222") || strings.Contains(exportJSON, "secret.customer@example.com") {
		t.Fatalf("stored export leaked: %s", exportJSON)
	}
	if !strings.Contains(localJSON, "987654321") || modelCalls != 0 || writes != 0 || generated != 0 || sent != 0 {
		t.Fatalf("stored local=%s model=%d writes=%d generated=%d sent=%d", localJSON, modelCalls, writes, generated, sent)
	}

	again := h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenant, body, 200)
	if again["model_calls"].(float64) != 0 || again["creative_project_writes"].(float64) != 0 {
		t.Fatalf("second post produced work: %v", again)
	}
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM campaign_motion_handoffs WHERE tenant_id=? AND campaign_id=?`, tenant, leadID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows=%d", rows)
	}

	status, bad, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenant, `{
		"campaign_goal":"digest 不对",
		"channels":["抖音"],
		"budget_attribution_id":"budget_spring_1",
		"motion":{"project_id":"prj_motion_1","revision_id":"rev_motion_1","campaign_id":"`+leadID+`","digest":"sha256:abc"}
	}`)
	if status != 400 || bad["error"] != "digest" {
		t.Fatalf("bad digest = %d %v", status, bad)
	}
	status, shape, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenant, `{
		"campaign_goal":"引用多了字段",
		"channels":["抖音"],
		"budget_attribution_id":"budget_spring_1",
		"motion":{"project_id":"prj_motion_1","revision_id":"rev_motion_1","campaign_id":"`+leadID+`","digest":"`+digest+`","timeline":{"ast":"nope"}}
	}`)
	if status != 400 || shape["error"] != "motion" {
		t.Fatalf("extra motion = %d %v", status, shape)
	}

	formLead := h.intake(sessionOwnerA, tenant, `{
		"source_app":"form","source_ns":"site","event_id":"evt-motion-form",
		"contact":{"name":"表单客","phone":"13900004444"},
		"business_category":"merchant_customer","source_type":"form"
	}`, 201)
	formID, _ := formLead["lead_id"].(string)
	status, _, _ = h.do("POST", "/api/v1/campaigns/"+formID+"/motion-handoff", sessionOwnerA, tenant, body)
	if status != 404 {
		t.Fatalf("form lead = %d", status)
	}
	status, _, _ = h.do("GET", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerB, tenant, "")
	if status != 403 {
		t.Fatalf("cross tenant = %d", status)
	}
}

func TestCampaignMotionRefStaysOnThePathCampaign(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureCampaignMotion: true})
	tenant, _, _ := h.seed()
	intake := h.intake(sessionOwnerA, tenant, `{
		"source_app":"touch","source_ns":"landing","event_id":"evt-motion-ref",
		"contact":{"name":"活动甲","phone":"13900005555"},
		"business_category":"merchant_customer","source_type":"touch_campaign",
		"source":{"source_app":"touch","source_ref":"camp-motion-ref"},
		"consent":{"source_submission_ref":"sub-motion-ref","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":false}
	}`, 201)
	leadID, _ := intake["lead_id"].(string)
	if leadID == "" {
		t.Fatalf("intake: %v", intake)
	}
	digest := "sha256:" + strings.Repeat("cd", 32)
	other := "led_other_campaign"
	status, mismatch, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenant, `{
		"campaign_goal":"指向另一次活动",
		"channels":["抖音"],
		"budget_attribution_id":"budget_spring_1",
		"motion":{"project_id":"prj_motion_1","revision_id":"rev_motion_1","campaign_id":"`+other+`","digest":"`+digest+`"}
	}`)
	if status != 400 || mismatch["error"] != "motion" {
		t.Fatalf("mismatch = %d %v", status, mismatch)
	}
	var rows int
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM campaign_motion_handoffs WHERE tenant_id=? AND (campaign_id=? OR export_json LIKE ?)`, tenant, leadID, "%"+other+"%").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("wrong ref stored, rows=%d", rows)
	}

	for _, body := range []string{
		`{"campaign_goal":"空工程","channels":["抖音"],"budget_attribution_id":"budget_spring_1","motion":{"project_id":"","revision_id":"rev_motion_1","campaign_id":"` + leadID + `","digest":"` + digest + `"}}`,
		`{"campaign_goal":"空版本","channels":["抖音"],"budget_attribution_id":"budget_spring_1","motion":{"project_id":"prj_motion_1","revision_id":"","digest":"` + digest + `"}}`,
	} {
		status, emptyID, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenant, body)
		if status != 400 || emptyID["error"] != "motion" {
			t.Fatalf("empty id = %d %v body=%s", status, emptyID, body)
		}
	}
	if err := h.api.St.DB.QueryRow(`SELECT COUNT(*) FROM campaign_motion_handoffs WHERE tenant_id=? AND campaign_id=?`, tenant, leadID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("rejected ref was stored, rows=%d", rows)
	}
}

func seedMotionLead(t *testing.T, h *harness, tenant string) string {
	t.Helper()
	intake := h.intake(sessionOwnerA, tenant, `{
		"source_app":"touch","source_ns":"landing","event_id":"evt-variant-1",
		"contact":{"name":"变体联系人","phone":"13900004444"},
		"business_category":"merchant_customer","source_type":"touch_campaign",
		"source":{"source_app":"touch","source_ref":"camp-variant","auth_scope_snapshot":"profile-variant"},
		"consent":{"source_submission_ref":"sub-variant","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":false}
	}`, 201)
	id, _ := intake["lead_id"].(string)
	if id == "" {
		t.Fatalf("intake: %v", intake)
	}
	return id
}

func motionBindingBody(leadID, binding string) string {
	return `{
		"campaign_goal":"变体走查目标",
		"channels":["抖音信息流","快手落地页"],
		"authorized_asset_ids":["asset_ok_1"],
		"budget_attribution_id":"budget_variant_1",
		"motion":{"project_id":"prj_motion_1","revision_id":"rev_motion_1","campaign_id":"` + leadID + `","digest":"sha256:` + strings.Repeat("cd", 32) + `"}
		` + binding + `
	}`
}

// A binding turns each channel into a motionref-shaped variant claim inside
// the export; counts stay zero and no link is issued over HTTP.
func TestCampaignMotionVariantBindingShapesPerChannel(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureCampaignMotion: true})
	tenant, _, _ := h.seed()
	leadID := seedMotionLead(t, h, tenant)
	binding := `,"variant_binding":{"owner_account_id":"acct_motion_1","expires_at":"2027-01-01T00:00:00Z"}`
	saved := h.mustDo("POST", "/api/v1/campaigns/"+leadID+"/motion-handoff", sessionOwnerA, tenant, motionBindingBody(leadID, binding), 200)
	export, _ := saved["export"].(map[string]any)
	variants, _ := export["variants"].([]any)
	if len(variants) != 2 {
		t.Fatalf("variants = %v", variants)
	}
	for _, v := range variants {
		row, _ := v.(map[string]any)
		id, _ := row["variant_id"].(string)
		digest, _ := row["digest"].(string)
		ref, _ := row["origin_context_ref"].(string)
		if !strings.HasPrefix(id, "var_") || !strings.HasPrefix(digest, "sha256:") ||
			!strings.HasPrefix(ref, "leads/campaigns/"+leadID+"/motion-handoffs#") {
			t.Fatalf("variant facts incomplete: %v", row)
		}
	}
	if saved["model_calls"].(float64) != 0 || saved["creative_project_writes"].(float64) != 0 {
		t.Fatalf("binding produced calls: %v", saved)
	}
	raw := jsonText(export)
	if strings.Contains(raw, "https://") || strings.Contains(raw, "motionRef=") {
		t.Fatalf("export carries a signed link: %s", raw)
	}
}

// The binding refusals surface with their own codes and write nothing.
func TestCampaignMotionVariantBindingRejections(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureCampaignMotion: true})
	tenantA, tenantB, _ := h.seed()
	leadID := seedMotionLead(t, h, tenantA)
	path := "/api/v1/campaigns/" + leadID + "/motion-handoff"

	status, out, _ := h.do("POST", path, sessionOwnerA, tenantA, motionBindingBody(leadID, `,"variant_binding":{"owner_tenant_id":"tnt_other","owner_account_id":"acct_motion_1"}`))
	if status != 403 || stringOf(errCode(out)) != "foreign_tenant" {
		t.Fatalf("foreign = %d %v, want 403 foreign_tenant", status, out)
	}
	status, out, _ = h.do("POST", path, sessionOwnerA, tenantA, motionBindingBody(leadID, `,"variant_binding":{"owner_account_id":"acct_motion_1","expires_at":"2020-01-01T00:00:00Z"}`))
	if status != 422 || stringOf(errCode(out)) != "variant_expired" {
		t.Fatalf("expired = %d %v, want 422 variant_expired", status, out)
	}
	status, out, _ = h.do("POST", path, sessionOwnerA, tenantA, motionBindingBody(leadID, `,"variant_binding":{"expires_at":"2027-01-01T00:00:00Z"}`))
	if status != 422 || stringOf(errCode(out)) != "motion_receiver" {
		t.Fatalf("no receiver = %d %v, want 422 motion_receiver", status, out)
	}

	got := h.mustDo("GET", path, sessionOwnerA, tenantA, "", 200)
	if _, has := got["export"]; has && got["export"] != nil {
		t.Fatalf("a rejected binding still saved an export: %v", got)
	}
	// Tenant B still cannot touch the campaign.
	status, _, _ = h.do("POST", path, sessionOwnerB, tenantB, motionBindingBody(leadID, `,"variant_binding":{"owner_account_id":"acct_motion_1"}`))
	if status != 404 {
		t.Fatalf("tenant b = %d, want 404", status)
	}
}
