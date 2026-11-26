package lightcopy

import (
	"strings"
	"testing"
	"time"
)

func TestProjectBriefEnforcesMinimalFactsAndPIIGuard(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	validPrice := int64(29900)
	sub := Subject{Kind: KindOpportunity, ID: "opp_1", TenantID: "tA", Title: "冬季皮肤护理套餐"}

	// Happy path: minimal confirmed scene/question/offer/next_step + authorized asset.
	proj, reason := ProjectBrief(BriefInput{
		CallerTenant:   "tA",
		Subject:        sub,
		SampleCategory: SampleService,
		Facts: []ConfirmedFact{
			{Key: "scene", Value: "冬季到店护理咨询", Confirmed: true, Authorized: true},
			{Key: "question", Value: "关心到店时长与预约方式", Confirmed: true, Authorized: true},
			{Key: "offer", Value: "工作日到店体验基础补水护理", Confirmed: true, Authorized: true},
			{Key: "next_step", Value: "预约本周四下午到店", Confirmed: true, Authorized: true},
			{Key: "price_cents", PriceCents: &validPrice, ValidUntil: now.Add(24 * time.Hour).Format(time.RFC3339), Confirmed: true, Authorized: true},
		},
		AuthorizedAssets: []AuthorizedAsset{
			{AssetID: "ast_shop_01", AssetKind: "image", GrantRef: "grt_brand_01", LicenseRef: "lic_01", ExpiresAt: now.Add(48 * time.Hour).Format(time.RFC3339)},
		},
	}, now)
	if reason != "" {
		t.Fatalf("expected valid brief, got reason=%q", reason)
	}
	if len(proj.Facts) != 5 || proj.Facts["scene"] != "冬季到店护理咨询" || proj.Facts["price_cents"] != int64(29900) {
		t.Fatalf("unexpected facts: %#v", proj.Facts)
	}
	if len(proj.AuthorizedAssets) != 1 || proj.AuthorizedAssets[0].AssetID != "ast_shop_01" {
		t.Fatalf("unexpected assets: %#v", proj.AuthorizedAssets)
	}
	if proj.InputFactsHash == "" {
		t.Fatalf("expected deterministic input_facts_hash")
	}

	// Reject forbidden keys (phone_list, sales_note, contact_fulltext, crm_profile, portrait).
	for _, forbidden := range []string{"phone_list", "phones", "sales_note", "internal_note", "contact_fulltext", "crm_profile", "unauthorized_portrait"} {
		_, r := ProjectBrief(BriefInput{
			CallerTenant: "tA",
			Subject:      sub,
			Facts: []ConfirmedFact{
				{Key: "scene", Value: "正常场景", Confirmed: true, Authorized: true},
				{Key: forbidden, Value: "敏感值", Confirmed: true, Authorized: true},
			},
		}, now)
		if r != ReasonUnauthorized {
			t.Fatalf("forbidden key %q: got %q, want %q", forbidden, r, ReasonUnauthorized)
		}
	}

	// Strict mode rejects PII smuggled inside allowed fields (phone list, email, internal note).
	piiSamples := []string{
		"客户手机号列表 13800138000, 13911112222 请尽快联系",
		"发邮件给 ceo@example.com 跟进",
		"内部备注：先报价高一点再给折扣",
		"联系人全文：张三住朝阳区",
		"未授权画像：高净值女性",
	}
	for _, sample := range piiSamples {
		_, r := ProjectBrief(BriefInput{
			CallerTenant: "tA",
			Subject:      sub,
			Facts: []ConfirmedFact{
				{Key: "scene", Value: sample, Confirmed: true, Authorized: true},
			},
		}, now)
		if r != ReasonPIIDetected {
			t.Fatalf("pii sample %q: got %q, want %q", sample, r, ReasonPIIDetected)
		}
	}

	// StripPII mode strips phone numbers and internal note lines while keeping clean facts.
	stripped, r := ProjectBrief(BriefInput{
		CallerTenant: "tA",
		Subject:      sub,
		StripPII:     true,
		Facts: []ConfirmedFact{
			{Key: "scene", Value: "门店开业活动咨询 13800138000\n内部备注：先压价", Confirmed: true, Authorized: true},
		},
	}, now)
	if r != "" || !stripped.StrippedPII {
		t.Fatalf("strip mode failed: r=%q proj=%+v", r, stripped)
	}
	gotScene, _ := stripped.Facts["scene"].(string)
	if strings.Contains(gotScene, "13800138000") || strings.Contains(gotScene, "内部备注") || !strings.Contains(gotScene, "门店开业活动咨询") {
		t.Fatalf("strip result invalid: %q", gotScene)
	}

	// Unauthorized or expired assets are rejected fail-closed.
	if _, r := ProjectBrief(BriefInput{
		CallerTenant:     "tA",
		Subject:          sub,
		Facts:            []ConfirmedFact{{Key: "scene", Value: "正常场景", Confirmed: true, Authorized: true}},
		AuthorizedAssets: []AuthorizedAsset{{AssetID: "ast_1", GrantRef: ""}},
	}, now); r != ReasonAssetUnauthorized {
		t.Fatalf("missing grant_ref: got %q, want %q", r, ReasonAssetUnauthorized)
	}
	if _, r := ProjectBrief(BriefInput{
		CallerTenant:     "tA",
		Subject:          sub,
		Facts:            []ConfirmedFact{{Key: "scene", Value: "正常场景", Confirmed: true, Authorized: true}},
		AuthorizedAssets: []AuthorizedAsset{{AssetID: "ast_1", GrantRef: "grt_1", ExpiresAt: now.Add(-time.Minute).Format(time.RFC3339)}},
	}, now); r != ReasonAssetExpired {
		t.Fatalf("expired asset: got %q, want %q", r, ReasonAssetExpired)
	}
}

