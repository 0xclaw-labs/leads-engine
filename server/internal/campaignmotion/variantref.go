// variantref.go — HUI-2747 变体源端形状，按 motionref sdk v0.4.0
// （tag sdk/go/v0.4.0 = 6065fba6，PR #236 consumer-link）契约钉死。
//
// leads 是公仓、hosted CI 无跨仓凭据，所以这里手写 v0.4.0 的 wire 子集：
// Ref 线材（13 键+ExpiresAt）、构造路径用到的 R0/R3/R4/R8/R9 权限矩阵子集、
// CanonicalRefBytes（spec 3.1.2 手写 canonical）、ComputeDigest（域
// "motionref.ref.v1\n"）、五存储键 consumer link（https 恒可/http 仅回环、
// base 无 query/fragment/userinfo、EncodeReturnToken 同字母表）。
// 字节一致由 variantref_test.go 的 golden vectors 钉死（真实 sdk 生成，
// 生成命令入测试注释）。UNSUPPORTED 解除条件：leads 获授权配置跨仓只读
// secret 后切换为模块直引并复跑字节一致性测试。
//
// campaign 语义：每渠道一个变体 Ref（variant_id=var_+sha256(campaign\x1fchannel)
// 前 16 hex）；owner.tenant_id=会话租户、owner.account_id=调用方供给（缺→
// ErrNoMotionReceiver，不伪造收件方）；origin_app=leads；
// origin_context_ref=leads/campaigns/<id>/motion-handoffs#<hash8>（返回目标）；
// 权限=参数改复用旧媒体的最小诚实矩阵：view+open+parameter_edit、
// editable_level=structured、creative/export/pay 恒 false（原生编辑归 AG09）；
// param 路径声明=授权素材复用槽（media.asset_<n>，值域=authorized_asset_ids），
// 复用映射留在 Leads 侧，不伪造新媒体。
//
// 诚实边界：本包是纯 wire 组装——出形状不授权、不证明原工程存在、不渲染、
// 不生成成片。消费方到站后能否使用由原 owner 路由独立裁定。
package campaignmotion

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	// ErrNoMotionReceiver means no motion account was supplied to receive the
	// variant. The shape refuses instead of inventing a receiver.
	ErrNoMotionReceiver = errors.New("campaignmotion: no motion receiver account")
	// ErrForeignTenant means the binding names another tenant's motion run.
	ErrForeignTenant = errors.New("campaignmotion: variant binding is a foreign-tenant run")
	// ErrVariantExpired means the binding's expires_at has passed (== now counts).
	ErrVariantExpired = errors.New("campaignmotion: variant binding expired")
)

// variantRefDigestDomain is the sdk v0.4.0 digest domain, byte-exact.
const variantRefDigestDomain = "motionref.ref.v1\n"

const (
	variantEditableStructured = "structured"
	variantActionView         = "view_render"
	variantActionOpen         = "open_project"
	variantActionParameter    = "parameter_edit"
	variantParamPrefix        = "param:"
	variantMaxParamActions    = 64
	variantMaxAllowedActions  = 7
	variantMaxIDBytes         = 128
	variantMaxAppBytes        = 64
	variantMaxRefStringBytes  = 256
)

var variantIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
var variantAppRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
var variantDigestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Permissions are the seven explicit bits; the wire form spells out all seven.
type Permissions struct {
	CanViewRender    bool
	CanUseRender     bool
	CanOpenProject   bool
	CanParameterEdit bool
	CanCreativeEdit  bool
	CanExportProject bool
	CanPay           bool
}

func (p Permissions) has(action string) bool {
	switch action {
	case variantActionView:
		return p.CanViewRender
	case "use_render":
		return p.CanUseRender
	case variantActionOpen:
		return p.CanOpenProject
	case variantActionParameter:
		return p.CanParameterEdit
	case "creative_edit":
		return p.CanCreativeEdit
	case "export_project":
		return p.CanExportProject
	case "pay":
		return p.CanPay
	}
	return false
}

