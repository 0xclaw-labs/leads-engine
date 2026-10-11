package lightcopy

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateWithoutModelLeavesDraftUnsent(t *testing.T) {
	before := Draft{Body: "手改过的回复", Origin: "manual", ContentVersion: 2}
	got, reason := Generate(before, ModelOutput{})
	if reason != ReasonModelUnavailable {
		t.Fatalf("reason = %q, want %s", reason, ReasonModelUnavailable)
	}
	if got.Body != before.Body || got.Generated || got.Sent || got.Published || got.MarketingPermitted {
		t.Fatalf("generate mutated or marked the draft done: %+v", got)
	}
	if got.ContentVersion != before.ContentVersion {
		t.Fatalf("version changed without a model: %d", got.ContentVersion)
	}
}

func TestGenerateRejectsTemplatePlaceholder(t *testing.T) {
	_, reason := Generate(Draft{}, ModelOutput{FromLiveModel: true, Text: "您好{{name}}，这是模板占位"})
	if reason != ReasonPlaceholder {
		t.Fatalf("reason = %q, want %s", reason, ReasonPlaceholder)
	}
}

// HUI-1682: when a real platform-task generation returns text, the draft must
// carry the model provenance and the real recorded charge, stay unsent, and
// require confirmation again. A manual edit resets provenance; confirming
// must not erase it.
func TestGenerateWithLiveModelMarksProvenanceAndCharge(t *testing.T) {
	before := Draft{Body: "旧正文", Origin: OriginManual, ContentVersion: 2, UserConfirmed: true}
	got, reason := Generate(before, ModelOutput{FromLiveModel: true, Text: "  根据门店活动写好的正文  ", CostCents: 1})
	if reason != "" {
		t.Fatalf("reason = %q", reason)
	}
	if got.Body != "根据门店活动写好的正文" {
		t.Fatalf("body = %q", got.Body)
	}
	if !got.Generated || got.Origin != OriginModel || got.LiveCharge != 1 {
		t.Fatalf("provenance = %+v", got)
	}
	if got.ContentVersion != 3 {
		t.Fatalf("version = %d", got.ContentVersion)
	}
	if got.UserConfirmed || got.Sent || got.Published || got.MarketingPermitted {
		t.Fatalf("generated draft must stay unconfirmed and unsent: %+v", got)
	}

	confirmed := Confirm(got)
	if !confirmed.UserConfirmed || !confirmed.Generated || confirmed.Origin != OriginModel || confirmed.LiveCharge != 1 {
		t.Fatalf("confirm erased model provenance or charge: %+v", confirmed)
	}
	if confirmed.Sent || confirmed.Published || confirmed.MarketingPermitted {
		t.Fatalf("confirm must not send or publish: %+v", confirmed)
	}

	edited, r := SaveManual(confirmed, "人工改稿覆盖模型正文")
	if r != "" || edited.Body != "人工改稿覆盖模型正文" {
		t.Fatalf("edit = %+v %q", edited, r)
	}
	if edited.Generated || edited.Origin != OriginManual || edited.LiveCharge != 0 || edited.UserConfirmed {
		t.Fatalf("manual edit must reset model provenance: %+v", edited)
	}
	if edited.ContentVersion != 4 {
		t.Fatalf("edit version = %d", edited.ContentVersion)
	}
}

// The prompt handed to the platform task carries only allow-listed facts, in a
// deterministic order, and never invents numbers on an empty packet.
func TestPromptCarriesOnlyAllowListedFacts(t *testing.T) {
	packet := Packet{SubjectKind: KindOpportunity, SubjectID: "opp_1", Facts: map[string]any{
		"title": "门店开业", "price_cents": int64(12800),
	}}
	p := Prompt(KindReply, "zh", packet)
	if !strings.Contains(p, "门店开业") || !strings.Contains(p, "12800") {
		t.Fatalf("prompt dropped allowed facts: %q", p)
	}
	if !strings.Contains(p, "回复") {
		t.Fatalf("prompt missing kind label: %q", p)
	}
	again := Prompt(KindReply, "zh", packet)
	if again != p {
		t.Fatalf("prompt is not deterministic:\n%q\n%q", p, again)
	}
	empty := Prompt(KindEmail, "zh", Packet{SubjectKind: KindOpportunity, SubjectID: "opp_1", Facts: map[string]any{}})
	if strings.Contains(empty, "12800") || strings.Contains(empty, "{{") {
		t.Fatalf("empty packet invented facts: %q", empty)
	}
	if !strings.Contains(empty, "邮件") {
		t.Fatalf("empty prompt missing kind label: %q", empty)
	}
}

func TestManualSaveAndEditStayUnsentUntilConfirmAndConfirmDoesNotSend(t *testing.T) {
	saved, reason := SaveManual(Draft{}, "  回复草稿  ")
	if reason != "" || saved.Body != "回复草稿" || saved.Origin != "manual" || saved.UserConfirmed || saved.Sent {
		t.Fatalf("save: %+v %q", saved, reason)
	}
	edited, reason := SaveManual(saved, "手改后的邮件")
	if reason != "" || edited.Body != "手改后的邮件" || edited.ContentVersion != 2 || edited.UserConfirmed {
		t.Fatalf("edit: %+v %q", edited, reason)
	}
	confirmed := Confirm(edited)
	if !confirmed.UserConfirmed || confirmed.Sent || confirmed.Published || confirmed.MarketingPermitted || confirmed.Generated {
		t.Fatalf("confirm sent or published: %+v", confirmed)
	}
	if confirmed.LiveCharge != 0 {
		t.Fatalf("live charge = %d", confirmed.LiveCharge)
	}
}