func TestProjectBriefMarksExpiredPriceAndMissingFactsUnknown(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	expiredPrice := int64(19900)
	sub := Subject{Kind: KindOpportunity, ID: "opp_2", TenantID: "tA", Title: "春季洁牙套餐"}

	// Strict mode (AllowUnknownMarking=false) refuses expired price.
	if _, r := ProjectBrief(BriefInput{
		CallerTenant: "tA",
		Subject:      sub,
		Facts: []ConfirmedFact{
			{Key: "scene", Value: "春季到店咨询", Confirmed: true, Authorized: true},
			{Key: "price_cents", PriceCents: &expiredPrice, ValidUntil: now.Add(-time.Hour).Format(time.RFC3339), Confirmed: true, Authorized: true},
		},
	}, now); r != ReasonPrice {
		t.Fatalf("strict expired price got %q, want %q", r, ReasonPrice)
	}

	// AllowUnknownMarking=true strips expired price from Facts and marks it unknown,
	// while marking missing/unconfirmed facts as needs_clarification.
	proj, r := ProjectBrief(BriefInput{
		CallerTenant:        "tA",
		Subject:             sub,
		SampleCategory:      SampleProduct,
		AllowUnknownMarking: true,
		Facts: []ConfirmedFact{
			{Key: "scene", Value: "春季到店咨询", Confirmed: true, Authorized: true},
			{Key: "offer", Value: "", Confirmed: true, Authorized: true},
			{Key: "next_step", Value: "待销售补充时间", Confirmed: false, Authorized: true},
			{Key: "price_cents", PriceCents: &expiredPrice, ValidUntil: now.Add(-time.Hour).Format(time.RFC3339), Confirmed: true, Authorized: true},
		},
	}, now)
	if r != "" {
		t.Fatalf("unknown marking failed: %q", r)
	}
	if _, exists := proj.Facts["price_cents"]; exists {
		t.Fatalf("expired price must never enter Facts: %#v", proj.Facts)
	}
	if len(proj.UnknownFields) != 3 {
		t.Fatalf("expected 3 unknown/clarification fields, got %v", proj.UnknownFields)
	}
	statusByKey := map[string]string{}
	for _, item := range proj.ClarificationItems {
		statusByKey[item.Key] = item.Status
	}
	if statusByKey["price_cents"] != FactStatusUnknown {
		t.Fatalf("price_cents status = %q, want unknown", statusByKey["price_cents"])
	}
	if statusByKey["offer"] != FactStatusNeedsClarification || statusByKey["next_step"] != FactStatusNeedsClarification {
		t.Fatalf("clarification statuses = %#v", statusByKey)
	}

	prompt := PromptFromBrief(KindReply, "zh", proj)
	if strings.Contains(prompt, "19900") {
		t.Fatalf("prompt leaked expired price: %q", prompt)
	}
	if !strings.Contains(prompt, "price_cents: status=unknown") || !strings.Contains(prompt, "严禁编造数字") {
		t.Fatalf("prompt missing unknown guidance: %q", prompt)
	}
}