// VariantRef is the sdk v0.4.0 Ref wire (13 keys + ExpiresAt) as this package
// issues it. It is a claim, not a credential.
type VariantRef struct {
	ProjectID        string
	RevisionID       string
	VariantID        string
	RenderID         string
	OwnerTenantID    string
	OwnerAccountID   string
	OriginApp        string
	OriginContextRef string
	Permissions      Permissions
	EditableLevel    string
	AllowedActions   []string
	Digest           string
	Version          int
	ExpiresAt        *time.Time
}

// VariantSpec is the campaign-side input for one channel sweep.
type VariantSpec struct {
	CampaignID      string
	TenantID        string
	OwnerAccountID  string
	OwnerTenantID   string
	ProjectID       string
	RevisionID      string
	Channels        []string
	AuthorizedAsset []string
	ExpiresAt       *time.Time
	Now             time.Time
}

// VariantOut is one built variant: the channel plus the facts the export carries.
type VariantOut struct {
	Channel          string
	VariantID        string
	Ref              VariantRef
	Digest           string
	OriginContextRef string
}

// BuildVariants derives one sealed variant ref per channel. A missing motion
// receiver, a foreign-tenant run, or an expired binding is refused — never
// papered over.
func BuildVariants(spec VariantSpec) ([]VariantOut, error) {
	if !tokenRe.MatchString(strings.TrimSpace(spec.CampaignID)) {
		return nil, ErrInvalid
	}
	if spec.OwnerAccountID == "" || !tokenRe.MatchString(spec.OwnerAccountID) {
		return nil, ErrNoMotionReceiver
	}
	ownerTenant := spec.TenantID
	if spec.OwnerTenantID != "" {
		if spec.OwnerTenantID != spec.TenantID {
			return nil, ErrForeignTenant
		}
		ownerTenant = spec.OwnerTenantID
	}
	if spec.ProjectID == "" || spec.RevisionID == "" ||
		!tokenRe.MatchString(spec.ProjectID) || !tokenRe.MatchString(spec.RevisionID) {
		return nil, ErrMotion
	}
	if spec.ExpiresAt != nil && !spec.Now.Before(*spec.ExpiresAt) {
		return nil, ErrVariantExpired
	}
	if len(spec.AuthorizedAsset) > variantMaxParamActions {
		return nil, ErrInvalid
	}
	ctxBase := "leads/campaigns/" + spec.CampaignID + "/motion-handoffs"
	baseHash := sha256.Sum256([]byte(ctxBase))
	out := make([]VariantOut, 0, len(spec.Channels))
	for _, channel := range spec.Channels {
		vs := sha256.Sum256([]byte(spec.CampaignID + "\x1f" + channel))
		variantID := "var_" + hex.EncodeToString(vs[:])[:16]
		ref := VariantRef{
			ProjectID:      spec.ProjectID,
			RevisionID:     spec.RevisionID,
			VariantID:      variantID,
			OwnerTenantID:  ownerTenant,
			OwnerAccountID: spec.OwnerAccountID,
			OriginApp:      "leads",
			// The return anchor points back at this campaign's handoff record.
			OriginContextRef: ctxBase + "#" + hex.EncodeToString(baseHash[:])[:8],
			Permissions: Permissions{
				CanViewRender:    true,
				CanOpenProject:   true,
				CanParameterEdit: true,
			},
			EditableLevel:  variantEditableStructured,
			AllowedActions: variantActions(spec.AuthorizedAsset),
			Version:        1,
			ExpiresAt:      spec.ExpiresAt,
		}
		// Seal first, then validate the sealed wire: the sdk v0.4.0 matrix
		// requires a well-formed digest on the wire, and the binding check
		// refuses a digest that does not match the content.
		ref.Digest = computeVariantRefDigest(ref)
		if err := validateVariantRef(ref); err != nil {
			return nil, err
		}
		if err := verifyVariantRefDigest(ref); err != nil {
			return nil, err
		}
		out = append(out, VariantOut{
			Channel:          channel,
			VariantID:        variantID,
			Ref:              ref,
			Digest:           ref.Digest,
			OriginContextRef: ref.OriginContextRef,
		})
	}
	if len(out) == 0 {
		return nil, ErrChannel
	}
	return out, nil
}

