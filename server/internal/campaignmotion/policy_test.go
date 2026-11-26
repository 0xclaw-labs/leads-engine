package campaignmotion

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPrepareRestrictedContextWithBrandProductAssetsBudgetPayerReturnURL(t *testing.T) {
	const campaignID = "led_camp_swarm_1"
	raw := []byte(`{
		"tenant_id": "tnt_alpha",
		"campaign_goal": "新品门店推广动效包",
		"target_audience": "华东连锁门店负责人",
		"approved_brand_facts": [
			{"key":"brand_tone","value":"专业克制、蓝白视觉规范","confirmed":true}
		],
		"approved_product_facts": [
			{"key":"core_offer","value":"7天上线多门店动效素材包","confirmed":true}
		],
		"approved_assets": [
			{"id":"asset_logo_v2","authorized":true,"version":"v2.1","expires_at":"2028-01-01T00:00:00Z"},
			{"id":"asset_pack_v3","authorized":true,"version":"v3.0","expires_at":"2028-01-01T00:00:00Z"}
		],
		"cta": "预约门店演示",
		"target_channels": ["抖音","视频号","小红书"],
		"channel_variants": [
			{"channel":"抖音","aspect_ratio":"9:16","layout_preset":"vertical_fast","cta_override":"立即预约","store_label":"上海静安店","asset_slot_overrides":{"logo":"asset_logo_v2"}},
			{"channel":"视频号","aspect_ratio":"16:9","layout_preset":"landscape_brand","store_label":"杭州西湖店","asset_slot_overrides":{"pack":"asset_pack_v3"}},
			{"channel":"小红书","aspect_ratio":"3:4","layout_preset":"card_clean"}
		],
		"budget": {"budget_attribution_id":"budget_q4_east","max_budget_cents":50000},
		"payer": {"payer_kind":"organization","payer_account_id":"acct_org_alpha"},
		"return_url": "/campaigns/led_camp_swarm_1/motion",
		"source_facts_version": "facts_v2026_11",
		"execution_preference": "night",
		"expires_at": "2028-01-01T00:00:00Z",
		"motion": {
			"project_id": "prj_swarm_1",
			"revision_id": "rev_swarm_1",
			"campaign_id": "` + campaignID + `",
			"digest": "` + validDigest() + `"
		}
	}`)

	got, err := Prepare("tnt_alpha", campaignID, raw, panicWriter{})
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if got.ModelCalls != 0 || got.CreativeProjectWrites != 0 || got.PieceGenerated || got.PieceSent {
		t.Fatalf("Prepare must not call models or write creative project: %+v", got)
	}
	if len(got.Document.ApprovedBrandFacts) != 1 || len(got.Document.ApprovedProductFacts) != 1 {
		t.Fatalf("brand/product facts missing: %+v", got.Document)
	}
	if len(got.Document.ApprovedAssets) != 2 || got.Document.ApprovedAssets[0].Version != "v2.1" {
		t.Fatalf("approved assets missing version: %+v", got.Document.ApprovedAssets)
	}
	if got.Document.Budget == nil || got.Document.Budget.MaxBudgetCents != 50000 || got.Document.BudgetAttributionID != "budget_q4_east" {
		t.Fatalf("budget mismatch: %+v", got.Document.Budget)
	}
	if got.Document.Payer == nil || got.Document.Payer.PayerAccountID != "acct_org_alpha" {
		t.Fatalf("payer mismatch: %+v", got.Document.Payer)
	}
	if got.Document.ReturnURL != "/campaigns/led_camp_swarm_1/motion" || got.Document.SourceFactsVersion != "facts_v2026_11" {
		t.Fatalf("return_url/facts_version mismatch: %+v", got.Document)
	}
	if len(got.Document.Variants) != 3 {
		t.Fatalf("want 3 parametric variants, got %d", len(got.Document.Variants))
	}
	if got.Document.Variants[0].AspectRatio != "9:16" || got.Document.Variants[1].AspectRatio != "16:9" || got.Document.Variants[2].AspectRatio != "3:4" {
		t.Fatalf("parametric aspect ratios lost: %+v", got.Document.Variants)
	}
	if got.Document.ExecutionPreference != "night" {
		t.Fatalf("execution_preference = %q, want night", got.Document.ExecutionPreference)
	}
}

