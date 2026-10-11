package campaignmotion

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Golden vectors, generated once against the REAL motionref sdk v0.4.0
// (tag sdk/go/v0.4.0 = 6065fba6, PR #236 consumer-link). Generation commands:
//
//	cd /Volumes/MobileHD/Work/派诺生态/public-ai && git archive --format=tar sdk/go/v0.4.0 -- sdk/go | tar -x --strip-components=2 -C /tmp/ag06-ap2-scratch/sdkgo
//	cd /tmp/ag06-ap2-scratch/gen && GOWORK=off go run .
//
// with gen/main.go building the same Ref as goldenSpec below through
// motionref.SealRef + motionref.BuildConsumerHandoffLink. leads cannot import
// the private sdk module (public repo, hosted CI, no cross-repo credential),
// so these bytes pin the v0.4.0 contract. UNSUPPORTED escape: once leads gains
// an authorized read secret, switch to a module-direct import and rerun this
// comparison.
var (
	goldenVariantID   = "var_a3acc42b14f7a78c"
	goldenContextRef  = "leads/campaigns/camp_1/motion-handoffs#3dae9001"
	goldenDigest      = "sha256:a77f4f31e6c8e710d3bea9ed8fb5bd23af200749225c7383b809921f06259bdc"
	goldenCanonical   = `{"project_id":"proj_campaign_1","revision_id":"rev_3","variant_id":"var_a3acc42b14f7a78c","owner":{"tenant_id":"tnt_live_a","account_id":"acct_motion_1"},"origin_app":"leads","origin_context_ref":"leads/campaigns/camp_1/motion-handoffs#3dae9001","permissions":{"can_view_render":true,"can_use_render":false,"can_open_project":true,"can_parameter_edit":true,"can_creative_edit":false,"can_export_project":false,"can_pay":false},"editable_level":"structured","allowed_actions":["open_project","param:media.asset_1","parameter_edit","view_render"],"version":1,"expires_at":"2027-01-01T00:00:00Z"}`
	goldenLinkBase    = "https://motion.example.com/p/proj_campaign_1/canvases/rev_3"
	goldenLinkPartial = "?motionRef=%7B%22project_id%22%3A%22proj_campaign_1%22%2C%22revision_id%22%3A%22rev_3%22%2C%22digest%22%3A%22sha256%3Aa77f4f31e6c8e710d3bea9ed8fb5bd23af200749225c7383b809921f06259bdc%22%2C%22origin_app%22%3A%22leads%22%2C%22origin_context_ref%22%3A%22leads%2Fcampaigns%2Fcamp_1%2Fmotion-handoffs%233dae9001%22%7D"
)

var goldenExpires = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

func goldenSpec() VariantSpec {
	return VariantSpec{
		CampaignID:      "camp_1",
		TenantID:        "tnt_live_a",
		OwnerAccountID:  "acct_motion_1",
		OwnerTenantID:   "tnt_live_a",
		ProjectID:       "proj_campaign_1",
		RevisionID:      "rev_3",
		Channels:        []string{"抖音信息流"},
		AuthorizedAsset: []string{"asset_1"},
		ExpiresAt:       &goldenExpires,
		Now:             goldenExpires.Add(-time.Hour),
	}
}

// The campaign variant shape reproduces the sdk v0.4.0 wire byte for byte:
// variant id derivation, canonical bytes, digest, and the consumer link.
func TestVariantRefMatchesSDKGoldenBytes(t *testing.T) {
	refs, err := BuildVariants(goldenSpec())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %d, want 1", len(refs))
	}
	ref := refs[0]
	if ref.VariantID != goldenVariantID {
		t.Fatalf("variant_id = %s, want %s", ref.VariantID, goldenVariantID)
	}
	if ref.OriginContextRef != goldenContextRef {
		t.Fatalf("context ref = %s, want %s", ref.OriginContextRef, goldenContextRef)
	}
	if got := string(CanonicalVariantRefBytes(ref.Ref)); got != goldenCanonical {
		t.Fatalf("canonical bytes drift:\n got %s\nwant %s", got, goldenCanonical)
	}
	if ref.Ref.Digest != goldenDigest {
		t.Fatalf("digest = %s, want %s", ref.Ref.Digest, goldenDigest)
	}
	link, err := ConsumerHandoffLink(goldenLinkBase, ref.Ref)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if link != goldenLinkBase+goldenLinkPartial {
		t.Fatalf("link drift:\n got %s\nwant %s", link, goldenLinkBase+goldenLinkPartial)
	}
}

// Digest and content are bound: a mutated digest cannot go out as a link.
func TestVariantConsumerLinkRefusesUnboundDigest(t *testing.T) {
	refs, err := BuildVariants(goldenSpec())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	lying := refs[0].Ref
	lying.Digest = "sha256:" + strings.Repeat("0", 64)
	if _, err := ConsumerHandoffLink(goldenLinkBase, lying); err == nil {
		t.Fatal("unbound digest produced a link")
	}
}

