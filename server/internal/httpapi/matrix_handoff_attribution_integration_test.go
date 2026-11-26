package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHUI2391_HTTP_IdempotentDraftRecoveryAndNullMetricsJSON(t *testing.T) {
	ag10 := newFakeAG10(t)
	h := newHarnessOpts(t, harnessOpts{featureMatrixHandoff: true, matrixDraftURL: ag10.srv.URL})
	tenant, _, _ := h.seed()
	leadID := seedTouchLead(t, h, tenant)
	path := "/api/v1/campaigns/" + leadID + "/matrix-drafts"

	// Concurrent duplicate clicks / timeout retries recover the same draft under -race.
	const workers = 6
	var wg sync.WaitGroup
	statuses := make([]int, workers)
	draftIDs := make([]string, workers)
	refIDs := make([]string, workers)
	rawBodies := make([]string, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			st, raw, _ := h.doRaw("POST", path, sessionOwnerA, tenant, matrixBody(""))
			statuses[idx] = st
			rawBodies[idx] = string(raw)
			var out map[string]any
			_ = json.Unmarshal(raw, &out)
			draftIDs[idx] = stringOf(out["draft_id"])
			refIDs[idx] = stringOf(out["ref_id"])
		}(i)
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		if statuses[i] != http.StatusCreated && statuses[i] != http.StatusOK {
			t.Fatalf("worker %d status=%d body=%s", i, statuses[i], rawBodies[i])
		}
		if draftIDs[i] != "d_fake_1" || refIDs[i] == "" || refIDs[i] != refIDs[0] {
			t.Fatalf("worker %d recovered different draft: draft=%q ref=%q want ref=%q", i, draftIDs[i], refIDs[i], refIDs[0])
		}
		// Unknown metrics & ROI MUST serialize as JSON null, NEVER 0.
		for _, wantSub := range []string{
			`"publish_stage":"draft_saved"`,
			`"official_publish_authorized":false`,
			`"ugc_reward_triggered":false`,
			`"conversion_roi":null`,
			`"matrix_reach":null`,
			`"matrix_likes":null`,
			`"matrix_leads":null`,
		} {
			if !strings.Contains(rawBodies[i], wantSub) {
				t.Fatalf("worker %d missing %s in %s", i, wantSub, rawBodies[i])
			}
		}
	}

	// Only one persisted ref row exists.
	list := h.mustDo("GET", path, sessionOwnerA, tenant, "", http.StatusOK)
	drafts, _ := list["drafts"].([]any)
	if len(drafts) != 1 {
		t.Fatalf("expected 1 draft row after concurrent retries, got %d: %v", len(drafts), list)
	}
}