func TestPrepareRejectsPrivacyExpiredWrongTenantAndGlobalResources(t *testing.T) {
	const campaignID = "led_camp_guard"
	baseMotion := `"motion":{"project_id":"prj_1","revision_id":"rev_1","campaign_id":"` + campaignID + `","digest":"` + validDigest() + `"}`

	cases := []struct {
		name string
		raw  string
		want error
	}{
		{
			name: "wrong top tenant_id",
			raw:  `{"tenant_id":"tnt_other","campaign_goal":"目标","channels":["抖音"],"budget_attribution_id":"b1",` + baseMotion + `}`,
			want: ErrForeignTenant,
		},
		{
			name: "wrong motion tenant_id",
			raw:  `{"campaign_goal":"目标","channels":["抖音"],"budget_attribution_id":"b1","motion":{"tenant_id":"tnt_other","project_id":"prj_1","revision_id":"rev_1","campaign_id":"` + campaignID + `","digest":"` + validDigest() + `"}}`,
			want: ErrForeignTenant,
		},
		{
			name: "expired top expires_at",
			raw:  `{"expires_at":"2020-01-01T00:00:00Z","campaign_goal":"目标","channels":["抖音"],"budget_attribution_id":"b1",` + baseMotion + `}`,
			want: ErrVariantExpired,
		},
		{
			name: "expired approved asset",
			raw:  `{"campaign_goal":"目标","channels":["抖音"],"budget_attribution_id":"b1","approved_assets":[{"id":"asset_old","authorized":true,"version":"v1","expires_at":"2020-01-01T00:00:00Z"}],` + baseMotion + `}`,
			want: ErrVariantExpired,
		},
		{
			name: "brand fact with customer phone",
			raw:  `{"campaign_goal":"目标","channels":["抖音"],"budget_attribution_id":"b1","approved_brand_facts":[{"key":"slogan","value":"联系王总 13800138000","confirmed":true}],` + baseMotion + `}`,
			want: ErrPrivacyBlocked,
		},
		{
			name: "product fact unconfirmed",
			raw:  `{"campaign_goal":"目标","channels":["抖音"],"budget_attribution_id":"b1","approved_product_facts":[{"key":"price","value":"99元","confirmed":false}],` + baseMotion + `}`,
			want: ErrPrivacyBlocked,
		},
		{
			name: "product fact with chat transcript marker",
			raw:  `{"campaign_goal":"目标","channels":["抖音"],"budget_attribution_id":"b1","approved_product_facts":[{"key":"desc","value":"聊天记录全文：客户说太贵了","confirmed":true}],` + baseMotion + `}`,
			want: ErrPrivacyBlocked,
		},
		{
			name: "return_url smuggling email",
			raw:  `{"campaign_goal":"目标","channels":["抖音"],"budget_attribution_id":"b1","return_url":"/campaigns/c1?email=boss@shop.cn",` + baseMotion + `}`,
			want: ErrPrivacyBlocked,
		},
		{
			name: "global resource config rejected",
			raw:  `{"campaign_goal":"目标","channels":["抖音"],"budget_attribution_id":"b1","gpu_pool":"h100-cluster-a",` + baseMotion + `}`,
			want: ErrGlobalResourceConfigForbidden,
		},
		{
			name: "internal execution preference rejected",
			raw:  `{"campaign_goal":"目标","channels":["抖音"],"budget_attribution_id":"b1","execution_preference":"internal",` + baseMotion + `}`,
			want: ErrInternalPreferenceForbidden,
		},
		{
			name: "channel variant with prompt rejected as linear generation",
			raw:  `{"campaign_goal":"目标","channels":["抖音"],"budget_attribution_id":"b1","channel_variants":[{"channel":"抖音","prompt":"重新生成一条高价视频"}],` + baseMotion + `}`,
			want: ErrLinearModelCallSpike,
		},
		{
			name: "channel variant referencing unauthorized asset",
			raw:  `{"campaign_goal":"目标","channels":["抖音"],"authorized_asset_ids":["asset_ok"],"budget_attribution_id":"b1","channel_variants":[{"channel":"抖音","asset_slot_overrides":{"hero":"asset_unauth"}}],` + baseMotion + `}`,
			want: ErrInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Prepare("tnt_alpha", campaignID, []byte(tc.raw), panicWriter{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got err=%v, want %v", err, tc.want)
			}
		})
	}
}

