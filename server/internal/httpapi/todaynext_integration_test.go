package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTodayNextHTTPQueueOrderingAndFrictionBaseline(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureReception: true, featureFollowups: true})
	tenantA, _, _ := h.seed()

	// 1. missing_info_or_permission_error (denied consent)
	deniedContact := h.mustDo("POST", "/api/v1/contacts", sessionOwnerA, tenantA,
		`{"name":"无许可客户","phone":"13700010001","business_category":"merchant_customer","source_type":"manual","consent_status":"denied"}`, http.StatusCreated)
	deniedLead := h.mustDo("POST", "/api/v1/leads", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"status":"new"}`, deniedContact["id"]), http.StatusCreated)
	deniedLeadID := deniedLead["id"].(string)

	// 2. waiting_customer
	waitLeadID := h.todayTouchLead(tenantA, "等待客户", "13700010002")
	h.mustDo("POST", "/api/v1/leads/"+waitLeadID+"/follow-through", sessionSalesA1, tenantA,
		`{"note":"已报价等反馈","complete":true,"disposition":"waiting_customer"}`, http.StatusCreated)

	// 3. ai_draft_pending (standalone session reply draft without human_takeover)
	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenantA, `{"default_mode":"assist"}`, http.StatusCreated)
	draftSess := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "",
		`{"visitor_key":"visitor-key-q-draft1"}`, http.StatusCreated)
	draftSID := draftSess["session"].(map[string]any)["id"].(string)
	savedDraft := h.mustDo("POST", "/api/v1/workbench/sessions/"+draftSID+"/reply-draft", sessionOwnerA, tenantA,
		`{"client_reply_id":"q-draft-1","body":"待确认的回复草稿"}`, http.StatusCreated)
	draftID := savedDraft["id"].(string)

	// 4. due_today
	dueLeadID := h.todayTouchLead(tenantA, "今日到期客户", "13700010003")
	todayAt := shanghaiWall(16, 0)
	h.mustDo("POST", "/api/v1/leads/"+dueLeadID+"/follow-through", sessionSalesA1, tenantA,
		fmt.Sprintf(`{"note":"约今天下午联系","complete":true,"next_follow_up_at":%q}`, todayAt), http.StatusCreated)

	// 5. overdue
	overdueLeadID := h.todayTouchLead(tenantA, "逾期未跟进客户", "13700010004")
	pastAt := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	h.mustDo("POST", "/api/v1/leads/"+overdueLeadID+"/follow-through", sessionSalesA1, tenantA,
		fmt.Sprintf(`{"note":"前天约定回访","complete":true,"next_follow_up_at":%q}`, pastAt), http.StatusCreated)

	// 6. new_inquiry
	newLeadID := h.todayTouchLead(tenantA, "新碰一碰咨询", "13700010005")

	// 7. human_takeover_needed
	humanSess := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "",
		`{"visitor_key":"visitor-key-q-human1"}`, http.StatusCreated)
	humanSID := humanSess["session"].(map[string]any)["id"].(string)
	h.mustDo("POST", "/api/v1/reception/sessions/"+humanSID+"/takeover", sessionOwnerA, tenantA, `{}`, http.StatusOK)

	desk := h.mustDo("GET", "/api/v1/workbench", sessionOwnerA, tenantA, "", http.StatusOK)
	raw := mustJSON(desk)
	for _, phone := range []string{"13700010001", "13700010002", "13700010003", "13700010004", "13700010005"} {
		if strings.Contains(raw, phone) {
			t.Fatalf("workbench response leaked phone %s", phone)
		}
	}
	if strings.Contains(raw, "自动触达已成功") {
		t.Fatalf("workbench leaked fake auto-outreach success: %s", raw)
	}

	queue, ok := desk["today_queue"].([]any)
	if !ok || len(queue) < 7 {
		t.Fatalf("today_queue missing or too short: %v", desk["today_queue"])
	}

	// Verify strict action-first bucket ordering across all 7 buckets.
	wantBucketRank := map[string]int{
		"human_takeover_needed":            0,
		"new_inquiry":                      1,
		"overdue":                          2,
		"due_today":                        3,
		"ai_draft_pending":                 4,
		"waiting_customer":                 5,
		"missing_info_or_permission_error": 6,
	}
	seenIDs := map[string]string{}
	lastRank := -1
	for i, elem := range queue {
		item := elem.(map[string]any)
		bucket := item["bucket"].(string)
		rank, ok := wantBucketRank[bucket]
		if !ok {
			t.Fatalf("queue[%d] unexpected bucket %q", i, bucket)
		}
		if rank < lastRank {
			t.Fatalf("queue out of action-first order at index %d: bucket=%s rank=%d after rank=%d", i, bucket, rank, lastRank)
		}
		lastRank = rank
		id, _ := item["id"].(string)
		seenIDs[id] = bucket
		eff, _ := item["effective_next"].(map[string]any)
		if eff["auto_call"] == true || eff["auto_message"] == true || eff["auto_sop"] == true || eff["force_opportunity"] == true {
			t.Fatalf("queue[%d] effective_next armed automatic side effect: %v", i, eff)
		}
	}

	wantPlacements := map[string]string{
		humanSID:      "human_takeover_needed",
		newLeadID:     "new_inquiry",
		overdueLeadID: "overdue",
		dueLeadID:     "due_today",
		draftID:       "ai_draft_pending",
		waitLeadID:    "waiting_customer",
		deniedLeadID:  "missing_info_or_permission_error",
	}
	for id, wantBucket := range wantPlacements {
		if got := seenIDs[id]; got != wantBucket {
			t.Fatalf("item %s placed in %q, want %q (all=%v)", id, got, wantBucket, seenIDs)
		}
	}

	// Verify honest friction_baseline defaults to not_measured / not_claimed.
	fb, _ := desk["friction_baseline"].(map[string]any)
	if fb["measured"] != false || fb["baseline_steps"] != "not_measured" ||
		fb["active_time"] != "not_measured" || fb["human_active_time_ms"] != "not_measured" ||
		fb["reduction"] != "not_claimed" {
		t.Fatalf("friction_baseline must remain honest unmeasured: %v", fb)
	}
}

func TestTodayNextHTTPIdempotencyCrossViewAndConcurrentDoubleClick(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureFollowups: true})
	tenantA, _, _ := h.seed()
	contactID, leadID := h.todayTouchContactLead(tenantA, "碰一碰幂等客户", "13700020001")

	nextAt := shanghaiWall(15, 30)
	const idemKey = "touch-idem-cross-view-001"
	const note = "碰一碰首跟进，约下午回访"
	bodyToday := fmt.Sprintf(`{"note":%q,"next_follow_up_at":%q,"idempotency_key":%q,"ai_score":99}`, note, nextAt, idemKey)

	// 1. Concurrent double-click on Today follow-through endpoint under -race.
	const clicks = 6
	var wg sync.WaitGroup
	results := make([]map[string]any, clicks)
	statuses := make([]int, clicks)
	for i := 0; i < clicks; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			st, out, _ := h.do("POST", "/api/v1/leads/"+leadID+"/follow-through", sessionSalesA1, tenantA, bodyToday)
			statuses[idx] = st
			results[idx] = out
		}(i)
	}
	wg.Wait()

	var firstID string
	for i := 0; i < clicks; i++ {
		if statuses[i] != http.StatusCreated && statuses[i] != http.StatusOK {
			t.Fatalf("click %d status = %d body = %v", i, statuses[i], results[i])
		}
		fid, _ := results[i]["follow_up_id"].(string)
		if fid == "" {
			t.Fatalf("click %d missing follow_up_id: %v", i, results[i])
		}
		if firstID == "" {
			firstID = fid
		} else if fid != firstID {
			t.Fatalf("double-click created two follow_ups: %s vs %s", firstID, fid)
		}
	}

	// 2. Retrying the SAME idempotency_key from Lead Detail view (POST /api/v1/follow-ups)
	//    MUST deduplicate to the exact same follow_up_id and NOT write a second row.
	bodyDetail := fmt.Sprintf(`{"contact_id":%q,"lead_id":%q,"note":%q,"next_follow_up_at":%q,"idempotency_key":%q}`,
		contactID, leadID, note, nextAt, idemKey)
	detailReplay := h.mustDo("POST", "/api/v1/follow-ups", sessionSalesA1, tenantA, bodyDetail, http.StatusOK)
	if detailReplay["id"] != firstID {
		t.Fatalf("cross-view retry returned %v, want %s", detailReplay["id"], firstID)
	}
	if n := countFollowNotes(t, h, tenantA, leadID, note); n != 1 {
		t.Fatalf("expected 1 follow_up row after concurrent Today + Lead Detail retries, got %d", n)
	}

	// 3. Reusing the same idempotency_key with a different payload MUST fail closed with 409.
	conflictBody := fmt.Sprintf(`{"contact_id":%q,"lead_id":%q,"note":"另一句不同的跟进","next_follow_up_at":%q,"idempotency_key":%q}`,
		contactID, leadID, nextAt, idemKey)
	st, out, _ := h.do("POST", "/api/v1/follow-ups", sessionSalesA1, tenantA, conflictBody)
	if st != http.StatusConflict || out["error"] != "idempotency_conflict" {
		t.Fatalf("cross-view payload conflict = %d %v, want 409 idempotency_conflict", st, out)
	}
}

func TestTodayNextHTTPTenantSwitchDisabledMemberAndReassignment(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureFollowups: true})
	tenantA, tenantB, _ := h.seed()
	// Give owner A membership in tenant B as well so we can test a real multi-tenant A/B switch.
	h.provision("member", tenantB, principalOwnerA, "owner", "Owner A in B")

	contactID, leadID := h.todayTouchContactLead(tenantA, "权限边界客户", "13700030001")
	salesA1ID := h.memberID(tenantA, principalSalesA1)
	salesA2ID := h.memberID(tenantA, principalSalesA2)

	// 1. Stale tenant A/B switch: caller switched workspace header to tenantB while form body still carries tenantA.
	st, out, _ := h.do("POST", "/api/v1/leads/"+leadID+"/follow-through", sessionOwnerA, tenantB,
		fmt.Sprintf(`{"tenant_id":%q,"note":"切租户残留提交","idempotency_key":"stale-ab-1"}`, tenantA))
	if st != http.StatusBadRequest && st != http.StatusForbidden {
		t.Fatalf("tenant A/B switch must fail closed, got %d %v", st, out)
	}
	if out["error"] != "cross_tenant" {
		t.Fatalf("tenant A/B switch error = %v, want cross_tenant", out["error"])
	}

	// 2. Sales A1 commits one follow-up with idempotency_key while assigned.
	committed := h.mustDo("POST", "/api/v1/leads/"+leadID+"/follow-through", sessionSalesA1, tenantA,
		fmt.Sprintf(`{"note":"交接前跟进","idempotency_key":"before-reassign-1","expected_assignee_member_id":%q}`, salesA1ID), http.StatusCreated)
	if committed["follow_up_id"] == "" {
		t.Fatalf("expected follow_up_id: %v", committed)
	}

	// 3. Owner reassigns the lead from Sales A1 to Sales A2.
	h.mustDo("PATCH", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA,
		fmt.Sprintf(`{"assigned_member_id":%q}`, salesA2ID), http.StatusOK)

	// Sales A1 retrying (even with the already-committed idempotency_key!) MUST fail closed (404 mask), zero cache leakage.
	st, out, _ = h.do("POST", "/api/v1/leads/"+leadID+"/follow-through", sessionSalesA1, tenantA,
		`{"note":"交接前跟进","idempotency_key":"before-reassign-1"}`)
	if st != http.StatusNotFound || out["follow_up_id"] != nil {
		t.Fatalf("reassigned sales retry must fail closed with 404 and zero cache leak, got %d %v", st, out)
	}

	// Owner submitting with stale expected_assignee_member_id (still Sales A1) MUST fail closed with 409 record_reassigned.
	st, out, _ = h.do("POST", "/api/v1/follow-ups", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"lead_id":%q,"note":"过期视图提交","expected_assignee_member_id":%q,"idempotency_key":"owner-stale-1"}`,
			contactID, leadID, salesA1ID))
	if st != http.StatusConflict || out["error"] != "record_reassigned" {
		t.Fatalf("stale expected assignee must fail with 409 record_reassigned, got %d %v", st, out)
	}

	// 4. Disabled member trying to write MUST fail closed with 403 member_disabled.
	h.mustDo("PATCH", "/api/v1/admin/members/"+salesA2ID, sessionOwnerA, tenantA, `{"enabled":false}`, http.StatusOK)
	st, out, _ = h.do("POST", "/api/v1/leads/"+leadID+"/follow-through", sessionSalesA2, tenantA,
		`{"note":"停用销售试图写入","idempotency_key":"disabled-write-1"}`)
	if st != http.StatusForbidden || out["error"] != "member_disabled" {
		t.Fatalf("disabled member write must fail with 403 member_disabled, got %d %v", st, out)
	}

	// Verify tenant B workbench contains zero trace of tenant A's lead/contact.
	deskB := h.mustDo("GET", "/api/v1/workbench", sessionOwnerB, tenantB, "", http.StatusOK)
	rawB := mustJSON(deskB)
	if strings.Contains(rawB, leadID) || strings.Contains(rawB, contactID) {
		t.Fatalf("tenant B leaked tenant A records: %s", rawB)
	}
}