// variantActions is the minimal honest matrix for reusing old media: view,
// open, parameter edit, plus one reuse slot per authorized asset.
func variantActions(authorizedAssets []string) []string {
	acts := []string{variantActionView, variantActionOpen, variantActionParameter}
	for i := range authorizedAssets {
		acts = append(acts, variantParamPrefix+"media.asset_"+strconv.Itoa(i+1))
	}
	return acts
}

func variantCheckID(name, s string) error {
	if s == "" || len(s) > variantMaxIDBytes || !variantIDRe.MatchString(s) {
		return ErrInvalid
	}
	_ = name
	return nil
}

// validateVariantRef applies the constructor's R0/R3/R4/R8/R9 subset with the
// sdk v0.4.0 semantics: format gates, then the permission matrix, then the
// action vocabulary/budget, then the appendix A-3 three-part param gate.
func validateVariantRef(r VariantRef) error {
	if err := variantCheckID("project_id", r.ProjectID); err != nil {
		return err
	}
	if err := variantCheckID("revision_id", r.RevisionID); err != nil {
		return err
	}
	if r.VariantID != "" {
		if err := variantCheckID("variant_id", r.VariantID); err != nil {
			return err
		}
	}
	if r.RenderID != "" {
		if err := variantCheckID("render_id", r.RenderID); err != nil {
			return err
		}
	}
	if err := variantCheckID("owner.tenant_id", r.OwnerTenantID); err != nil {
		return err
	}
	if err := variantCheckID("owner.account_id", r.OwnerAccountID); err != nil {
		return err
	}
	if r.OriginApp == "" || len(r.OriginApp) > variantMaxAppBytes || !variantAppRe.MatchString(r.OriginApp) {
		return ErrInvalid
	}
	if r.OriginContextRef == "" || len(r.OriginContextRef) > variantMaxRefStringBytes || !utf8.ValidString(r.OriginContextRef) {
		return ErrInvalid
	}
	for _, b := range []byte(r.OriginContextRef) {
		if b < 0x20 || b == 0x7f {
			return ErrInvalid
		}
	}
	if variantLevelRank(r.EditableLevel) < 0 {
		return ErrInvalid
	}
	if !variantDigestRe.MatchString(r.Digest) {
		return ErrInvalid
	}
	if r.Version < 1 {
		return ErrInvalid
	}
	if rule := variantPermissionMatrix(r.Permissions, r.EditableLevel); rule != "" {
		return fmt.Errorf("campaignmotion: permission matrix %s", rule)
	}
	vocab, params := 0, 0
	seen := map[string]bool{}
	hasParamEdit := false
	for _, a := range r.AllowedActions {
		if a == variantActionParameter {
			hasParamEdit = true
		}
	}
	for _, a := range r.AllowedActions {
		if len(a) > len(variantParamPrefix) && a[:len(variantParamPrefix)] == variantParamPrefix {
			if !variantIDRe.MatchString(a[len(variantParamPrefix):]) {
				return fmt.Errorf("campaignmotion: R8 malformed param declaration")
			}
			if seen[a] {
				return fmt.Errorf("campaignmotion: R8 duplicate param declaration")
			}
			seen[a] = true
			params++
			continue
		}
		if !variantValidAction(a) {
			return fmt.Errorf("campaignmotion: R8 unknown action")
		}
		if seen[a] {
			return fmt.Errorf("campaignmotion: R8 duplicate action")
		}
		seen[a] = true
		vocab++
	}
	if len(r.AllowedActions) == 0 || vocab == 0 || vocab > variantMaxAllowedActions {
		return fmt.Errorf("campaignmotion: R8 allowed_actions budget")
	}
	if params > variantMaxParamActions {
		return fmt.Errorf("campaignmotion: R8 param budget")
	}
	for _, a := range r.AllowedActions {
		if len(a) > len(variantParamPrefix) && a[:len(variantParamPrefix)] == variantParamPrefix {
			if !r.Permissions.CanParameterEdit || !hasParamEdit {
				return fmt.Errorf("campaignmotion: R9")
			}
			continue
		}
		if !r.Permissions.has(a) {
			return fmt.Errorf("campaignmotion: R9")
		}
	}
	return nil
}