func TestAuthorizeExecutionPreferenceMatrix(t *testing.T) {
	freeEnt := VerifiedEntitlement{TenantID: "tnt_free", Tier: "free", PlanStatus: "active"}
	paidEnt := VerifiedEntitlement{TenantID: "tnt_pro", Tier: "pro", PlanStatus: "active"}

	// Free account cannot access internal, priority, night, or idle without server grant.
	for _, pref := range []string{"internal", "internal_priority", "system"} {
		if _, err := AuthorizeExecutionPreference(EntitlementCheckInput{
			MemberRole: "owner", RequestedPreference: pref, Entitlement: freeEnt,
		}); !errors.Is(err, ErrInternalPreferenceForbidden) {
			t.Fatalf("free %s err=%v, want ErrInternalPreferenceForbidden", pref, err)
		}
	}
	for _, pref := range []string{"priority", "paid_elastic", "night", "night_batch", "idle", "free_idle"} {
		if _, err := AuthorizeExecutionPreference(EntitlementCheckInput{
			MemberRole: "owner", RequestedPreference: pref, Entitlement: freeEnt,
		}); !errors.Is(err, ErrEntitlementDenied) {
			t.Fatalf("free %s err=%v, want ErrEntitlementDenied", pref, err)
		}
	}

	// Paid verified tenant owner can use immediate/priority/night/idle, but still NEVER internal.
	for _, pref := range []string{"immediate", "priority", "night", "idle", "night_batch", "free_idle", "paid_elastic"} {
		auth, err := AuthorizeExecutionPreference(EntitlementCheckInput{
			MemberRole: "owner", RequestedPreference: pref, Entitlement: paidEnt,
		})
		if err != nil {
			t.Fatalf("paid owner %s unexpected err: %v", pref, err)
		}
		if auth.Canonical == "" {
			t.Fatalf("paid owner %s empty canonical", pref)
		}
	}
	if _, err := AuthorizeExecutionPreference(EntitlementCheckInput{
		MemberRole: "owner", RequestedPreference: "internal", Entitlement: paidEnt,
	}); !errors.Is(err, ErrInternalPreferenceForbidden) {
		t.Fatalf("paid internal err=%v, want ErrInternalPreferenceForbidden", err)
	}

	// Agent cannot select priority or escalate above existing preference rank.
	if _, err := AuthorizeExecutionPreference(EntitlementCheckInput{
		MemberRole: "agent", RequestedPreference: "priority", Entitlement: paidEnt,
	}); !errors.Is(err, ErrAgentEscalation) {
		t.Fatalf("agent priority err=%v, want ErrAgentEscalation", err)
	}
	if _, err := AuthorizeExecutionPreference(EntitlementCheckInput{
		MemberRole: "agent", RequestedPreference: "immediate", ExistingPreference: "night", Entitlement: paidEnt,
	}); !errors.Is(err, ErrAgentEscalation) {
		t.Fatalf("agent escalation night->immediate err=%v, want ErrAgentEscalation", err)
	}
	if _, err := AuthorizeExecutionPreference(EntitlementCheckInput{
		MemberRole: "agent", RequestedPreference: "idle", ExistingPreference: "night", Entitlement: paidEnt,
	}); err != nil {
		t.Fatalf("agent downgrade night->idle should pass: %v", err)
	}
}