func TestVariantBuildRejections(t *testing.T) {
	spec := goldenSpec()
	spec.OwnerAccountID = ""
	if _, err := BuildVariants(spec); err != ErrNoMotionReceiver {
		t.Fatalf("no receiver = %v, want ErrNoMotionReceiver", err)
	}

	spec = goldenSpec()
	spec.OwnerTenantID = "tnt_other"
	if _, err := BuildVariants(spec); err != ErrForeignTenant {
		t.Fatalf("foreign tenant = %v, want ErrForeignTenant", err)
	}

	spec = goldenSpec()
	spec.ProjectID = ""
	if _, err := BuildVariants(spec); err != ErrMotion {
		t.Fatalf("no project = %v, want ErrMotion (never fabricate)", err)
	}

	spec = goldenSpec()
	spec.RevisionID = ""
	if _, err := BuildVariants(spec); err != ErrMotion {
		t.Fatalf("no revision = %v, want ErrMotion", err)
	}

	spec = goldenSpec()
	spec.Now = goldenExpires
	if _, err := BuildVariants(spec); err != ErrVariantExpired {
		t.Fatalf("expires_at == now = %v, want ErrVariantExpired", err)
	}

	spec = goldenSpec()
	spec.Now = goldenExpires.Add(time.Minute)
	if _, err := BuildVariants(spec); err != ErrVariantExpired {
		t.Fatalf("expired = %v, want ErrVariantExpired", err)
	}
}

// The copied permission matrix refuses every shape our construction path
// forbids, matching sdk v0.4.0 R0/R3/R4/R8/R9 on the constructor's domain.
func TestVariantPermissionMatrixRefusals(t *testing.T) {
	base := VariantRef{
		ProjectID: "p", RevisionID: "r", VariantID: "v",
		OwnerTenantID: "t", OwnerAccountID: "a",
		OriginApp: "leads", OriginContextRef: "leads/campaigns/c/motion-handoffs#ab12cd34",
		EditableLevel: "structured",
		Version:       1,
		Digest:        "sha256:" + strings.Repeat("a", 64),
	}
	validBits := Permissions{CanViewRender: true, CanOpenProject: true, CanParameterEdit: true}
	validActs := []string{"view_render", "open_project", "parameter_edit", "param:media.asset_1"}

	mk := func(bits Permissions, acts []string, level string) VariantRef {
		r := base
		r.Permissions = bits
		r.AllowedActions = acts
		r.EditableLevel = level
		return r
	}

	cases := []struct {
		name string
		ref  VariantRef
	}{
		{"R0 view missing", mk(Permissions{CanOpenProject: true, CanParameterEdit: true}, validActs, "structured")},
		{"R3 flattened with open", mk(Permissions{CanViewRender: true, CanOpenProject: true}, []string{"view_render", "open_project"}, "flattened")},
		{"R3 flattened with param", mk(Permissions{CanViewRender: true, CanParameterEdit: true}, []string{"view_render", "parameter_edit"}, "flattened")},
		{"R4 param without open", mk(Permissions{CanViewRender: true, CanParameterEdit: true}, validActs, "structured")},
		{"R4 param on flattened", mk(Permissions{CanViewRender: true, CanOpenProject: true, CanParameterEdit: true}, validActs, "flattened")},
		{"R8 unknown action", mk(validBits, []string{"view_render", "render_video"}, "structured")},
		{"R8 duplicate action", mk(validBits, []string{"view_render", "view_render"}, "structured")},
		{"R8 malformed param", mk(validBits, []string{"view_render", "param:"}, "structured")},
		{"R8 param path charset", mk(validBits, []string{"view_render", "param:media/asset 1"}, "structured")},
		{"R9 param without bit", mk(Permissions{CanViewRender: true, CanOpenProject: true}, validActs, "structured")},
		{"R9 param without action", mk(validBits, []string{"view_render", "open_project", "param:media.asset_1"}, "structured")},
		{"R9 vocab without bit", mk(Permissions{CanViewRender: true}, []string{"view_render", "open_project"}, "structured")},
		{"R8 empty actions", mk(validBits, nil, "structured")},
	}
	for _, tc := range cases {
		if err := validateVariantRef(tc.ref); err == nil {
			t.Fatalf("%s: accepted", tc.name)
		}
	}

	if err := validateVariantRef(mk(validBits, validActs, "structured")); err != nil {
		t.Fatalf("valid ref refused: %v", err)
	}
}

func TestVariantConsumerLinkBaseRules(t *testing.T) {
	refs, err := BuildVariants(goldenSpec())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ref := refs[0].Ref
	for _, base := range []string{
		"http://motion.example.com/x",         // http off loopback
		"https://motion.example.com/p?x=1",    // query
		"https://motion.example.com/p#frag",   // fragment
		"https://u:pass@motion.example.com/p", // userinfo
		"ftp://motion.example.com/p",          // scheme
	} {
		if _, err := ConsumerHandoffLink(base, ref); err == nil {
			t.Fatalf("base %q accepted", base)
		}
	}
	if _, err := ConsumerHandoffLink("http://127.0.0.1:29330/panel", ref); err != nil {
		t.Fatalf("http loopback refused: %v", err)
	}
}