func TestHUI2391_HTTP_FailClosedGuards_WrongType_Expired_SmuggledPII_CrossCampaign(t *testing.T) {
	ag10 := newFakeAG10(t)
	h := newHarnessOpts(t, harnessOpts{featureMatrixHandoff: true, matrixDraftURL: ag10.srv.URL})
	tenantA, tenantB, _ := h.seed()
	camp1 := seedTouchLead(t, h, tenantA)
	camp2 := h.intake(sessionOwnerA, tenantA, `{
		"source_app":"touch","source_ns":"landing","event_id":"evt-matrix-2",
		"contact":{"name":"联系人乙","phone":"13900002222","email":"b@shop.cn"},
		"business_category":"merchant_customer","source_type":"touch_campaign",
		"source":{"source_app":"touch","source_ref":"camp-matrix-2","auth_scope_snapshot":"scope-matrix-2"},
		"consent":{"source_submission_ref":"sub-matrix-2","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":false}
	}`, 201)["lead_id"].(string)

	path1 := "/api/v1/campaigns/" + camp1 + "/matrix-drafts"
	path2 := "/api/v1/campaigns/" + camp2 + "/matrix-drafts"

	// 1. image / video / needs_video fail closed locally before AG10 (zero silent transcode/charge).
	for _, ct := range []string{"image", "video", "needs_video", "audio"} {
		body := `{"asset_id":"asset_1","asset_hash":"` + matrixHash + `","revision":1,"content_type":"` + ct + `","grant_ref":"g1","license_ref":"l1","brand_id":"b1","payer_id":"p1"}`
		st, out, _ := h.do("POST", path1, sessionOwnerA, tenantA, body)
		if st != http.StatusUnprocessableEntity || stringOf(errCode(out)) != "wrong_type" {
			t.Fatalf("content_type=%q got %d %v, want 422 wrong_type", ct, st, out)
		}
	}

	// 2. Expired license fails closed locally with 403 license_expired before AG10.
	expired := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	expBody := `{"asset_id":"asset_1","asset_hash":"` + matrixHash + `","revision":1,"content_type":"copy","grant_ref":"g1","license_ref":"l1","license_expires_at":"` + expired + `","brand_id":"b1","payer_id":"p1"}`
	st, out, _ := h.do("POST", path1, sessionOwnerA, tenantA, expBody)
	if st != http.StatusForbidden || stringOf(errCode(out)) != "license_expired" {
		t.Fatalf("expired license got %d %v, want 403 license_expired", st, out)
	}

	// 3. Private fields & smuggled phone/CRM notes fail closed before AG10.
	for _, badBody := range []string{
		`{"asset_id":"asset_1","asset_hash":"` + matrixHash + `","revision":1,"content_type":"copy","grant_ref":"g1","license_ref":"l1","brand_id":"b1","payer_id":"p1","crm_notes":"内部备注"}`,
		`{"asset_id":"asset_1","asset_hash":"` + matrixHash + `","revision":1,"content_type":"copy","grant_ref":"g1","license_ref":"l1","brand_id":"b1","payer_id":"p1","customer_list":["c1"]}`,
		`{"asset_id":"asset_1","asset_hash":"` + matrixHash + `","revision":1,"content_type":"copy","grant_ref":"g1","license_ref":"l1","brand_id":"b1","payer_id":"p1","asset_source_ref":"touch/13800138000"}`,
		`{"asset_id":"asset_1","asset_hash":"` + matrixHash + `","revision":1,"content_type":"copy","grant_ref":"g1","license_ref":"l1","brand_id":"b1","payer_id":"p1","asset_source_ref":"crm_note/secret"}`,
	} {
		st, _, _ := h.do("POST", path1, sessionOwnerA, tenantA, badBody)
		if st != http.StatusBadRequest {
			t.Fatalf("private/smuggled body got %d, want 400: %s", st, badBody)
		}
	}

	if ag10.callCount() != 0 {
		t.Fatalf("fail-closed requests leaked to AG10: calls=%d", ag10.callCount())
	}

	// 4. Create legitimate draft on camp1; cross-campaign and cross-tenant receipt/source-events MUST 404.
	h.mustDo("POST", path1, sessionOwnerA, tenantA, matrixBody(""), http.StatusCreated)
	ref1 := matrixRefID(t, h, tenantA, camp1)

	if st, _, _ := h.do("GET", path2+"/"+ref1+"/receipt", sessionOwnerA, tenantA, ""); st != http.StatusNotFound {
		t.Fatalf("cross-campaign receipt got %d, want 404", st)
	}
	if st, _, _ := h.do("POST", path2+"/"+ref1+"/source-events", sessionOwnerA, tenantA, sourceEventBody("result", "evt_xcamp")); st != http.StatusNotFound {
		t.Fatalf("cross-campaign source-event got %d, want 404", st)
	}
	if st, _, _ := h.do("GET", path1+"/"+ref1+"/receipt", sessionOwnerB, tenantB, ""); st != http.StatusNotFound {
		t.Fatalf("cross-tenant receipt got %d, want 404", st)
	}
}

