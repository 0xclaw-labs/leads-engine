package todaynext

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/receptionguard"
	"github.com/bianjiefilm/leads-engine/server/internal/workbench"
)

func TestSortWorkItemsSevenBucketsAndManualOverridesAI(t *testing.T) {
	// Deliberately scrambled order (opposite of canonical priority, simulating raw CRM table order).
	items := []WorkItem{
		{
			ID:               "w_info",
			TenantID:         "tnt_a",
			RecordKind:       "lead",
			LeadID:           "lead_info",
			Bucket:           ActionBucket(workbench.TodayInfo), // legacy alias
			SourceCampaign:   "camp-info",
			SourceChannel:    "碰一碰",
			ConfirmedContext: workbench.ConfirmedContext{Customer: "缺信息客户", Ask: []string{"负责人"}},
			ConsentState:     "denied",
			ConsentRevoked:   true,
			AllowedChannels:  []string{"sms", "phone"}, // must be stripped because revoked
			OutreachNotice:   "自动触达已成功",                // must be sanitized away
			LastInteraction:  workbench.Interaction{At: "2026-11-25T09:00:00Z", Summary: "联系电话 13800138000 待核对"},
		},
		{
			ID:              "w_wait",
			TenantID:        "tnt_a",
			RecordKind:      "lead",
			LeadID:          "lead_wait",
			Bucket:          BucketWaitingCustomer,
			SourceCampaign:  "camp-touch-2",
			SourceChannel:   "碰一碰",
			OwnerLabel:      "Sales A1",
			ConsentState:    "granted",
			LastInteraction: workbench.Interaction{At: "2026-11-25T08:00:00Z", Summary: "已报价，等待客户确认"},
		},
		{
			ID:             "w_draft",
			TenantID:       "tnt_a",
			RecordKind:     "ai_draft",
			SessionID:      "sess_draft",
			Bucket:         ActionBucket(workbench.TodayAIDraft), // legacy alias
			SourceChannel:  "网站咨询",
			OwnerLabel:     "Sales A1",
			DraftBody:      "您好，这是 AI 草稿，请确认后再发，勿带 13911112222",
			SuggestedNext:  &NextStep{Kind: "confirm_draft", Source: "ai_suggestion", Label: "确认或修改草稿", AutoCall: true, AutoMessage: true, AutoSOP: true},
			AIScore:        98,
			LastInteraction: workbench.Interaction{At: "2026-11-25T08:30:00Z", Summary: "AI 生成草稿"},
		},
		{
			ID:             "w_due_late",
			TenantID:       "tnt_a",
			RecordKind:     "lead",
			LeadID:         "lead_due_late",
			Bucket:         BucketDueToday,
			SourceCampaign: "camp-due",
			SourceChannel:  "表单",
			OwnerLabel:     "Sales A1",
			DueAt:          "2026-11-25T16:00:00Z",
			SuggestedNext:  &NextStep{Kind: "suggest_schedule", Source: "ai_suggestion", Label: "AI 建议今天外呼", AutoCall: true, AutoMessage: true},
			ManualNext:     &NextStep{Kind: "manual_follow_up", At: "2026-11-25T16:00:00Z", Label: "下午四点手工微信跟进"},
			AIScore:        99,
		},
		{
			ID:             "w_due_early",
			TenantID:       "tnt_a",
			RecordKind:     "lead",
			LeadID:         "lead_due_early",
			Bucket:         BucketDueToday,
			SourceCampaign: "camp-due",
			SourceChannel:  "表单",
			OwnerLabel:     "Sales A1",
			DueAt:          "2026-11-25T10:00:00Z",
			ManualNext:     &NextStep{Kind: "manual_follow_up", At: "2026-11-25T10:00:00Z"},
		},
		{
			ID:             "w_overdue_newer",
			TenantID:       "tnt_a",
			RecordKind:     "lead",
			LeadID:         "lead_overdue_2",
			Bucket:         BucketOverdue,
			DueAt:          "2026-11-24T12:00:00Z",
			SourceCampaign: "camp-od",
			SourceChannel:  "碰一碰",
		},
		{
			ID:             "w_overdue_older",
			TenantID:       "tnt_a",
			RecordKind:     "lead",
			LeadID:         "lead_overdue_1",
			Bucket:         BucketOverdue,
			DueAt:          "2026-11-22T09:00:00Z",
			SourceCampaign: "camp-od",
			SourceChannel:  "碰一碰",
		},
		{
			ID:              "w_new",
			TenantID:        "tnt_a",
			RecordKind:      "lead",
			LeadID:          "lead_new",
			Bucket:          BucketNewInquiry,
			SourceCampaign:  "camp-touch-new",
			SourceChannel:   "碰一碰",
			OwnerLabel:      "Sales A1",
			LastInteraction: workbench.Interaction{At: "2026-11-25T09:30:00Z", Summary: "扫码留资"},
		},
		{
			ID:              "w_human",
			TenantID:        "tnt_a",
			RecordKind:      "reception",
			SessionID:       "sess_human",
			Bucket:          ActionBucket(workbench.TodayHuman), // legacy alias
			SourceChannel:   "碰一碰",
			OwnerLabel:      "Sales A1",
			LastInteraction: workbench.Interaction{At: "2026-11-25T09:45:00Z", Summary: "访客请求人工接管"},
		},
	}

	sorted := SortWorkItems(items)
	wantIDs := []string{
		"w_human",
		"w_new",
		"w_overdue_older",
		"w_overdue_newer",
		"w_due_early",
		"w_due_late",
		"w_draft",
		"w_wait",
		"w_info",
	}
	if len(sorted) != len(wantIDs) {
		t.Fatalf("len(sorted) = %d, want %d", len(sorted), len(wantIDs))
	}
	for i, want := range wantIDs {
		if sorted[i].ID != want {
			t.Fatalf("sorted[%d].ID = %q (bucket=%s), want %q", i, sorted[i].ID, sorted[i].Bucket, want)
		}
	}

	// Verify canonical 7 bucket ordering strictly matches specification.
	wantOrder := CanonicalBucketOrder()
	seenBuckets := []ActionBucket{}
	for _, it := range sorted {
		if len(seenBuckets) == 0 || seenBuckets[len(seenBuckets)-1] != it.Bucket {
			seenBuckets = append(seenBuckets, it.Bucket)
		}
	}
	if len(seenBuckets) != len(wantOrder) {
		t.Fatalf("buckets = %v, want %v", seenBuckets, wantOrder)
	}
	for i := range wantOrder {
		if seenBuckets[i] != wantOrder[i] {
			t.Fatalf("bucket[%d] = %q, want %q", i, seenBuckets[i], wantOrder[i])
		}
	}

	// Verify manual Next Action strictly overrides AI suggestion even with AIScore=99.
	dueLate := sorted[5]
	if dueLate.EffectiveNext.Source != "manual" || dueLate.EffectiveNext.Kind != "manual_follow_up" ||
		dueLate.EffectiveNext.At != "2026-11-25T16:00:00Z" || dueLate.EffectiveNext.Label != "下午四点手工微信跟进" {
		t.Fatalf("manual did not override AI suggestion: %+v", dueLate.EffectiveNext)
	}
	if dueLate.EffectiveNext.AutoCall || dueLate.EffectiveNext.AutoMessage || dueLate.EffectiveNext.AutoSOP || dueLate.EffectiveNext.ForceOpportunity {
		t.Fatalf("side-effect flag leaked on manual next: %+v", dueLate.EffectiveNext)
	}

	// Verify AI draft item is editable & dismissible, scrubs phone, and has no side effects.
	draft := sorted[6]
	if !draft.DraftEditable || !draft.DraftDismissible {
		t.Fatalf("draft must be editable & dismissible: %+v", draft)
	}
	if strings.Contains(draft.DraftBody, "13911112222") {
		t.Fatalf("draft leaked raw phone: %q", draft.DraftBody)
	}
	if !draft.EffectiveNext.Editable || !draft.EffectiveNext.Dismissible ||
		draft.EffectiveNext.AutoCall || draft.EffectiveNext.AutoMessage || draft.EffectiveNext.AutoSOP {
		t.Fatalf("draft suggestion violated editable/dismissible/non-auto contract: %+v", draft.EffectiveNext)
	}

	// Verify revoked consent item strips sms/phone channels, scrubs phone, and never claims auto-outreach success.
	info := sorted[8]
	if len(info.AllowedChannels) != 0 {
		t.Fatalf("revoked consent kept marketing channels: %v", info.AllowedChannels)
	}
	if strings.Contains(info.OutreachNotice, "自动触达已成功") {
		t.Fatalf("revoked consent showed fake outreach success: %q", info.OutreachNotice)
	}
	if strings.Contains(info.LastInteraction.Summary, "13800138000") {
		t.Fatalf("last interaction leaked phone: %q", info.LastInteraction.Summary)
	}

	// Dismissing an AI suggestion yields a safe manual none action.
	dismissed := ResolveEffectiveNextAction(&NextStep{Kind: "suggest_schedule", Source: "ai_suggestion", Dismissed: true}, nil, 100)
	if dismissed.Kind != "none" || dismissed.Source != "manual" || dismissed.AutoCall || dismissed.AutoMessage || dismissed.AutoSOP {
		t.Fatalf("dismissed suggestion = %+v", dismissed)
	}
}