func TestAuditThreeSampleCategories(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	validPrice := int64(39900)

	t.Run("campaign sample clean pass and omission check", func(t *testing.T) {
		proj, r := ProjectBrief(BriefInput{
			CallerTenant:   "tA",
			Subject:        Subject{Kind: KindCampaign, ID: "lead_c1", TenantID: "tA", ActivityRef: "双十二门店主题周"},
			Selected:       []string{"activity_ref"},
			SampleCategory: SampleCampaign,
			Facts: []ConfirmedFact{
				{Key: "scene", Value: "双十二门店打卡活动", Confirmed: true, Authorized: true},
				{Key: "offer", Value: "到店签到领伴手礼", Confirmed: true, Authorized: true},
				{Key: "next_step", Value: "回复预约到店时段", Confirmed: true, Authorized: true},
			},
		}, now)
		if r != "" {
			t.Fatalf("project campaign: %q", r)
		}
		good := "欢迎参加双十二门店主题周的双十二门店打卡活动，到店签到领伴手礼，欢迎回复预约到店时段。"
		rep := AuditGeneratedCopy(proj, good)
		if !rep.Passed || len(rep.OmittedFacts) != 0 || len(rep.UnsupportedClaims) != 0 || !rep.ToneOK || !rep.HasCTA {
			t.Fatalf("campaign good audit = %+v", rep)
		}

		omitted := "您好，欢迎关注门店最新动态。"
		repOmit := AuditGeneratedCopy(proj, omitted)
		if len(repOmit.OmittedFacts) < 3 {
			t.Fatalf("expected omitted facts for campaign, got %+v", repOmit)
		}
	})

	t.Run("product sample with expired price refuses invented price and placeholders", func(t *testing.T) {
		expired := int64(59900)
		proj, r := ProjectBrief(BriefInput{
			CallerTenant:        "tA",
			Subject:             Subject{Kind: KindOpportunity, ID: "opp_p1", TenantID: "tA", Title: "恒温护发吹风机"},
			Selected:            []string{"title"},
			SampleCategory:      SampleProduct,
			AllowUnknownMarking: true,
			Facts: []ConfirmedFact{
				{Key: "offer", Value: "支持门店现场试用", Confirmed: true, Authorized: true},
				{Key: "price_cents", PriceCents: &expired, ValidUntil: now.Add(-time.Hour).Format(time.RFC3339), Confirmed: true, Authorized: true},
			},
		}, now)
		if r != "" {
			t.Fatalf("project product: %q", r)
		}

		// Model invents price 299元 when price is unknown -> must block.
		invented := AuditGeneratedCopy(proj, "恒温护发吹风机现特价299元，支持门店现场试用，欢迎预约体验。")
		if invented.Passed || invented.BlockingReason != ReasonUngroundedClaim || len(invented.UnsupportedClaims) == 0 {
			t.Fatalf("invented price must fail audit: %+v", invented)
		}

		// Model uses placeholder -> must block.
		placeholder := AuditGeneratedCopy(proj, "恒温护发吹风机最新价格为[待填写]，支持门店现场试用。")
		if placeholder.Passed || placeholder.BlockingReason != ReasonPlaceholder || !placeholder.HasPlaceholder {
			t.Fatalf("placeholder must fail audit: %+v", placeholder)
		}

		// Honest unknown handling -> passes cleanly.
		honest := AuditGeneratedCopy(proj, "恒温护发吹风机支持门店现场试用，当前最新报价待核实并以门店最新确认为准，欢迎预约到店体验。")
		if !honest.Passed || !honest.UnknownAcknowledged || !honest.HasCTA {
			t.Fatalf("honest product copy should pass: %+v", honest)
		}
	})

	t.Run("service sample with valid price passes and rejects ungrounded promises or PII", func(t *testing.T) {
		proj, r := ProjectBrief(BriefInput{
			CallerTenant:   "tA",
			Subject:        Subject{Kind: KindOpportunity, ID: "opp_s1", TenantID: "tA", Title: "年度空调深度清洗服务"},
			Selected:       []string{"title"},
			SampleCategory: SampleService,
			Facts: []ConfirmedFact{
				{Key: "scene", Value: "老客户夏季前保养咨询", Confirmed: true, Authorized: true},
				{Key: "next_step", Value: "确认上门地址与预约日期", Confirmed: true, Authorized: true},
				{Key: "price_cents", PriceCents: &validPrice, ValidUntil: now.Add(48 * time.Hour).Format(time.RFC3339), Confirmed: true, Authorized: true},
			},
		}, now)
		if r != "" {
			t.Fatalf("project service: %q", r)
		}

		okRep := AuditGeneratedCopy(proj, "针对老客户夏季前保养咨询，年度空调深度清洗服务当前确认报价为399元，欢迎联系确认上门地址与预约日期。")
		if !okRep.Passed || len(okRep.OmittedFacts) != 0 || !okRep.HasCTA {
			t.Fatalf("service good copy failed: %+v", okRep)
		}

		promiseRep := AuditGeneratedCopy(proj, "年度空调深度清洗服务399元，100%保证效果且已自动发送预约确认。")
		if promiseRep.Passed || promiseRep.BlockingReason != ReasonUngroundedClaim {
			t.Fatalf("ungrounded promise must fail: %+v", promiseRep)
		}

		piiRep := AuditGeneratedCopy(proj, "年度空调深度清洗服务399元，请直接拨打13800138000联系。")
		if piiRep.Passed || piiRep.BlockingReason != ReasonPIIDetected || !piiRep.HasPIILeak {
			t.Fatalf("pii leak in output must fail: %+v", piiRep)
		}
	})
}