func TestRepeatMethodVisibilityAndPrivateWorkBoundary(t *testing.T) {
	now := time.Date(2026, 11, 27, 1, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	past := now.Add(-1 * time.Hour)

	// Valid: same campaign different store with private customer work (stays private).
	if _, err := ValidateRepeatMethod(RepeatMethodSpec{
		Mode:               RepeatModeSameCampaignDifferentStore,
		Name:               "华东门店复用",
		TemplateID:         "tpl_priv_1",
		TemplateVisibility: TemplateVisibilityPrivateCustomer,
		ParametricSlots:    map[string]string{"store_label": "南京新街口店"},
	}, now); err != nil {
		t.Fatalf("same_campaign_different_store failed: %v", err)
	}

	// Valid: same product different channel.
	if _, err := ValidateRepeatMethod(RepeatMethodSpec{
		Mode:               RepeatModeSameProductDifferentChannel,
		Name:               "冷萃多渠道横竖版",
		TemplateID:         "tpl_priv_2",
		TemplateVisibility: TemplateVisibilityPrivateCustomer,
	}, now); err != nil {
		t.Fatalf("same_product_different_channel failed: %v", err)
	}

	// Valid: cross-customer with platform template or authorized team template.
	if _, err := ValidateRepeatMethod(RepeatMethodSpec{
		Mode:               RepeatModeCrossCustomerTemplate,
		Name:               "平台品牌模板",
		TemplateID:         "tpl_platform_1",
		TemplateVisibility: TemplateVisibilityPlatform,
	}, now); err != nil {
		t.Fatalf("platform cross-customer failed: %v", err)
	}
	if _, err := ValidateRepeatMethod(RepeatMethodSpec{
		Mode:               RepeatModeCrossCustomerTemplate,
		Name:               "授权团队模板",
		TemplateID:         "tpl_team_1",
		TemplateVisibility: TemplateVisibilityAuthorizedTeam,
		TeamGrantID:        "grant_team_ok",
		GrantExpiresAt:     &future,
	}, now); err != nil {
		t.Fatalf("authorized team cross-customer failed: %v", err)
	}

	// Forbidden: private customer work can NEVER become public or cross-customer.
	for _, spec := range []RepeatMethodSpec{
		{
			Mode:               RepeatModeSameCampaignDifferentStore,
			Name:               "私作转公",
			TemplateID:         "tpl_priv_pub",
			TemplateVisibility: TemplateVisibilityPrivateCustomer,
			PublicTemplate:     true,
		},
		{
			Mode:               RepeatModeSameProductDifferentChannel,
			Name:               "自动晋升公模",
			TemplateID:         "tpl_priv_promote",
			TemplateVisibility: TemplateVisibilityPrivateCustomer,
			PromoteToPublic:    true,
		},
		{
			Mode:               RepeatModeSameCampaignDifferentStore,
			Name:               "跨租户分享私作",
			TemplateID:         "tpl_priv_cross",
			TemplateVisibility: TemplateVisibilityPrivateCustomer,
			ShareCrossTenant:   true,
		},
		{
			Mode:               RepeatModeCrossCustomerTemplate,
			Name:               "私作跨客户复用",
			TemplateID:         "tpl_priv_cross_cust",
			TemplateVisibility: TemplateVisibilityPrivateCustomer,
		},
	} {
		if _, err := ValidateRepeatMethod(spec, now); !errors.Is(err, ErrPrivateWorkCannotBecomePublic) {
			t.Fatalf("private work boundary %q got err=%v, want ErrPrivateWorkCannotBecomePublic", spec.Name, err)
		}
	}

	// Team template without grant or expired grant must fail closed.
	if _, err := ValidateRepeatMethod(RepeatMethodSpec{
		Mode:               RepeatModeCrossCustomerTemplate,
		Name:               "无授权团队模板",
		TemplateID:         "tpl_team_no_grant",
		TemplateVisibility: TemplateVisibilityAuthorizedTeam,
	}, now); !errors.Is(err, ErrRepeatMethodForbidden) {
		t.Fatalf("missing team grant got %v, want ErrRepeatMethodForbidden", err)
	}
	if _, err := ValidateRepeatMethod(RepeatMethodSpec{
		Mode:               RepeatModeCrossCustomerTemplate,
		Name:               "过期团队模板",
		TemplateID:         "tpl_team_expired",
		TemplateVisibility: TemplateVisibilityAuthorizedTeam,
		TeamGrantID:        "grant_old",
		GrantExpiresAt:     &past,
	}, now); !errors.Is(err, ErrVariantExpired) {
		t.Fatalf("expired team grant got %v, want ErrVariantExpired", err)
	}
}

func TestQueueBackflowValidationAndStratification(t *testing.T) {
	doc := Document{
		AuthorizedAssetIDs:  []string{"asset_logo_v2"},
		Channels:            []string{"抖音", "视频号"},
		BudgetAttributionID: "budget_1",
		Motion: MotionRef{
			ProjectID:  "prj_1",
			RevisionID: "rev_1",
			CampaignID: "camp_1",
			Digest:     validDigest(),
		},
	}

	// Nullable ETA (nil -> JSON null), retains authorized asset version.
	valid, err := ValidateQueueBackflow(QueueBackflow{
		IdempotencyKey:      "qkey_1",
		JobID:               "job_night_1",
		CreativeID:          "crv_1",
		CampaignID:          "camp_1",
		ProjectID:           "prj_1",
		RevisionID:          "rev_1",
		Digest:              validDigest(),
		ExecutionPreference: "night",
		Phase:               "scheduled_night",
		QueueState:          QueueStateWaitingWindow,
		WaitReason:          "night_window_scheduled",
		NextWindow:          "2026-11-28T01:00:00Z/2026-11-28T06:00:00Z",
		ETASeconds:          nil,
		PolicyVersion:       "mxops-v2026.11",
		RetainedAssetVersions: []RetainedAssetVersion{
			{AssetID: "asset_logo_v2", Version: "v2.1"},
		},
	}, "camp_1", doc)
	if err != nil {
		t.Fatalf("valid night queue backflow failed: %v", err)
	}
	if valid.ETASeconds != nil {
		t.Fatalf("expected nil ETASeconds, got %v", *valid.ETASeconds)
	}
	encoded, _ := json.Marshal(valid)
	if !strings.Contains(string(encoded), `"eta_seconds":null`) {
		t.Fatalf("nil eta_seconds must marshal to JSON null: %s", encoded)
	}

	// Stratification separates cloud artifact waiting from human follow-up and never blocks CRM.
	stCloudOnly := BuildWaitStratification(QueueStateWaitingWindow, false)
	if !stCloudOnly.CloudArtifactWaiting || stCloudOnly.HumanFollowupPending || stCloudOnly.CRMWorkBlocked || stCloudOnly.WaitingKind != WaitKindCloudArtifact {
		t.Fatalf("cloud-only stratification wrong: %+v", stCloudOnly)
	}
	stBoth := BuildWaitStratification(QueueStateQueued, true)
	if !stBoth.CloudArtifactWaiting || !stBoth.HumanFollowupPending || stBoth.CRMWorkBlocked || stBoth.WaitingKind != WaitKindBoth {
		t.Fatalf("both stratification wrong: %+v", stBoth)
	}
	stHumanOnly := BuildWaitStratification(QueueStateSucceeded, true)
	if stHumanOnly.CloudArtifactWaiting || !stHumanOnly.HumanFollowupPending || stHumanOnly.CRMWorkBlocked || stHumanOnly.WaitingKind != WaitKindHumanFollowup {
		t.Fatalf("human-only stratification wrong: %+v", stHumanOnly)
	}

	// Negative ETA rejected.
	neg := int64(-10)
	if _, err := ValidateQueueBackflow(QueueBackflow{
		IdempotencyKey: "qkey_neg", JobID: "job_1", CreativeID: "crv_1",
		ProjectID: "prj_1", RevisionID: "rev_1", Digest: validDigest(),
		Phase: "queued", QueueState: QueueStateQueued, PolicyVersion: "mxops-v1",
		ETASeconds: &neg,
	}, "camp_1", doc); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative ETA got %v, want ErrInvalid", err)
	}

	// Project mismatch refuses creating a second project.
	if _, err := ValidateQueueBackflow(QueueBackflow{
		IdempotencyKey: "qkey_p2", JobID: "job_2", CreativeID: "crv_1",
		ProjectID: "prj_second", RevisionID: "rev_1", Digest: validDigest(),
		Phase: "queued", QueueState: QueueStateQueued, PolicyVersion: "mxops-v1",
	}, "camp_1", doc); !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("second project got %v, want ErrProjectMismatch", err)
	}

	// Auto side effects rejected.
	if _, err := ValidateQueueBackflow(QueueBackflow{
		IdempotencyKey: "qkey_auto", JobID: "job_1", CreativeID: "crv_1",
		ProjectID: "prj_1", RevisionID: "rev_1", Digest: validDigest(),
		Phase: "completed", QueueState: QueueStateSucceeded, PolicyVersion: "mxops-v1",
		AutoOutreach: true,
	}, "camp_1", doc); !errors.Is(err, ErrAutoSideEffectForbidden) {
		t.Fatalf("auto_outreach got %v, want ErrAutoSideEffectForbidden", err)
	}
}