func TestFollowUpIdempotencyGuardConcurrentAndCrossViewAndBoundaries(t *testing.T) {
	guard := NewFollowUpIdempotencyGuard()

	// 1. Concurrent double-click under -race: only 1 goroutine executes the write,
	//    all others wait and receive replay with identical follow_up_id.
	const workers = 12
	var writes atomic.Int32
	var replays atomic.Int32
	var wg sync.WaitGroup
	ids := make([]string, workers)

	baseCtx := MutationContext{
		TenantID:         "tnt_a",
		BodyTenantID:     "tnt_a",
		PrincipalID:      "usr_sales_a1",
		MemberID:         "mem_sales_a1",
		MemberRole:       "sales",
		MemberEnabled:    true,
		RecordTenantID:   "tnt_a",
		RecordID:         "lead_touch_1",
		RecordAssigneeID: "mem_sales_a1",
		ExpectedOwnerID:  "mem_sales_a1",
		IdempotencyKey:   "idem-touch-first-001",
		PayloadDigest:    CanonicalPayloadDigest("lead_touch_1", "首次碰一碰跟进", "2026-11-26T09:00:00Z", "", ""),
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx := baseCtx
			if idx%2 == 0 {
				ctx.ViewSource = "today"
			} else {
				ctx.ViewSource = "lead_detail"
			}
			dec, lease := guard.Begin(ctx)
			if !dec.Allowed {
				t.Errorf("worker %d unexpectedly blocked: %+v", idx, dec)
				return
			}
			if dec.Replay {
				replays.Add(1)
				ids[idx] = dec.Cached.FollowUpID
				return
			}
			defer lease.Abort()
			writes.Add(1)
			time.Sleep(15 * time.Millisecond)
			lease.Commit(CachedWriteResult{
				FollowUpID:    "fup_single_write",
				TodayGroup:    "due_today",
				PayloadDigest: ctx.PayloadDigest,
			})
			ids[idx] = "fup_single_write"
		}(i)
	}
	wg.Wait()

	if writes.Load() != 1 {
		t.Fatalf("expected exactly 1 persisted write across %d concurrent double-clicks, got %d", workers, writes.Load())
	}
	if replays.Load() != workers-1 {
		t.Fatalf("expected %d replays, got %d", workers-1, replays.Load())
	}
	for i, id := range ids {
		if id != "fup_single_write" {
			t.Fatalf("worker %d got follow_up_id=%q, want fup_single_write", i, id)
		}
	}

	// 2. Same idempotency_key with different payload must fail closed with 409 conflict.
	conflictCtx := baseCtx
	conflictCtx.ViewSource = "lead_detail"
	conflictCtx.PayloadDigest = CanonicalPayloadDigest("lead_touch_1", "改了不同跟进文案", "2026-11-26T09:00:00Z", "", "")
	conflictDec, lease := guard.Begin(conflictCtx)
	if conflictDec.Allowed || !conflictDec.Conflict || conflictDec.HTTPStatus != http.StatusConflict || lease != nil {
		t.Fatalf("different payload with same key must conflict: %+v", conflictDec)
	}

	// 3. Tenant A/B switch (stale body tenant or cross-tenant record) MUST fail closed
	//    even when reusing the previously committed idempotency_key (zero leakage).
	staleTenantCtx := baseCtx
	staleTenantCtx.TenantID = "tnt_b"
	staleTenantCtx.BodyTenantID = "tnt_a"
	staleDec, _ := guard.Begin(staleTenantCtx)
	if staleDec.Allowed || staleDec.Code != "cross_tenant" || staleDec.Cached != nil {
		t.Fatalf("tenant A/B switch must fail closed with zero cache leakage: %+v", staleDec)
	}

	crossRecCtx := baseCtx
	crossRecCtx.RecordTenantID = "tnt_b"
	crossDec, _ := guard.Begin(crossRecCtx)
	if crossDec.Allowed || crossDec.Code != "cross_tenant" || crossDec.Cached != nil {
		t.Fatalf("cross-tenant record must fail closed: %+v", crossDec)
	}

	// 4. Disabled member MUST fail closed even on retry with a committed key.
	disabledCtx := baseCtx
	disabledCtx.MemberEnabled = false
	disabledDec, _ := guard.Begin(disabledCtx)
	if disabledDec.Allowed || disabledDec.HTTPStatus != http.StatusForbidden || disabledDec.Code != "member_disabled" || disabledDec.Cached != nil {
		t.Fatalf("disabled member must fail closed: %+v", disabledDec)
	}

	// 5. Reassigned record MUST fail closed for the previous assignee AND for stale expected owner.
	reassignedCtx := baseCtx
	reassignedCtx.RecordAssigneeID = "mem_sales_a2"
	reassignedDec, _ := guard.Begin(reassignedCtx)
	if reassignedDec.Allowed || reassignedDec.Code != "record_reassigned" || reassignedDec.Cached != nil {
		t.Fatalf("reassigned record must fail closed for old assignee: %+v", reassignedDec)
	}

	ownerStaleViewCtx := baseCtx
	ownerStaleViewCtx.MemberRole = "owner"
	ownerStaleViewCtx.RecordAssigneeID = "mem_sales_a2"
	ownerStaleViewCtx.ExpectedOwnerID = "mem_sales_a1"
	ownerStaleDec, _ := guard.Begin(ownerStaleViewCtx)
	if ownerStaleDec.Allowed || ownerStaleDec.HTTPStatus != http.StatusConflict || ownerStaleDec.Code != "record_reassigned" {
		t.Fatalf("stale expected owner must conflict: %+v", ownerStaleDec)
	}
}