func TestProjectPassesOnlySelectedFacts(t *testing.T) {
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	price := int64(12800)
	sub := Subject{
		Kind: KindOpportunity, ID: "opp_1", TenantID: "tA", Title: "门店开业",
		PriceCents: &price, PriceValidUntil: now.Add(time.Hour),
	}
	packet, reason := Project("tA", sub, []string{"title", "price_cents"}, now)
	if reason != "" {
		t.Fatalf("reason = %q", reason)
	}
	if packet.Facts["title"] != "门店开业" || packet.Facts["price_cents"] != int64(12800) {
		t.Fatalf("facts = %#v", packet.Facts)
	}
	if len(packet.Facts) != 2 {
		t.Fatalf("extra facts: %#v", packet.Facts)
	}
}

func TestProjectRejectsTenantPriceAndUnauthorizedFields(t *testing.T) {
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	price := int64(100)
	sub := Subject{
		Kind: KindOpportunity, ID: "opp_1", TenantID: "tA", Title: "门店开业",
		PriceCents: &price, PriceValidUntil: now.Add(-time.Minute),
		ContactText: "张三 13800000000 全量联系人", SalesNote: "内部：先压价", Profile: "未授权画像",
	}
	if _, reason := Project("tB", sub, []string{"title"}, now); reason != ReasonTenant {
		t.Fatalf("tenant reason = %q", reason)
	}
	packet, reason := Project("tA", sub, []string{"price_cents"}, now)
	if reason != ReasonPrice || len(packet.Facts) != 0 {
		t.Fatalf("expired price: %q %#v", reason, packet.Facts)
	}
	packet, reason = Project("tA", sub, []string{"title", "contact_text"}, now)
	if reason != ReasonUnauthorized || len(packet.Facts) != 0 {
		t.Fatalf("contact text leaked: %q %#v", reason, packet.Facts)
	}
	for _, key := range []string{"sales_note", "profile", "auth_scope_snapshot"} {
		if _, reason := Project("tA", sub, []string{key}, now); reason != ReasonUnauthorized {
			t.Fatalf("%s reason = %q", key, reason)
		}
	}
	bare := Subject{Kind: KindOpportunity, ID: "opp_1", TenantID: "tA", Title: "门店开业"}
	if _, reason := Project("tA", bare, []string{"price_cents"}, now); reason != ReasonPrice {
		t.Fatalf("unproven price reason = %q", reason)
	}
}

func TestCampaignFactOmitsContact(t *testing.T) {
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	sub := Subject{
		Kind: KindCampaign, ID: "lead_1", TenantID: "tA", ActivityRef: "camp-9",
		ContactText: "不应出现的联系人全文",
	}
	packet, reason := Project("tA", sub, []string{"activity_ref"}, now)
	if reason != "" || packet.Facts["activity_ref"] != "camp-9" || len(packet.Facts) != 1 {
		t.Fatalf("packet = %#v %q", packet.Facts, reason)
	}
}

func TestHandoffReplayDoesNotCreateOrRegenerate(t *testing.T) {
	facts := map[string]any{"title": "门店开业"}
	first := OpenHandoff(HandoffInput{
		Key: "k1", Tool: ToolGoBoost, SourceKind: KindOpportunity, SourceID: "opp_1",
		DraftID: "d1", ContentVersion: 3, Facts: facts,
	})
	if first.Status != StatusFailed || first.Reason != ReasonToolEntry || first.ProjectCreated || first.Regenerated {
		t.Fatalf("first = %+v", first)
	}
	if first.SourceID != "opp_1" || first.ContentVersion != 3 {
		t.Fatalf("source dropped: %+v", first)
	}
	again := OpenHandoff(HandoffInput{
		Key: "k1", Tool: ToolGoBoost, SourceKind: KindOpportunity, SourceID: "opp_1",
		DraftID: "d1", ContentVersion: 3, Facts: facts, Existing: &first,
	})
	if again.ID != first.ID || again.Regenerated || again.ProjectCreated || again.Status != StatusFailed {
		t.Fatalf("replay = %+v", again)
	}
	opened := OpenHandoff(HandoffInput{
		Key: "k2", Tool: ToolAiCut, EntryConfigured: true,
		SourceKind: KindCampaign, SourceID: "lead_1", DraftID: "d2", ContentVersion: 1,
		Facts: map[string]any{"activity_ref": "camp-9"},
	})
	if opened.Status != StatusRecorded || opened.ProjectCreated || opened.Reason != ReasonOpenExisting {
		t.Fatalf("configured entry created a project: %+v", opened)
	}
}

func TestDecliningAdvancedToolDoesNotBlockManualDraft(t *testing.T) {
	declined := OpenHandoff(HandoffInput{Key: "k", Tool: ToolProductImage, Decline: true, SourceKind: KindOpportunity, SourceID: "opp_1"})
	if declined.Status != StatusDeclined || declined.ProjectCreated || declined.Regenerated {
		t.Fatalf("decline = %+v", declined)
	}
	saved, reason := SaveManual(Draft{}, "拒绝专业工具后仍保存")
	if reason != "" || saved.Body == "" || saved.Sent {
		t.Fatalf("save after decline: %+v %q", saved, reason)
	}
}

func TestUnknownToolIsRefused(t *testing.T) {
	got := OpenHandoff(HandoffInput{Key: "k", Tool: "video-engine", SourceKind: KindOpportunity, SourceID: "opp_1", DraftID: "d"})
	if got.Status != StatusRefused || got.Reason != ReasonUnknownTool || got.ProjectCreated {
		t.Fatalf("unknown tool = %+v", got)
	}
}