func TestHUI2391_HTTP_StaleReceiptBlockedAndFabricatedPublishedDemoted(t *testing.T) {
	// Dishonest upstream fixture claiming status=published, published=true, charged=true.
	dishonestAG10 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/v1/matrix-drafts" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			rev := int(body["revision"].(float64))
			did := "d_rev3"
			if rev == 4 {
				did = "d_rev4"
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"draft_id":          did,
				"schema_version":    "matrix-draft/v1",
				"source_app":        "leads",
				"campaign_id":       body["campaign_id"],
				"revision":          rev,
				"asset_id":          body["asset_id"],
				"asset_hash":        body["asset_hash"],
				"content_type":      "copy",
				"tenant_id":         r.Header.Get("X-Matrix-Tenant"),
				"status":            "draft",
				"published":         false,
				"charged":           false,
				"idempotent_replay": false,
			})
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/receipt") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/matrix-drafts/"), "/receipt")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"draft_id":       id,
				"schema_version": "matrix-draft-receipt/v1",
				"source_app":     "leads",
				"revision":       3,
				"asset_id":       "asset_copy_1",
				"asset_hash":     matrixHash,
				"content_type":   "copy",
				"tenant_id":      r.Header.Get("X-Matrix-Tenant"),
				"status":         "published",
				"published":      true,
				"charged":        true,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(dishonestAG10.Close)

	h := newHarnessOpts(t, harnessOpts{featureMatrixHandoff: true, matrixDraftURL: dishonestAG10.URL})
	tenant, _, _ := h.seed()
	leadID := seedTouchLead(t, h, tenant)
	path := "/api/v1/campaigns/" + leadID + "/matrix-drafts"

	// Create revision 3.
	createdV3 := h.mustDo("POST", path, sessionOwnerA, tenant, matrixBody(""), http.StatusCreated)
	ref3 := createdV3["ref_id"].(string)

	// Receipt MUST NOT adopt dishonest published=true / charged=true without official auth.
	rcptStatus, rcptRaw, _ := h.doRaw("GET", path+"/"+ref3+"/receipt", sessionOwnerA, tenant, "")
	if rcptStatus != http.StatusOK {
		t.Fatalf("receipt status=%d body=%s", rcptStatus, rcptRaw)
	}
	var rcpt map[string]any
	_ = json.Unmarshal(rcptRaw, &rcpt)
	if rcpt["published"] != false || rcpt["charged"] != false || rcpt["publish_stage"] != "draft_saved" || rcpt["official_publish_authorized"] != false || rcpt["ugc_reward_triggered"] != false || rcpt["conversion_roi"] != nil {
		t.Fatalf("dishonest publish claim was not demoted: %s", rcptRaw)
	}
	if stringOf(rcpt["fetched_at"]) == "" {
		t.Fatalf("fetched_at missing: %s", rcptRaw)
	}

	// Land source_version=2 on ref3, then reject stale source_version=1.
	h.mustDo("POST", path+"/"+ref3+"/source-events", sessionOwnerA, tenant,
		`{"provider_event_id":"evt_sv2","kind":"result","source_version":2,"matrix_plan_id":"plan_v2","reach":88}`, http.StatusCreated)

	st, out, _ := h.do("POST", path+"/"+ref3+"/source-events", sessionOwnerA, tenant,
		`{"provider_event_id":"evt_sv1_late","kind":"result","source_version":1,"matrix_plan_id":"plan_v1","reach":1}`)
	if st != http.StatusConflict || stringOf(errCode(out)) != "stale_receipt" {
		t.Fatalf("stale source_version got %d %v, want 409 stale_receipt", st, out)
	}

	// Adopt newer revision 4 on the same campaign.
	v4Body := `{"asset_id":"asset_copy_1","asset_hash":"` + matrixHash + `","revision":4,"content_type":"copy","grant_ref":"grant_1","license_ref":"license_1","brand_id":"brand_1","payer_id":"payer_1"}`
	createdV4 := h.mustDo("POST", path, sessionOwnerA, tenant, v4Body, http.StatusCreated)
	ref4 := createdV4["ref_id"].(string)

	// Late result targeting superseded ref3 MUST fail with 409 stale_receipt.
	st, out, _ = h.do("POST", path+"/"+ref3+"/source-events", sessionOwnerA, tenant,
		`{"provider_event_id":"evt_v3_after_v4","kind":"result","source_version":3,"matrix_plan_id":"plan_v3_late","reach":999}`)
	if st != http.StatusConflict || stringOf(errCode(out)) != "stale_receipt" {
		t.Fatalf("superseded rev3 event got %d %v, want 409 stale_receipt", st, out)
	}

	// Negative metrics fail closed.
	st, _, _ = h.do("POST", path+"/"+ref4+"/source-events", sessionOwnerA, tenant,
		`{"provider_event_id":"evt_neg","kind":"result","reach":-5}`)
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("negative metric got %d, want 422", st)
	}
}