func TestValidateTodayActionConsentAndNonCoercion(t *testing.T) {
	// 1. Missing marketing consent blocks auto-outreach, auto-SOP, and auto-outreach success claims.
	missingOutreach := ValidateTodayAction(TodayActionInput{
		TenantID:                 "tnt_a",
		Action:                   ActionAutoOutreach,
		MarketingAllowed:         false,
		ConsentStatus:            "pending",
		TriggerAutoOutbound:      true,
		ClaimAutoOutreachSuccess: true,
	})
	if missingOutreach.Allowed || missingOutreach.AutoOutreachSuccess || missingOutreach.AllowAutoSOP || missingOutreach.AllowAutoCall {
		t.Fatalf("missing marketing consent must block auto-outreach: %+v", missingOutreach)
	}
	if strings.Contains(missingOutreach.OutreachNotice, "自动触达已成功") {
		t.Fatalf("missing consent emitted fake success notice: %+v", missingOutreach)
	}

	// 2. Revoked marketing consent blocks auto-SOP and auto-outreach even if MarketingAllowed flag is stale true.
	revokedSOP := ValidateTodayAction(TodayActionInput{
		TenantID:         "tnt_a",
		Action:           ActionAutoSOP,
		MarketingAllowed: true,
		ConsentRevoked:   true,
		TriggerAutoSOP:   true,
		Consent: &receptionguard.ConsentRecord{
			TenantID:         "tnt_a",
			MarketingAllowed: true,
			Revoked:          true,
			NoticeVersion:    "v1",
		},
	})
	if revokedSOP.Allowed || revokedSOP.Code != "marketing_consent_revoked" || revokedSOP.AllowAutoSOP {
		t.Fatalf("revoked consent must block auto-SOP: %+v", revokedSOP)
	}

	// 3. Even with valid marketing consent, claiming auto-outreach success without a stored send receipt is forbidden.
	noReceipt := ValidateTodayAction(TodayActionInput{
		TenantID:                 "tnt_a",
		Action:                   ActionManualFollowUp,
		MarketingAllowed:         true,
		ConsentStatus:            "granted",
		SendReceiptStored:        false,
		ClaimAutoOutreachSuccess: true,
	})
	if noReceipt.Allowed || noReceipt.AutoOutreachSuccess || noReceipt.Code != "send_receipt_required" {
		t.Fatalf("missing send receipt must reject outreach success claim: %+v", noReceipt)
	}

	// 4. Ordinary inquiry and after-sales MUST NOT force opportunity creation.
	for _, purpose := range []string{"consult", "ordinary_inquiry", "ordinary_faq", "after_sales", "marketing_refused"} {
		dec := ValidateTodayAction(TodayActionInput{
			TenantID:         "tnt_a",
			Action:           ActionCreateOpportunity,
			Purpose:          purpose,
			ForceOpportunity: true,
		})
		if dec.Allowed || dec.Code != "ordinary_inquiry_no_forced_opportunity" || dec.ForceOpportunity {
			t.Fatalf("purpose=%q must not force opportunity: %+v", purpose, dec)
		}
	}

	// 5. Individual customer MUST NOT require a company field.
	individualOK := ValidateTodayAction(TodayActionInput{
		TenantID:       "tnt_a",
		Action:         ActionCreateContact,
		CustomerType:   CustomerIndividual,
		Company:        "",
		RequireCompany: false,
	})
	if !individualOK.Allowed || individualOK.CompanyRequired {
		t.Fatalf("individual customer without company must be allowed: %+v", individualOK)
	}
	individualForcedCompany := ValidateTodayAction(TodayActionInput{
		TenantID:       "tnt_a",
		Action:         ActionCreateContact,
		CustomerType:   CustomerIndividual,
		Company:        "",
		RequireCompany: true,
	})
	if individualForcedCompany.Allowed || individualForcedCompany.Code != "individual_customer_company_not_required" {
		t.Fatalf("forcing company on individual customer must be rejected: %+v", individualForcedCompany)
	}

	// 6. High AI score NEVER triggers auto-calling.
	aiCall := ValidateTodayAction(TodayActionInput{
		TenantID:         "tnt_a",
		Action:           ActionAutoCall,
		MarketingAllowed: true,
		ConsentStatus:    "granted",
		AIScore:          100,
		TriggerAutoCall:  true,
	})
	if aiCall.Allowed || aiCall.Code != "ai_score_auto_call_forbidden" || aiCall.AllowAutoCall {
		t.Fatalf("AI score must never trigger auto-call: %+v", aiCall)
	}
}