func TestRenderBackflowNonLinearGuardAndAttribution(t *testing.T) {
	doc := Document{
		Channels:            []string{"抖音", "视频号", "小红书", "快手"},
		BudgetAttributionID: "budget_q4",
		Budget:              &BudgetSpec{BudgetAttributionID: "budget_q4", MaxBudgetCents: 1000},
		Payer:               &PayerSpec{PayerKind: "organization", PayerAccountID: "acct_org_1"},
		Motion: MotionRef{
			ProjectID:  "prj_1",
			RevisionID: "rev_1",
			CampaignID: "camp_1",
			Digest:     validDigest(),
		},
	}

	acc := RenderAccountingState{ExistingReceiptIDs: map[string]string{}}

	// 1) Baseline render with 1 high-cost model call passes.
	r1, err := ValidateRenderBackflow(RenderBackflow{
		IdempotencyKey:        "rkey_1",
		CampaignID:            "camp_1",
		CreativeID:            "crv_1",
		ProjectID:             "prj_1",
		RevisionID:            "rev_1",
		Digest:                validDigest(),
		RenderID:              "rnd_1",
		VariantID:             "var_douyin",
		Channel:               "抖音",
		SourceFactsVersion:    "facts_v1",
		RenderMode:            RenderModeBaseline,
		HighCostModelCalls:    1,
		BilledGenerationCount: 1,
		CostCents:             400,
		BillingReceiptID:      "rcpt_base_1",
		PayerRef:              "acct_org_1",
		BudgetAttributionID:   "budget_q4",
	}, "camp_1", doc, acc)
	if err != nil {
		t.Fatalf("baseline render failed: %v", err)
	}
	acc.ExistingTotalHighCostCalls += r1.HighCostModelCalls
	acc.ExistingTotalCostCents += r1.CostCents
	acc.ExistingReceiptIDs[r1.BillingReceiptID] = r1.CreativeID + "|" + r1.VariantID

	// 2) Parametric variants for other channels with 0 high-cost calls pass.
	for i, ch := range []string{"视频号", "小红书", "快手"} {
		rv, err := ValidateRenderBackflow(RenderBackflow{
			IdempotencyKey:        "rkey_p_" + string(rune('a'+i)),
			CampaignID:            "camp_1",
			CreativeID:            "crv_1",
			ProjectID:             "prj_1",
			RevisionID:            "rev_1",
			Digest:                validDigest(),
			RenderID:              "rnd_p_" + string(rune('a'+i)),
			VariantID:             "var_p_" + string(rune('a'+i)),
			Channel:               ch,
			SourceFactsVersion:    "facts_v1",
			RenderMode:            RenderModeParametricVariant,
			HighCostModelCalls:    0,
			BilledGenerationCount: 0,
			CostCents:             50,
			BillingReceiptID:      "rcpt_param_" + string(rune('a'+i)),
			PayerRef:              "acct_org_1",
			BudgetAttributionID:   "budget_q4",
		}, "camp_1", doc, acc)
		if err != nil {
			t.Fatalf("parametric variant %s failed: %v", ch, err)
		}
		acc.ExistingTotalCostCents += rv.CostCents
		acc.ExistingReceiptIDs[rv.BillingReceiptID] = rv.CreativeID + "|" + rv.VariantID
	}

	// 3) Parametric variant attempting linear high-cost model call is rejected.
	if _, err := ValidateRenderBackflow(RenderBackflow{
		IdempotencyKey:        "rkey_spike",
		CampaignID:            "camp_1",
		CreativeID:            "crv_1",
		ProjectID:             "prj_1",
		RevisionID:            "rev_1",
		Digest:                validDigest(),
		RenderID:              "rnd_spike",
		VariantID:             "var_spike",
		Channel:               "视频号",
		SourceFactsVersion:    "facts_v1",
		RenderMode:            RenderModeParametricVariant,
		HighCostModelCalls:    1,
		BilledGenerationCount: 1,
		CostCents:             400,
	}, "camp_1", doc, acc); !errors.Is(err, ErrLinearModelCallSpike) {
		t.Fatalf("linear spike got %v, want ErrLinearModelCallSpike", err)
	}

	// 4) Second baseline trying to bill another high-cost model call is rejected (O(1) cap).
	if _, err := ValidateRenderBackflow(RenderBackflow{
		IdempotencyKey:        "rkey_base2",
		CampaignID:            "camp_1",
		CreativeID:            "crv_1",
		ProjectID:             "prj_1",
		RevisionID:            "rev_1",
		Digest:                validDigest(),
		RenderID:              "rnd_base2",
		VariantID:             "var_base2",
		Channel:               "快手",
		SourceFactsVersion:    "facts_v1",
		RenderMode:            RenderModeBaseline,
		HighCostModelCalls:    1,
		BilledGenerationCount: 1,
		CostCents:             200,
	}, "camp_1", doc, acc); !errors.Is(err, ErrLinearModelCallSpike) {
		t.Fatalf("second baseline high-cost call got %v, want ErrLinearModelCallSpike", err)
	}

	// 5) Duplicate billing receipt across another variant is rejected.
	if _, err := ValidateRenderBackflow(RenderBackflow{
		IdempotencyKey:      "rkey_dup_rcpt",
		CampaignID:          "camp_1",
		CreativeID:          "crv_2",
		ProjectID:           "prj_1",
		RevisionID:          "rev_1",
		Digest:              validDigest(),
		RenderID:            "rnd_dup",
		VariantID:           "var_dup",
		Channel:             "抖音",
		SourceFactsVersion:  "facts_v1",
		RenderMode:          RenderModeParametricVariant,
		CostCents:           50,
		BillingReceiptID:    "rcpt_base_1",
		PayerRef:            "acct_org_1",
		BudgetAttributionID: "budget_q4",
	}, "camp_1", doc, acc); !errors.Is(err, ErrDuplicateCharge) {
		t.Fatalf("duplicate receipt got %v, want ErrDuplicateCharge", err)
	}

	// 6) Exceeding campaign budget cap is rejected.
	if _, err := ValidateRenderBackflow(RenderBackflow{
		IdempotencyKey:      "rkey_over_budget",
		CampaignID:          "camp_1",
		CreativeID:          "crv_3",
		ProjectID:           "prj_1",
		RevisionID:          "rev_1",
		Digest:              validDigest(),
		RenderID:            "rnd_over",
		VariantID:           "var_over",
		Channel:             "抖音",
		SourceFactsVersion:  "facts_v1",
		RenderMode:          RenderModeParametricVariant,
		CostCents:           600, // 550 existing + 600 > 1000
		BillingReceiptID:    "rcpt_over",
		PayerRef:            "acct_org_1",
		BudgetAttributionID: "budget_q4",
	}, "camp_1", doc, acc); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("over budget got %v, want ErrBudgetExceeded", err)
	}

	// 7) Writing conversion effectiveness back to creative project is forbidden.
	if _, err := ValidateRenderBackflow(RenderBackflow{
		IdempotencyKey:        "rkey_wb",
		CampaignID:            "camp_1",
		CreativeID:            "crv_1",
		ProjectID:             "prj_1",
		RevisionID:            "rev_1",
		Digest:                validDigest(),
		RenderID:              "rnd_wb",
		VariantID:             "var_wb",
		Channel:               "抖音",
		SourceFactsVersion:    "facts_v1",
		WritebackCreativeFact: true,
	}, "camp_1", doc, acc); !errors.Is(err, ErrCreativeWritebackForbidden) {
		t.Fatalf("writeback got %v, want ErrCreativeWritebackForbidden", err)
	}

	// 8) Attribution summary computes nullable per-accepted-creative and per-channel costs.
	summaryZeroAccepted := ComputeAttributionSummary([]RenderMetricItem{
		{Channel: "抖音", Status: "candidate", HighCostModelCalls: 1, CostCents: 400},
		{Channel: "视频号", Status: "candidate", HighCostModelCalls: 0, CostCents: 50},
	})
	if summaryZeroAccepted.CostPerAcceptedCreativeCents != nil {
		t.Fatalf("expected nil cost_per_accepted_creative_cents when 0 accepted")
	}
	if summaryZeroAccepted.CostPerChannelVariantCents == nil || *summaryZeroAccepted.CostPerChannelVariantCents != 225 {
		t.Fatalf("cost_per_channel_variant_cents=%v, want 225", summaryZeroAccepted.CostPerChannelVariantCents)
	}
	summaryAccepted := ComputeAttributionSummary([]RenderMetricItem{
		{Channel: "抖音", Status: "accepted", HighCostModelCalls: 1, CostCents: 400},
		{Channel: "视频号", Status: "accepted", HighCostModelCalls: 0, CostCents: 50},
		{Channel: "小红书", Status: "candidate", HighCostModelCalls: 0, CostCents: 50},
	})
	if summaryAccepted.Authority != "leads_only" || summaryAccepted.CreativeProjectWrites != 0 {
		t.Fatalf("authority separation broken: %+v", summaryAccepted)
	}
	if summaryAccepted.CostPerAcceptedCreativeCents == nil || *summaryAccepted.CostPerAcceptedCreativeCents != 250 {
		t.Fatalf("cost_per_accepted_creative_cents=%v, want 250", summaryAccepted.CostPerAcceptedCreativeCents)
	}
}