// Prepare grows the per-channel variant facts only when a binding is given,
// and the export whitelist accepts the incremental keys. Zero creative writes.
func TestPrepareWithVariantBinding(t *testing.T) {
	port := &CountingWriter{}
	body := `{
		"campaign_goal":"三月大促预热",
		"budget_attribution_id":"budget_1",
		"channels":["抖音信息流","快手落地页"],
		"authorized_asset_ids":["asset_1"],
		"motion":{"project_id":"proj_campaign_1","revision_id":"rev_3","digest":"sha256:` + strings.Repeat("b", 64) + `"},
		"variant_binding":{"owner_account_id":"acct_motion_1","owner_tenant_id":"tnt_live_a","expires_at":"2027-01-01T00:00:00Z"}
	}`
	res, err := Prepare("tnt_live_a", "camp_1", []byte(body), port)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(res.Document.Variants) != 2 {
		t.Fatalf("variants = %d, want 2", len(res.Document.Variants))
	}
	seen := map[string]bool{}
	for _, v := range res.Document.Variants {
		if !strings.HasPrefix(v.VariantID, "var_") || len(v.VariantID) != len("var_")+16 {
			t.Fatalf("variant id %q", v.VariantID)
		}
		if !strings.HasPrefix(v.Digest, "sha256:") {
			t.Fatalf("variant digest %q", v.Digest)
		}
		if !strings.HasPrefix(v.OriginContextRef, "leads/campaigns/camp_1/motion-handoffs#") {
			t.Fatalf("context ref %q", v.OriginContextRef)
		}
		seen[v.Channel] = true
	}
	if !seen["抖音信息流"] || !seen["快手落地页"] {
		t.Fatalf("channels missing: %v", res.Document.Variants)
	}
	var export map[string]any
	if err := json.Unmarshal(res.Export, &export); err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := port.AssertZero(); err != nil {
		t.Fatalf("creative writes: %v", err)
	}

	// Distinct campaigns or channels derive distinct variant ids.
	res2, err := Prepare("tnt_live_a", "camp_2", []byte(body), port)
	if err != nil {
		t.Fatalf("prepare 2: %v", err)
	}
	if res2.Document.Variants[0].VariantID == res.Document.Variants[0].VariantID {
		t.Fatal("variant id collided across campaigns")
	}
	if err := port.AssertZero(); err != nil {
		t.Fatalf("creative writes after second: %v", err)
	}
}

// Without a binding the #55 shape is kept: channel names only, no ids.
func TestPrepareWithoutVariantBindingKeepsPriorShape(t *testing.T) {
	port := &CountingWriter{}
	body := `{
		"campaign_goal":"三月大促预热",
		"budget_attribution_id":"budget_1",
		"channels":["抖音信息流"],
		"authorized_asset_ids":["asset_1"],
		"motion":{"project_id":"proj_campaign_1","revision_id":"rev_3","digest":"sha256:` + strings.Repeat("b", 64) + `"}
	}`
	res, err := Prepare("tnt_live_a", "camp_1", []byte(body), port)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for _, v := range res.Document.Variants {
		if v.VariantID != "" || v.Digest != "" || v.OriginContextRef != "" {
			t.Fatalf("unbound variant grew ids: %+v", v)
		}
	}
	if !bytes.Contains(res.Export, []byte(`"variants":[{"channel":"抖音信息流"}]`)) {
		t.Fatalf("export shape changed: %s", res.Export)
	}
}

// The binding rejections surface through Prepare with their own errors.
func TestPrepareVariantBindingRejections(t *testing.T) {
	motion := `"motion":{"project_id":"proj_campaign_1","revision_id":"rev_3","digest":"sha256:` + strings.Repeat("b", 64) + `"}`
	cases := []struct {
		name string
		bind string
		want error
	}{
		{"no receiver", `"variant_binding":{"owner_tenant_id":"tnt_live_a"}`, ErrNoMotionReceiver},
		{"foreign tenant", `"variant_binding":{"owner_account_id":"acct_motion_1","owner_tenant_id":"tnt_other"}`, ErrForeignTenant},
		{"expired", `"variant_binding":{"owner_account_id":"acct_motion_1","expires_at":"2020-01-01T00:00:00Z"}`, ErrVariantExpired},
	}
	for _, tc := range cases {
		body := `{"campaign_goal":"预热","budget_attribution_id":"budget_1","channels":["抖音信息流"],` + motion + `,` + tc.bind + `}`
		if _, err := Prepare("tnt_live_a", "camp_1", []byte(body), &CountingWriter{}); err != tc.want {
			t.Fatalf("%s = %v, want %v", tc.name, err, tc.want)
		}
	}
}