func variantLevelRank(l string) int {
	switch l {
	case "flattened":
		return 0
	case variantEditableStructured:
		return 1
	case "native":
		return 2
	}
	return -1
}

func variantValidAction(a string) bool {
	switch a {
	case variantActionView, "use_render", variantActionOpen, variantActionParameter,
		"creative_edit", "export_project", "pay":
		return true
	}
	return false
}

// variantPermissionMatrix is the sdk v0.4.0 R0..R6 chain (R1 kept explicit,
// R2/R7 orthogonal), matching the constructor's domain.
func variantPermissionMatrix(p Permissions, level string) string {
	rank := variantLevelRank(level)
	switch {
	case !p.CanViewRender:
		return "R0"
	case p.CanUseRender && !p.CanViewRender:
		return "R1"
	case level == "flattened" && (p.CanOpenProject || p.CanParameterEdit || p.CanCreativeEdit || p.CanExportProject):
		return "R3"
	case p.CanParameterEdit && (!p.CanOpenProject || rank < 1):
		return "R4"
	case p.CanCreativeEdit && (!p.CanOpenProject || !p.CanParameterEdit || level != "native"):
		return "R5"
	case p.CanExportProject && (!p.CanOpenProject || rank < 1):
		return "R6"
	}
	return ""
}

// CanonicalVariantRefBytes writes the spec 3.1.2 canonical form by hand, byte
// identical to sdk v0.4.0 (golden-locked). encoding/json is deliberately not
// used: it HTML-escapes <>& and would break cross-issuer digest equality.
func CanonicalVariantRefBytes(r VariantRef) []byte {
	acts := append([]string{}, r.AllowedActions...)
	sort.Strings(acts)
	var b strings.Builder
	str := func(s string) {
		b.WriteByte('"')
		for i := 0; i < len(s); i++ {
			switch c := s[i]; {
			case c == '"' || c == '\\':
				b.WriteByte('\\')
				b.WriteByte(c)
			case c < 0x20:
				fmt.Fprintf(&b, "\\u%04x", c)
			default:
				b.WriteByte(c)
			}
		}
		b.WriteByte('"')
	}
	field := func(name string, first bool) {
		if !first {
			b.WriteByte(',')
		}
		str(name)
		b.WriteByte(':')
	}
	boolean := func(v bool) { b.WriteString(strconv.FormatBool(v)) }

	b.WriteByte('{')
	field("project_id", true)
	str(r.ProjectID)
	field("revision_id", false)
	str(r.RevisionID)
	if r.VariantID != "" {
		field("variant_id", false)
		str(r.VariantID)
	}
	if r.RenderID != "" {
		field("render_id", false)
		str(r.RenderID)
	}
	field("owner", false)
	b.WriteByte('{')
	field("tenant_id", true)
	str(r.OwnerTenantID)
	field("account_id", false)
	str(r.OwnerAccountID)
	b.WriteByte('}')
	field("origin_app", false)
	str(r.OriginApp)
	field("origin_context_ref", false)
	str(r.OriginContextRef)
	field("permissions", false)
	b.WriteByte('{')
	p := r.Permissions
	for i, kv := range []struct {
		k string
		v bool
	}{
		{"can_view_render", p.CanViewRender}, {"can_use_render", p.CanUseRender},
		{"can_open_project", p.CanOpenProject}, {"can_parameter_edit", p.CanParameterEdit},
		{"can_creative_edit", p.CanCreativeEdit}, {"can_export_project", p.CanExportProject},
		{"can_pay", p.CanPay},
	} {
		field(kv.k, i == 0)
		boolean(kv.v)
	}
	b.WriteByte('}')
	field("editable_level", false)
	str(r.EditableLevel)
	field("allowed_actions", false)
	b.WriteByte('[')
	for i, a := range acts {
		if i > 0 && a == acts[i-1] {
			continue
		}
		if i > 0 {
			b.WriteByte(',')
		}
		str(a)
	}
	b.WriteByte(']')
	field("version", false)
	b.WriteString(strconv.Itoa(r.Version))
	if r.ExpiresAt != nil {
		field("expires_at", false)
		str(r.ExpiresAt.UTC().Truncate(time.Second).Format(time.RFC3339))
	}
	b.WriteByte('}')
	return []byte(b.String())
}