func TestNormalizeToolAndValidateReturnCandidate(t *testing.T) {
	for raw, want := range map[string]string{
		"goboost":       ToolGoBoost,
		"product_image": ToolProductImage,
		"product-image": ToolProductImage,
		"digital_human": ToolDigitalHuman,
		"avatar":        ToolDigitalHuman,
		"swarm":         ToolSwarm,
		"fengqun":       ToolSwarm,
		"aicut":         ToolAiCut,
	} {
		got, ok := NormalizeTool(raw)
		if !ok || got != want {
			t.Fatalf("NormalizeTool(%q) = %q, %v; want %q, true", raw, got, ok, want)
		}
	}
	for _, bad := range []string{"", "video-engine", "in-crm-nle", "unknown"} {
		if _, ok := NormalizeTool(bad); ok {
			t.Fatalf("NormalizeTool(%q) must fail", bad)
		}
	}

	clean, r := ValidateReturnCandidate(ReturnCandidateInput{
		IdempotencyKey: "ret_1",
		AssetRef:       "ast_gb_1001",
		TaskRef:        "tsk_gb_9001",
		CandidateKind:  "video_ref",
		Summary:        "GoBoost 成品候选片段，待人工审核确认",
		ContentVersion: 2,
	})
	if r != "" || clean.AssetRef != "ast_gb_1001" || clean.TaskRef != "tsk_gb_9001" {
		t.Fatalf("valid return candidate failed: %+v %q", clean, r)
	}
	if _, r := ValidateReturnCandidate(ReturnCandidateInput{IdempotencyKey: "ret_2", Summary: "联系 13800138000", AssetRef: "ast_1"}); r != ReasonPIIDetected {
		t.Fatalf("pii summary got %q, want %q", r, ReasonPIIDetected)
	}
	if _, r := ValidateReturnCandidate(ReturnCandidateInput{IdempotencyKey: "ret_3", AssetRef: "", TaskRef: ""}); r != ReasonInvalidReturn {
		t.Fatalf("missing refs got %q, want %q", r, ReasonInvalidReturn)
	}
}