func TestTodayNextHTTPConsentRevocationAndIndividualCustomerNoCompany(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureFollowups: true})
	tenantA, _, _ := h.seed()

	// 1. Individual customer without any company field is allowed end-to-end.
	indContact := h.mustDo("POST", "/api/v1/contacts", sessionSalesA1, tenantA,
		`{"name":"个人买家小王","phone":"13700040001","business_category":"merchant_customer","source_type":"manual","consent_status":"pending"}`, http.StatusCreated)
	indContactID := indContact["id"].(string)
	indLead := h.mustDo("POST", "/api/v1/leads", sessionSalesA1, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"status":"new"}`, indContactID), http.StatusCreated)
	indLeadID := indLead["id"].(string)

	// 2. Missing marketing consent blocks auto_outreach, trigger_sop, claim_auto_outreach_success.
	for _, payload := range []string{
		`{"note":"尝试自动外发","auto_outreach":true}`,
		`{"note":"尝试自动 SOP","trigger_sop":true}`,
		`{"note":"尝试展示自动触达成功","claim_auto_outreach_success":true}`,
	} {
		st, out, _ := h.do("POST", "/api/v1/leads/"+indLeadID+"/follow-through", sessionSalesA1, tenantA, payload)
		if st != http.StatusConflict {
			t.Fatalf("payload %s must be blocked with 409, got %d %v", payload, st, out)
		}
		if strings.Contains(mustJSON(out), "自动触达已成功") {
			t.Fatalf("blocked consent response emitted fake success: %v", out)
		}
	}

	// 3. Grant marketing consent then REVOKE it; revoked consent must block auto_outreach and trigger_sop.
	h.mustDo("POST", "/api/v1/contacts/"+indContactID+"/consents", sessionSalesA1, tenantA,
		`{"source_submission_ref":"sub-mkt-1","source_channel":"sms","notice_version":"v1","purpose":"marketing","marketing_allowed":true}`, http.StatusOK)
	h.mustDo("POST", "/api/v1/contacts/"+indContactID+"/revoke-marketing", sessionSalesA1, tenantA,
		`{"reason":"客户退订"}`, http.StatusOK)

	st, out, _ := h.do("POST", "/api/v1/leads/"+indLeadID+"/follow-through", sessionSalesA1, tenantA,
		`{"note":"撤销后触发SOP","trigger_sop":true}`)
	if st != http.StatusConflict || out["error"] != "marketing_consent_revoked" {
		t.Fatalf("revoked consent SOP must fail with 409 marketing_consent_revoked, got %d %v", st, out)
	}

	// 4. High AI score NEVER triggers auto_call.
	st, out, _ = h.do("POST", "/api/v1/leads/"+indLeadID+"/follow-through", sessionSalesA1, tenantA,
		`{"note":"AI 99分尝试自动外呼","ai_score":99,"auto_call":true}`)
	if st != http.StatusConflict || out["error"] != "ai_score_auto_call_forbidden" {
		t.Fatalf("AI auto_call must fail with 409 ai_score_auto_call_forbidden, got %d %v", st, out)
	}

	// 5. Ordinary manual follow-up for the individual customer (without company) succeeds with zero automation.
	okRes := h.mustDo("POST", "/api/v1/leads/"+indLeadID+"/follow-through", sessionSalesA1, tenantA,
		`{"note":"个人客户手工微信确认尺寸","complete":true,"disposition":"waiting_customer","ai_score":99}`, http.StatusCreated)
	auto := okRes["automation"].(map[string]any)
	if auto["auto_call"] == true || auto["auto_message"] == true || auto["create_order"] == true {
		t.Fatalf("automation must stay false: %v", auto)
	}
}