// computeVariantRefDigest is sha256 over the "motionref.ref.v1\n" domain plus
// the canonical form. Digest detects change; it never authorises anything.
func computeVariantRefDigest(r VariantRef) string {
	h := sha256.New()
	h.Write([]byte(variantRefDigestDomain))
	h.Write(CanonicalVariantRefBytes(r))
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// verifyVariantRefDigest refuses to issue a shape whose digest does not bind
// its content: a sender cannot emit a lying reference.
func verifyVariantRefDigest(r VariantRef) error {
	if r.Digest != computeVariantRefDigest(r) {
		return errors.New("campaignmotion: digest does not bind this ref")
	}
	return nil
}

const variantRefQueryParam = "motionRef"

// ConsumerHandoffLink encodes the five stored keys into the consumer panel
// deep link: <base>?motionRef=<percent-encoded JSON>. https to any host, http
// only for loopback dev; base must not carry query/fragment/userinfo. Routing,
// never authorization: the receiving side re-checks everything against the
// original owner.
func ConsumerHandoffLink(base string, r VariantRef) (string, error) {
	if err := validateVariantRef(r); err != nil {
		return "", err
	}
	if bound := computeVariantRefDigest(r); r.Digest != bound {
		return "", fmt.Errorf("campaignmotion: digest does not bind this ref (want %s, got %s)", bound, r.Digest)
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", ErrInvalid
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return "", ErrInvalid
		}
	default:
		return "", ErrInvalid
	}
	if u.User != nil {
		return "", ErrInvalid
	}
	if u.Host == "" {
		return "", ErrInvalid
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return "", ErrInvalid
	}
	if u.RawQuery != "" {
		return "", ErrInvalid
	}
	if u.Path != "" && (!strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//")) {
		return "", ErrInvalid
	}
	payload := variantStoredJSON(r)
	return u.String() + "?" + variantRefQueryParam + "=" + encodeReturnToken(payload), nil
}

// variantStoredJSON writes the five receiver storage keys in storage order,
// byte-identical to the sdk v0.4.0 encoder (golden-locked).
func variantStoredJSON(r VariantRef) string {
	var b strings.Builder
	b.WriteString(`{"project_id":`)
	variantWriteString(&b, r.ProjectID)
	b.WriteString(`,"revision_id":`)
	variantWriteString(&b, r.RevisionID)
	b.WriteString(`,"digest":`)
	variantWriteString(&b, r.Digest)
	b.WriteString(`,"origin_app":`)
	variantWriteString(&b, r.OriginApp)
	b.WriteString(`,"origin_context_ref":`)
	variantWriteString(&b, r.OriginContextRef)
	b.WriteByte('}')
	return b.String()
}

func variantWriteString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < 0x20:
			const hex = "0123456789abcdef"
			b.WriteString(`\u00`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}

// encodeReturnToken percent-encodes a query value with exactly the
// encodeURIComponent alphabet: unreserved A-Za-z0-9-_.!~*'() pass, every other
// byte becomes %XX over UTF-8 with uppercase hex. Hand-rolled so Go and
// JavaScript issuers produce byte-identical links.
func encodeReturnToken(s string) string {
	const unreserved = "-_.!~*'()"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			strings.IndexByte(unreserved, c) >= 0:
			b.WriteByte(c)
		default:
			const hexUpper = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&0x0f])
		}
	}
	return b.String()
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