func TestHUI2391_HTTP_ChannelAttributionDedup_ReceptionGuardTakeover_ZeroSecondBot(t *testing.T) {
	const secretText = "客户私信机密内容_13988887777_不得进日志"
	h := newHarnessOpts(t, harnessOpts{featureReception: true, captureLog: true})
	tenantA, _, _ := h.seed()
	h.provision("grant", tenantA, principalAgentA)

	// 1. Grant comment.read + comment.reply only (NOT message.read, NOT message.reply).
	h.mustDo("POST", "/api/v1/channel-grants", sessionOwnerA, tenantA, `{
		"provider":"connector","account_id":"acct_m1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"capabilities":["comment.read","comment.reply"]
	}`, http.StatusCreated)

	// DM is rejected when only comment.read/comment.reply exist.
	dmSt, dmOut, _ := h.do("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, `{
		"target_tenant_id":"`+tenantA+`","provider":"connector","account_id":"acct_m1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"event_id":"evt_dm_denied","kind":"direct_message","subject_id":"sub_1","nickname":"访客",
		"text":"`+secretText+`","explicit_intent":true,"purpose":"sales_inquiry"
	}`)
	if dmSt != http.StatusForbidden || dmOut["error"] != "capability_missing" {
		t.Fatalf("DM with comment-only grant got %d %v, want 403 capability_missing", dmSt, dmOut)
	}

	// 2. Open a real HUI-1688 reception session (AI mode).
	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenantA, `{"default_mode":"ai","language":"zh"}`, http.StatusCreated)
	opened := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "", `{"visitor_key":"vk-matrix-attr-1"}`, http.StatusCreated)
	sid := opened["session"].(map[string]any)["id"].(string)

	// Attempting to reply as a second bot (matrix_bot) is ALWAYS blocked.
	secondBot := h.mustDo("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, `{
		"target_tenant_id":"`+tenantA+`","provider":"connector","account_id":"acct_m1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"event_id":"evt_bot2","kind":"comment","subject_id":"sub_1","nickname":"访客",
		"text":"`+secretText+`","want_reply":true,"reply_speaker":"matrix_bot","reception_session_id":"`+sid+`"
	}`, http.StatusCreated)
	if secondBot["reply_allowed"] != false || secondBot["reply_refusal"] != "second_reply_bot_forbidden" || secondBot["second_reply_bot_started"] != false || secondBot["reply_authority"] != "receptionguard" {
		t.Fatalf("second reply bot must be refused: %v", secondBot)
	}

	// Before takeover, receptionguard authorizes single Leads reply draft (still delivered=false).
	beforeTO := h.mustDo("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, `{
		"target_tenant_id":"`+tenantA+`","provider":"connector","account_id":"acct_m1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"event_id":"evt_pre_to","kind":"comment","subject_id":"sub_1","nickname":"访客",
		"text":"请问价格","want_reply":true,"reception_session_id":"`+sid+`"
	}`, http.StatusCreated)
	if beforeTO["reply_allowed"] != true || beforeTO["delivered"] != false || beforeTO["reply_authority"] != "receptionguard" || beforeTO["second_reply_bot_started"] != false {
		t.Fatalf("before takeover reply = %v", beforeTO)
	}

	// 3. Human takes over reception session -> receptionguard is the sole arbiter and blocks AI reply.
	h.mustDo("POST", "/api/v1/reception/sessions/"+sid+"/takeover", sessionAgentA, tenantA, `{"epoch":1}`, http.StatusOK)

	afterTO := h.mustDo("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, `{
		"target_tenant_id":"`+tenantA+`","provider":"connector","account_id":"acct_m1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"event_id":"evt_post_to","kind":"comment","subject_id":"sub_1","nickname":"访客",
		"text":"还有优惠吗","want_reply":true,"reception_session_id":"`+sid+`"
	}`, http.StatusCreated)
	if afterTO["reply_allowed"] != false || afterTO["reply_refusal"] != "human_takeover_active" || afterTO["reply_authority"] != "receptionguard" || afterTO["second_reply_bot_started"] != false {
		t.Fatalf("after human takeover receptionguard must block reply: %v", afterTO)
	}

	// 4. Retract + late legacy-route replay MUST NOT duplicate candidate or restore marketing consent.
	salesBody := `{
		"target_tenant_id":"` + tenantA + `","provider":"connector","account_id":"acct_m1","app_id":"app_auth",
		"subject_ns":"connector.subject","message_ns":"connector.message","post_ns":"connector.post",
		"event_id":"evt_sales_retract","kind":"comment","subject_id":"sub_2","nickname":"买家",
		"phone":"13800009999","phone_marketing_consent":true,"sms_marketing_consent":true,
		"text":"` + secretText + `","explicit_intent":true,"purpose":"sales_inquiry"
	}`
	first := h.mustDo("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, salesBody, http.StatusCreated)
	if first["candidate_id"] == "" {
		t.Fatalf("expected candidate created: %v", first)
	}
	retractBody := strings.Replace(salesBody, `"purpose":"sales_inquiry"`, `"purpose":"sales_inquiry","retracted":true`, 1)
	retracted := h.mustDo("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, retractBody, http.StatusCreated)
	if retracted["candidate_status"] != "withdrawn" || retracted["phone_marketing"] != false || retracted["sms_marketing"] != false {
		t.Fatalf("retract failed to withdraw/clear consent: %v", retracted)
	}
	lateLegacy := strings.Replace(salesBody, `"purpose":"sales_inquiry"`, `"purpose":"sales_inquiry","legacy_route":true`, 1)
	late := h.mustDo("POST", "/api/v1/channel-interactions", sessionOwnerA, tenantA, lateLegacy, http.StatusOK)
	if late["idempotent"] != true || late["candidate_status"] != "withdrawn" || late["phone_marketing"] != false || late["sms_marketing"] != false {
		t.Fatalf("late legacy replay resurrected withdrawn candidate or consent: %v", late)
	}

	// 5. Server logs MUST NEVER contain sensitive message body or customer phone.
	if strings.Contains(h.logs.String(), secretText) || strings.Contains(h.logs.String(), "13988887777") {
		t.Fatalf("logs leaked sensitive message: %s", h.logs.String())
	}
}

func TestHUI2391_HTTP_MatrixOutageNeverBlocksLeadsCoreJourney(t *testing.T) {
	// Point MatrixDraft to an unreachable loopback port to simulate Matrix outage.
	h := newHarnessOpts(t, harnessOpts{
		featureMatrixHandoff: true,
		featureReception:     true,
		featureFollowups:     true,
		matrixDraftURL:       "http://127.0.0.1:1",
	})
	tenant, _, _ := h.seed()

	// 1. Leads independent intake still works.
	intake := h.intake(sessionOwnerA, tenant, `{
		"source_app":"touch","source_ns":"landing","event_id":"evt-matrix-outage",
		"contact":{"name":"联系人甲","phone":"13900001111","email":"a@shop.cn"},
		"business_category":"merchant_customer","source_type":"touch_campaign",
		"source":{"source_app":"touch","source_ref":"camp-matrix-outage","auth_scope_snapshot":"scope-matrix"},
		"consent":{"source_submission_ref":"sub-matrix-outage","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":false}
	}`, 201)
	leadID := intake["lead_id"].(string)
	contactID := intake["contact_id"].(string)

	// 2. Matrix draft handoff fails honestly with 503 matrix_unavailable.
	st, out, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/matrix-drafts", sessionOwnerA, tenant, matrixBody(""))
	if st != http.StatusServiceUnavailable || stringOf(errCode(out)) != "matrix_unavailable" {
		t.Fatalf("matrix outage got %d %v, want 503 matrix_unavailable", st, out)
	}

	// 3. Leads core journey (list leads, add follow-up, open reception widget/session) stays 100% operational.
	leads := h.mustDo("GET", "/api/v1/leads", sessionOwnerA, tenant, "", http.StatusOK)
	if !jsonListHasID(leads["items"], leadID) {
		t.Fatalf("lead missing during matrix outage: %v", leads)
	}
	h.mustDo("POST", "/api/v1/follow-ups", sessionOwnerA, tenant,
		`{"contact_id":"`+contactID+`","lead_id":"`+leadID+`","note":"矩阵故障期间继续人工跟进"}`, http.StatusCreated)
	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenant,
		`{"default_mode":"ai","language":"zh"}`, http.StatusCreated)
	h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "",
		`{"visitor_key":"vk-during-matrix-outage"}`, http.StatusCreated)
}