func TestFrictionBaselineRecordHonestyContract(t *testing.T) {
	// 1. Default unmeasured baseline keeps explicit not_measured / not_claimed and passes Validate().
	unmeasured := NewUnmeasuredFrictionBaseline("HUI-2598")
	if unmeasured.Measured || unmeasured.BaselineSteps != StatusNotMeasured ||
		unmeasured.ActiveTime != StatusNotMeasured || unmeasured.HumanActiveTimeMS != StatusNotMeasured ||
		unmeasured.Reduction != StatusNotClaimed {
		t.Fatalf("unmeasured baseline = %+v", unmeasured)
	}
	if err := unmeasured.Validate(); err != nil {
		t.Fatalf("unmeasured baseline failed Validate(): %v", err)
	}

	// 2. Forged unbacked claims (e.g. claiming reduction/pass/0 without measurement) MUST be rejected.
	forged := unmeasured
	forged.Reduction = "-45%"
	if err := forged.Validate(); !errors.Is(err, ErrUnbackedClaim) {
		t.Fatalf("forged reduction error = %v, want ErrUnbackedClaim", err)
	}

	forgedPass := unmeasured
	forgedPass.BaselineSteps = "pass"
	if err := forgedPass.Validate(); !errors.Is(err, ErrUnbackedClaim) {
		t.Fatalf("forged pass error = %v, want ErrUnbackedClaim", err)
	}

	// 3. Fewer than 5 rounds MUST be rejected and return honest unmeasured values.
	fourRounds := validFiveRounds()[:4]
	rec4, err := BuildFrictionBaseline("HUI-2598", fourRounds, true)
	if !errors.Is(err, ErrInsufficientRounds) {
		t.Fatalf("4 rounds error = %v, want ErrInsufficientRounds", err)
	}
	if rec4.Measured || rec4.Reduction != StatusNotClaimed || rec4.BaselineSteps != StatusNotMeasured {
		t.Fatalf("4 rounds leaked measured claim: %+v", rec4)
	}

	// 4. Unverified same-input MUST be rejected.
	if _, err := BuildFrictionBaseline("HUI-2598", validFiveRounds(), false); !errors.Is(err, ErrSameInputUnverified) {
		t.Fatalf("unverified same-input error = %v, want ErrSameInputUnverified", err)
	}
	mismatchedInput := validFiveRounds()
	mismatchedInput[3].InputSignature = "different-input-fixture"
	if _, err := BuildFrictionBaseline("HUI-2598", mismatchedInput, true); !errors.Is(err, ErrSameInputUnverified) {
		t.Fatalf("mismatched input signature error = %v, want ErrSameInputUnverified", err)
	}

	// 5. Unseparated human active time vs network/model wait time MUST be rejected.
	unseparated := validFiveRounds()
	unseparated[2].SeparatedWaitTime = false
	if _, err := BuildFrictionBaseline("HUI-2598", unseparated, true); !errors.Is(err, ErrWaitTimeNotSeparated) {
		t.Fatalf("unseparated wait time error = %v, want ErrWaitTimeNotSeparated", err)
	}

	// 6. Valid 5-round same-input separated measurements pass and compute honest averages.
	measured, err := BuildFrictionBaseline("HUI-2598", validFiveRounds(), true)
	if err != nil {
		t.Fatalf("valid 5-round build failed: %v", err)
	}
	if !measured.Measured || measured.RoundsCount != 5 || !measured.SameInputVerified || !measured.WaitTimeSeparated {
		t.Fatalf("valid measured metadata = %+v", measured)
	}
	if err := measured.Validate(); err != nil {
		t.Fatalf("valid measured failed Validate(): %v", err)
	}
	if !strings.Contains(measured.Reduction, "steps=50.0%") || !strings.Contains(measured.Reduction, "human_active=50.0%") {
		t.Fatalf("unexpected reduction = %q", measured.Reduction)
	}
}

func validFiveRounds() []FrictionRoundSample {
	out := make([]FrictionRoundSample, 5)
	for i := 0; i < 5; i++ {
		out[i] = FrictionRoundSample{
			Round:               i + 1,
			InputSignature:      "touch-lead-same-input-v1",
			BeforeSteps:         8,
			AfterSteps:          4,
			BeforeHumanActiveMS: 12000,
			BeforeNetworkWaitMS: 600,
			BeforeModelWaitMS:   900,
			AfterHumanActiveMS:  6000,
			AfterNetworkWaitMS:  550,
			AfterModelWaitMS:    850,
			SeparatedWaitTime:   true,
		}
	}
	return out
}
