// policy.go — HUI-2747 蜂群消费者强化契约：
//   - 最小受限上下文交接与 CRM 隐私/全局资源配置拦截；
//   - 多渠道参数化 Variant 差异约束与非线性高价调用/重复扣费/预算守卫；
//   - Repeat Method 复用权限与模板可见性边界（私有客户作品绝不可自动变公共模板）；
//   - MX-OPS 服务端权益核验、队列/夜间/闲时等待回流与双轨状态分层；
//   - Render 归因保留与 Leads 转化权威分离（绝不反向写创作工程）。
//
// 本文件不发起任何网络/模型调用，不重建视频编辑器或计算调度引擎。
package campaignmotion

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	// ErrPrivacyBlocked signals that CRM customer privacy, contact phone/email,
	// chat transcripts, or unauthorized leads attempted to cross the boundary.
	ErrPrivacyBlocked = errors.New("campaignmotion: crm privacy blocked")
	// ErrGlobalResourceConfigForbidden rejects any global compute/queue/cluster
	// configuration fields in requests or backflows.
	ErrGlobalResourceConfigForbidden = errors.New("campaignmotion: global resource configuration is forbidden")
	// ErrInternalPreferenceForbidden rejects any attempt (especially by free accounts)
	// to claim or access internal scheduling privileges.
	ErrInternalPreferenceForbidden = errors.New("campaignmotion: internal execution privilege is forbidden")
	// ErrEntitlementDenied rejects execution preferences not granted by server-verified entitlement.
	ErrEntitlementDenied = errors.New("campaignmotion: execution preference not allowed by entitlement")
	// ErrAgentEscalation rejects an agent attempting to escalate execution priority/tier.
	ErrAgentEscalation = errors.New("campaignmotion: agent cannot escalate execution preference")
	// ErrLinearModelCallSpike rejects N-channel parametric renders that claim linear high-cost model calls.
	ErrLinearModelCallSpike = errors.New("campaignmotion: parametric variants must not trigger linear high-cost model calls")
	// ErrDuplicateCharge rejects duplicate billing receipts or repeated generation charges.
	ErrDuplicateCharge = errors.New("campaignmotion: duplicate generation charge rejected")
	// ErrBudgetExceeded rejects costs exceeding the campaign budget cap or unauthorized payer.
	ErrBudgetExceeded = errors.New("campaignmotion: campaign budget exceeded or payer unauthorized")
	// ErrPrivateWorkCannotBecomePublic blocks private customer works from becoming
	// public templates or being reused across customers.
	ErrPrivateWorkCannotBecomePublic = errors.New("campaignmotion: private customer work cannot become public template")
	// ErrRepeatMethodForbidden rejects invalid repeat methods or missing team template authorization.
	ErrRepeatMethodForbidden = errors.New("campaignmotion: repeat method not authorized")
	// ErrAutoSideEffectForbidden rejects any backflow claiming or requesting auto-outreach,
	// auto-publish, or auto-conversion recording.
	ErrAutoSideEffectForbidden = errors.New("campaignmotion: automatic outreach/publish/conversion is forbidden")
	// ErrProjectMismatch rejects a backflow attempting to attach a second project to the same handoff.
	ErrProjectMismatch = errors.New("campaignmotion: backflow project/revision mismatch; refusing second project")
	// ErrRevoked rejects operations after the campaign handoff authorization has been revoked.
	ErrRevoked = errors.New("campaignmotion: campaign handoff authorization revoked")
	// ErrIdempotencyConflict rejects reusing an idempotency key with a different payload.
	ErrIdempotencyConflict = errors.New("campaignmotion: idempotency key reused with conflicting payload")
	// ErrCreativeWritebackForbidden rejects any attempt to write conversion performance
	// back into creative project facts.
	ErrCreativeWritebackForbidden = errors.New("campaignmotion: conversion attribution cannot be written back to creative project")
)

var (
	idCardRe = regexp.MustCompile(`(^|[^0-9])[1-9]\d{5}(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx]([^0-9]|$)`)
)

// Canonical MX-OPS named execution preferences.
const (
	PrefImmediate = "immediate"
	PrefPriority  = "priority"
	PrefNight     = "night"
	PrefIdle      = "idle"
)

// Template visibility levels for Repeat Method.
const (
	TemplateVisibilityPrivateCustomer = "private_customer_work"
	TemplateVisibilityAuthorizedTeam  = "authorized_team_template"
	TemplateVisibilityPlatform        = "platform_template"
)

// Repeat Method modes.
const (
	RepeatModeSameCampaignDifferentStore   = "same_campaign_different_store"
	RepeatModeSameProductDifferentChannel  = "same_product_different_channel"
	RepeatModeCrossCustomerTemplate        = "cross_customer_template"
)

// Queue states and phases for MX-OPS backflow.
const (
	QueueStateQueued        = "queued"
	QueueStateWaitingWindow = "waiting_window"
	QueueStateRunning       = "running"
	QueueStateSucceeded     = "succeeded"
	QueueStateFailed        = "failed"
	QueueStateCancelled     = "cancelled"
)

// Wait kind stratification.
const (
	WaitKindNone          = "none"
	WaitKindCloudArtifact = "cloud_artifact"
	WaitKindHumanFollowup = "human_followup"
	WaitKindBoth          = "both"
)

// Render modes for attribution and cost accounting.
const (
	RenderModeParametricVariant = "parametric_variant"
	RenderModeBaseline          = "baseline"
)

// Global resource configuration keys that must never be accepted or exposed.
var forbiddenGlobalResourceKeys = map[string]bool{
	"global_resources":       true,
	"global_resource_config": true,
	"gpu_pool":               true,
	"gpu_nodes":              true,
	"cluster_config":         true,
	"cluster_capacity":       true,
	"worker_pool":            true,
	"worker_capacity":        true,
	"queue_weights":          true,
	"queue_weight":           true,
	"global_queue_depth":     true,
	"scheduler_config":       true,
	"internal_priority":      true,
	"node_selector":          true,
	"h3_engine_config":       true,
	"compute_dispatch":       true,
}

var allowedAspectRatios = map[string]bool{
	"9:16": true,
	"16:9": true,
	"1:1":  true,
	"3:4":  true,
	"4:3":  true,
	"4:5":  true,
}

var privacyTextMarkers = []string{
	"crm_profile",
	"crm_note",
	"internal_note",
	"customer_list",
	"phone_list",
	"chat_transcript",
	"chat_history",
	"私聊全文",
	"聊天记录全文",
	"聊天原文",
	"手机号列表",
	"客户档案",
	"未授权线索",
	"内部备注",
}

// ApprovedAssetRef records an authorized asset id with optional pinned version
// and license expiry so night-queue waiting retains exact authorized versions.
type ApprovedAssetRef struct {
	ID        string `json:"id"`
	Version   string `json:"version,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// BudgetSpec carries campaign budget cap and attribution id without exposing wallets.
type BudgetSpec struct {
	BudgetAttributionID string `json:"budget_attribution_id"`
	MaxBudgetCents      int64  `json:"max_budget_cents,omitempty"`
}

// PayerSpec identifies the organization or explicitly authorized payer account.
type PayerSpec struct {
	PayerKind      string `json:"payer_kind"`
	PayerAccountID string `json:"payer_account_id"`
}

// VariantParamDiff captures per-channel parametric differences only (aspect ratio,
// layout preset, CTA override, store label, and authorized asset slot overrides).
type VariantParamDiff struct {
	Channel            string            `json:"channel"`
	AspectRatio        string            `json:"aspect_ratio,omitempty"`
	LayoutPreset       string            `json:"layout_preset,omitempty"`
	CTAOverride        string            `json:"cta_override,omitempty"`
	StoreLabel         string            `json:"store_label,omitempty"`
	AssetSlotOverrides map[string]string `json:"asset_slot_overrides,omitempty"`
}

// VerifiedEntitlement is the server-verified execution entitlement for a tenant/campaign.
// It is NEVER populated from client request bodies.
type VerifiedEntitlement struct {
	TenantID           string
	Tier               string   // "free", "standard", "pro", "team", "enterprise"
	PlanStatus         string   // "active", "expired", "inactive"
	AllowedPreferences []string // normalized canonical preferences granted by server/MX-OPS
	OrgBillingAccount  string
	AuthorizedPayer    string
	MaxBudgetCents     int64 // 0 means no extra server cap beyond handoff budget
	VerifiedBy         string
}

// EntitlementVerifier resolves server-verified MX-OPS entitlements for a tenant/campaign.
type EntitlementVerifier interface {
	VerifyCampaignEntitlement(tenantID, campaignID string) (VerifiedEntitlement, error)
}

// EntitlementCheckInput holds server-side identity and entitlement context for
// execution preference authorization.
type EntitlementCheckInput struct {
	MemberRole          string // "owner", "sales", "agent"
	RequestedPreference string
	ExistingPreference  string // non-empty if updating an existing handoff
	Entitlement         VerifiedEntitlement
}

// AuthorizedPreference is the server-approved execution preference result.
type AuthorizedPreference struct {
	Canonical  string
	WireValue  string
	Tier       string
	VerifiedBy string
}

// NormalizeExecutionPreference maps canonical and legacy C0 preference names to
// canonical MX-OPS values. Internal/admin/unknown values are rejected fail-closed.
func NormalizeExecutionPreference(raw string) (canonical string, wire string, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", nil
	}
	lower := strings.ToLower(s)
	switch lower {
	case "internal", "internal_priority", "internal_queue", "system", "admin", "root", "super":
		return "", "", ErrInternalPreferenceForbidden
	case "immediate":
		return PrefImmediate, "immediate", nil
	case "priority":
		return PrefPriority, "priority", nil
	case "paid_elastic":
		return PrefPriority, "paid_elastic", nil
	case "night":
		return PrefNight, "night", nil
	case "night_batch":
		return PrefNight, "night_batch", nil
	case "idle":
		return PrefIdle, "idle", nil
	case "free_idle":
		return PrefIdle, "free_idle", nil
	default:
		return "", "", ErrInvalid
	}
}

// PreferenceRank defines relative scheduling priority order for anti-escalation checks.
func PreferenceRank(canonical string) int {
	switch canonical {
	case "":
		return 0
	case PrefIdle:
		return 1
	case PrefNight:
		return 2
	case PrefImmediate:
		return 3
	case PrefPriority:
		return 4
	default:
		return -1
	}
}

// AuthorizeExecutionPreference enforces MX-OPS entitlement rules:
//  1. Free accounts (or any account) can NEVER access internal privileges.
//  2. Only server-verified entitlements may choose priority / night / idle.
//  3. Agents can NEVER select priority or escalate above existing/baseline rank.
func AuthorizeExecutionPreference(in EntitlementCheckInput) (AuthorizedPreference, error) {
	canonical, wire, err := NormalizeExecutionPreference(in.RequestedPreference)
	if err != nil {
		return AuthorizedPreference{}, err
	}
	if canonical == "" {
		return AuthorizedPreference{
			Tier:       in.Entitlement.Tier,
			VerifiedBy: in.Entitlement.VerifiedBy,
		}, nil
	}

	role := strings.ToLower(strings.TrimSpace(in.MemberRole))
	if role == "agent" {
		// Agents can never select priority or escalate past an existing preference.
		if canonical == PrefPriority {
			return AuthorizedPreference{}, ErrAgentEscalation
		}
		if existingCanon, _, exErr := NormalizeExecutionPreference(in.ExistingPreference); exErr == nil && existingCanon != "" {
			if PreferenceRank(canonical) > PreferenceRank(existingCanon) {
				return AuthorizedPreference{}, ErrAgentEscalation
			}
		}
	}

	tier := strings.ToLower(strings.TrimSpace(in.Entitlement.Tier))
	if tier == "" {
		tier = "free"
	}

	// Build allowed set strictly from server-verified entitlement.
	allowed := map[string]bool{}
	for _, p := range in.Entitlement.AllowedPreferences {
		c, _, pErr := NormalizeExecutionPreference(p)
		if pErr == nil && c != "" {
			allowed[c] = true
		}
	}
	// If the server verifier did not populate explicit AllowedPreferences, derive
	// fail-closed defaults from verified active paid plan status.
	if len(allowed) == 0 {
		activePaid := strings.EqualFold(strings.TrimSpace(in.Entitlement.PlanStatus), "active") &&
			tier != "free" && tier != "trial"
		if activePaid {
			allowed[PrefImmediate] = true
			allowed[PrefNight] = true
			allowed[PrefIdle] = true
			if tier == "pro" || tier == "crm_pro" || tier == "team" || tier == "enterprise" || tier == "paid" {
				allowed[PrefPriority] = true
			}
		} else {
			// Free or inactive accounts only have baseline immediate (no priority/night/idle/internal).
			allowed[PrefImmediate] = true
		}
	}

	if !allowed[canonical] {
		return AuthorizedPreference{}, ErrEntitlementDenied
	}
	return AuthorizedPreference{
		Canonical:  canonical,
		WireValue:  wire,
		Tier:       tier,
		VerifiedBy: in.Entitlement.VerifiedBy,
	}, nil
}

// ValidatePayerAuthorization verifies that the declared payer belongs to the
// tenant's organization billing account or explicitly authorized payer account.
func ValidatePayerAuthorization(payer *PayerSpec, ent VerifiedEntitlement) error {
	if payer == nil {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(payer.PayerKind))
	acct := strings.TrimSpace(payer.PayerAccountID)
	if acct == "" || !tokenRe.MatchString(acct) {
		return ErrBudgetExceeded
	}
	if ent.OrgBillingAccount == "" && ent.AuthorizedPayer == "" {
		// No billing cache configured yet: only validate token/kind shape.
		if kind != "organization" && kind != "authorized" {
			return ErrBudgetExceeded
		}
		return nil
	}
	switch kind {
	case "organization":
		if ent.OrgBillingAccount == "" || acct != ent.OrgBillingAccount {
			return ErrBudgetExceeded
		}
		return nil
	case "authorized":
		if ent.AuthorizedPayer == "" || acct != ent.AuthorizedPayer || acct == ent.OrgBillingAccount {
			return ErrBudgetExceeded
		}
		return nil
	default:
		return ErrBudgetExceeded
	}
}

// ContainsPrivacyLeak reports whether s contains phone numbers, emails,
// Chinese national IDs, or explicit CRM private transcript/list markers.
func ContainsPrivacyLeak(s string) bool {
	if s == "" {
		return false
	}
	if phoneRe.MatchString(s) || emailRe.MatchString(s) || idCardRe.MatchString(s) {
		return true
	}
	lower := strings.ToLower(s)
	for _, marker := range privacyTextMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ValidateNoGlobalResourceConfig walks any decoded JSON tree and rejects forbidden
// global compute/queue/cluster configuration keys.
func ValidateNoGlobalResourceConfig(v any) error {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			lk := strings.ToLower(strings.TrimSpace(k))
			if forbiddenGlobalResourceKeys[lk] {
				return ErrGlobalResourceConfigForbidden
			}
			if err := ValidateNoGlobalResourceConfig(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range t {
			if err := ValidateNoGlobalResourceConfig(child); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateReturnURL validates a safe return_url: either a clean relative app path
// starting with "/" (not "//") or an https / loopback-http URL without userinfo,
// fragment abuse, or privacy leaks.
func ValidateReturnURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if len(s) > 512 || !utf8.ValidString(s) {
		return "", ErrInvalid
	}
	if ContainsPrivacyLeak(s) {
		return "", ErrPrivacyBlocked
	}
	if strings.HasPrefix(s, "/") {
		if strings.HasPrefix(s, "//") || strings.ContainsAny(s, "\r\n\t ") {
			return "", ErrInvalid
		}
		return s, nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil {
		return "", ErrInvalid
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		if !strings.EqualFold(host, "localhost") {
			ip := net.ParseIP(host)
			if ip == nil || !ip.IsLoopback() {
				return "", ErrInvalid
			}
		}
	default:
		return "", ErrInvalid
	}
	return u.String(), nil
}

// ValidateParametricVariantDiff validates one channel's parametric differences.
// Every asset slot override must reference an authorized asset id, and no PII is allowed.
func ValidateParametricVariantDiff(diff VariantParamDiff, authorizedAssets []string) (VariantParamDiff, error) {
	diff.Channel = strings.TrimSpace(diff.Channel)
	if !validChannel(diff.Channel) || ContainsPrivacyLeak(diff.Channel) {
		return VariantParamDiff{}, ErrChannel
	}
	if diff.AspectRatio != "" {
		diff.AspectRatio = strings.TrimSpace(diff.AspectRatio)
		if !allowedAspectRatios[diff.AspectRatio] {
			return VariantParamDiff{}, ErrInvalid
		}
	}
	if diff.LayoutPreset != "" {
		diff.LayoutPreset = strings.TrimSpace(diff.LayoutPreset)
		if !tokenRe.MatchString(diff.LayoutPreset) || ContainsPrivacyLeak(diff.LayoutPreset) {
			return VariantParamDiff{}, ErrInvalid
		}
	}
	if diff.CTAOverride != "" {
		diff.CTAOverride = strings.TrimSpace(diff.CTAOverride)
		if utf8.RuneCountInString(diff.CTAOverride) > 120 {
			return VariantParamDiff{}, ErrInvalid
		}
		if ContainsPrivacyLeak(diff.CTAOverride) {
			return VariantParamDiff{}, ErrPrivacyBlocked
		}
	}
	if diff.StoreLabel != "" {
		diff.StoreLabel = strings.TrimSpace(diff.StoreLabel)
		if utf8.RuneCountInString(diff.StoreLabel) > 80 {
			return VariantParamDiff{}, ErrInvalid
		}
		if ContainsPrivacyLeak(diff.StoreLabel) {
			return VariantParamDiff{}, ErrPrivacyBlocked
		}
	}
	if len(diff.AssetSlotOverrides) > 0 {
		if len(diff.AssetSlotOverrides) > variantMaxParamActions {
			return VariantParamDiff{}, ErrInvalid
		}
		authSet := make(map[string]bool, len(authorizedAssets))
		for _, id := range authorizedAssets {
			authSet[id] = true
		}
		clean := make(map[string]string, len(diff.AssetSlotOverrides))
		for slot, assetID := range diff.AssetSlotOverrides {
			slot = strings.TrimSpace(slot)
			assetID = strings.TrimSpace(assetID)
			if !tokenRe.MatchString(slot) || !tokenRe.MatchString(assetID) {
				return VariantParamDiff{}, ErrInvalid
			}
			if ContainsPrivacyLeak(slot) || ContainsPrivacyLeak(assetID) {
				return VariantParamDiff{}, ErrPrivacyBlocked
			}
			if !authSet[assetID] {
				return VariantParamDiff{}, ErrInvalid
			}
			clean[slot] = assetID
		}
		diff.AssetSlotOverrides = clean
	}
	return diff, nil
}

// RepeatMethodSpec represents a reusable production recipe saved from a successful
// campaign asset without leaking private customer works into public templates.
type RepeatMethodSpec struct {
	Mode               string            `json:"mode"`
	Name               string            `json:"name"`
	TemplateID         string            `json:"template_id"`
	TemplateVisibility string            `json:"template_visibility"`
	TeamGrantID        string            `json:"team_grant_id,omitempty"`
	GrantExpiresAt     *time.Time        `json:"grant_expires_at,omitempty"`
	PublicTemplate     bool              `json:"public_template,omitempty"`
	PromoteToPublic    bool              `json:"promote_to_public,omitempty"`
	ShareCrossTenant   bool              `json:"share_cross_tenant,omitempty"`
	SourceFactsVersion string            `json:"source_facts_version,omitempty"`
	ParametricSlots    map[string]string `json:"parametric_slots,omitempty"`
}

// ValidateRepeatMethod enforces Repeat Method mode semantics and strict template
// visibility boundaries. Private customer works can NEVER become public templates
// or be reused across customers.
func ValidateRepeatMethod(spec RepeatMethodSpec, now time.Time) (RepeatMethodSpec, error) {
	spec.Mode = strings.TrimSpace(spec.Mode)
	spec.Name = strings.TrimSpace(spec.Name)
	spec.TemplateID = strings.TrimSpace(spec.TemplateID)
	spec.TemplateVisibility = strings.TrimSpace(spec.TemplateVisibility)
	spec.TeamGrantID = strings.TrimSpace(spec.TeamGrantID)
	spec.SourceFactsVersion = strings.TrimSpace(spec.SourceFactsVersion)

	if spec.Name == "" || utf8.RuneCountInString(spec.Name) > 80 {
		return RepeatMethodSpec{}, ErrInvalid
	}
	if ContainsPrivacyLeak(spec.Name) || ContainsPrivacyLeak(spec.TemplateID) || ContainsPrivacyLeak(spec.TeamGrantID) {
		return RepeatMethodSpec{}, ErrPrivacyBlocked
	}
	if !tokenRe.MatchString(spec.TemplateID) {
		return RepeatMethodSpec{}, ErrInvalid
	}
	if spec.SourceFactsVersion != "" && !tokenRe.MatchString(spec.SourceFactsVersion) {
		return RepeatMethodSpec{}, ErrInvalid
	}

	switch spec.TemplateVisibility {
	case TemplateVisibilityPrivateCustomer, TemplateVisibilityAuthorizedTeam, TemplateVisibilityPlatform:
	default:
		return RepeatMethodSpec{}, ErrRepeatMethodForbidden
	}

	// Hard boundary: private customer works can NEVER become public templates or cross-tenant.
	if spec.TemplateVisibility == TemplateVisibilityPrivateCustomer {
		if spec.PublicTemplate || spec.PromoteToPublic || spec.ShareCrossTenant {
			return RepeatMethodSpec{}, ErrPrivateWorkCannotBecomePublic
		}
		if spec.Mode == RepeatModeCrossCustomerTemplate {
			return RepeatMethodSpec{}, ErrPrivateWorkCannotBecomePublic
		}
	}
	// Team templates also cannot be silently promoted to public templates.
	if spec.TemplateVisibility == TemplateVisibilityAuthorizedTeam {
		if spec.PublicTemplate || spec.PromoteToPublic {
			return RepeatMethodSpec{}, ErrPrivateWorkCannotBecomePublic
		}
	}

	switch spec.Mode {
	case RepeatModeSameCampaignDifferentStore, RepeatModeSameProductDifferentChannel:
		// Allowed for private/team/platform templates within the same tenant.
	case RepeatModeCrossCustomerTemplate:
		switch spec.TemplateVisibility {
		case TemplateVisibilityPlatform:
			// Platform template allowed.
		case TemplateVisibilityAuthorizedTeam:
			if spec.TeamGrantID == "" || !tokenRe.MatchString(spec.TeamGrantID) {
				return RepeatMethodSpec{}, ErrRepeatMethodForbidden
			}
			if spec.GrantExpiresAt != nil && !now.Before(*spec.GrantExpiresAt) {
				return RepeatMethodSpec{}, ErrVariantExpired
			}
		default:
			return RepeatMethodSpec{}, ErrPrivateWorkCannotBecomePublic
		}
	default:
		return RepeatMethodSpec{}, ErrRepeatMethodForbidden
	}

	if len(spec.ParametricSlots) > variantMaxParamActions {
		return RepeatMethodSpec{}, ErrInvalid
	}
	cleanSlots := make(map[string]string, len(spec.ParametricSlots))
	for k, v := range spec.ParametricSlots {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if !tokenRe.MatchString(k) || utf8.RuneCountInString(v) > 120 {
			return RepeatMethodSpec{}, ErrInvalid
		}
		if ContainsPrivacyLeak(k) || ContainsPrivacyLeak(v) {
			return RepeatMethodSpec{}, ErrPrivacyBlocked
		}
		cleanSlots[k] = v
	}
	spec.ParametricSlots = cleanSlots
	return spec, nil
}

// RetainedAssetVersion pins an authorized asset id + version while a job waits
// in a night/idle queue so resume/backflow keeps the exact authorized snapshot.
type RetainedAssetVersion struct {
	AssetID string `json:"asset_id"`
	Version string `json:"version"`
}

// QueueBackflow is the MX-OPS queue / night / idle status backflow payload.
// ETASeconds is a nullable pointer: nil means unknown (serialized as JSON null, never 0).
type QueueBackflow struct {
	IdempotencyKey         string                 `json:"idempotency_key"`
	JobID                  string                 `json:"job_id"`
	CreativeID             string                 `json:"creative_id"`
	CampaignID             string                 `json:"campaign_id"`
	ProjectID              string                 `json:"project_id"`
	RevisionID             string                 `json:"revision_id"`
	Digest                 string                 `json:"digest"`
	ExecutionPreference    string                 `json:"execution_preference,omitempty"`
	Phase                  string                 `json:"phase"`
	QueueState             string                 `json:"queue_state"`
	WaitReason             string                 `json:"wait_reason"`
	NextWindow             string                 `json:"next_window,omitempty"`
	ETASeconds             *int64                 `json:"eta_seconds"`
	PolicyVersion          string                 `json:"policy_version"`
	RetainedAssetVersions  []RetainedAssetVersion `json:"retained_asset_versions,omitempty"`
	AutoOutreach           bool                   `json:"auto_outreach,omitempty"`
	AutoPublish            bool                   `json:"auto_publish,omitempty"`
	AutoRecordConversion   bool                   `json:"auto_record_conversion,omitempty"`
}

// WaitStratification explicitly separates waiting for cloud artifacts from
// waiting for human sales follow-up / review, and guarantees GPU queueing never
// blocks ordinary CRM work.
type WaitStratification struct {
	CloudArtifactWaiting bool   `json:"cloud_artifact_waiting"`
	HumanFollowupPending bool   `json:"human_followup_pending"`
	CRMWorkBlocked       bool   `json:"crm_work_blocked"`
	WaitingKind          string `json:"waiting_kind"`
}

// BuildWaitStratification derives the honest dual-track waiting classification.
func BuildWaitStratification(queueState string, humanFollowupPending bool) WaitStratification {
	cloudWaiting := queueState == QueueStateQueued ||
		queueState == QueueStateWaitingWindow ||
		queueState == QueueStateRunning
	kind := WaitKindNone
	switch {
	case cloudWaiting && humanFollowupPending:
		kind = WaitKindBoth
	case cloudWaiting:
		kind = WaitKindCloudArtifact
	case humanFollowupPending:
		kind = WaitKindHumanFollowup
	}
	return WaitStratification{
		CloudArtifactWaiting: cloudWaiting,
		HumanFollowupPending: humanFollowupPending,
		CRMWorkBlocked:       false, // GPU queueing NEVER blocks CRM daily work.
		WaitingKind:          kind,
	}
}

// ValidateQueueBackflow validates an MX-OPS queue status backflow against the
// existing campaign handoff document so replays update the same creative/job
// without creating a second project, leaking privacy, or triggering outreach.
func ValidateQueueBackflow(in QueueBackflow, expectedCampaignID string, expectedDoc Document) (QueueBackflow, error) {
	if in.AutoOutreach || in.AutoPublish || in.AutoRecordConversion {
		return QueueBackflow{}, ErrAutoSideEffectForbidden
	}
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	in.JobID = strings.TrimSpace(in.JobID)
	in.CreativeID = strings.TrimSpace(in.CreativeID)
	in.CampaignID = strings.TrimSpace(in.CampaignID)
	in.ProjectID = strings.TrimSpace(in.ProjectID)
	in.RevisionID = strings.TrimSpace(in.RevisionID)
	in.Digest = strings.TrimSpace(in.Digest)
	in.Phase = strings.TrimSpace(in.Phase)
	in.QueueState = strings.TrimSpace(in.QueueState)
	in.WaitReason = strings.TrimSpace(in.WaitReason)
	in.NextWindow = strings.TrimSpace(in.NextWindow)
	in.PolicyVersion = strings.TrimSpace(in.PolicyVersion)

	if !tokenRe.MatchString(in.IdempotencyKey) || !tokenRe.MatchString(in.JobID) || !tokenRe.MatchString(in.CreativeID) {
		return QueueBackflow{}, ErrInvalid
	}
	if in.CampaignID != "" && in.CampaignID != expectedCampaignID {
		return QueueBackflow{}, ErrMotion
	}
	in.CampaignID = expectedCampaignID

	// Must bind to the existing handoff project/revision — never create a second project.
	if in.ProjectID == "" || in.RevisionID == "" || in.Digest == "" {
		return QueueBackflow{}, ErrMotion
	}
	if !digestRe.MatchString(in.Digest) {
		return QueueBackflow{}, ErrDigest
	}
	if in.ProjectID != expectedDoc.Motion.ProjectID || in.RevisionID != expectedDoc.Motion.RevisionID || in.Digest != expectedDoc.Motion.Digest {
		return QueueBackflow{}, ErrProjectMismatch
	}

	if in.ExecutionPreference != "" {
		_, wire, err := NormalizeExecutionPreference(in.ExecutionPreference)
		if err != nil {
			return QueueBackflow{}, err
		}
		in.ExecutionPreference = wire
	}

	switch in.QueueState {
	case QueueStateQueued, QueueStateWaitingWindow, QueueStateRunning,
		QueueStateSucceeded, QueueStateFailed, QueueStateCancelled:
	default:
		return QueueBackflow{}, ErrInvalid
	}
	if !tokenRe.MatchString(in.Phase) || !tokenRe.MatchString(in.PolicyVersion) {
		return QueueBackflow{}, ErrInvalid
	}
	if utf8.RuneCountInString(in.WaitReason) > 160 || utf8.RuneCountInString(in.NextWindow) > 128 {
		return QueueBackflow{}, ErrInvalid
	}
	if ContainsPrivacyLeak(in.WaitReason) || ContainsPrivacyLeak(in.NextWindow) ||
		ContainsPrivacyLeak(in.Phase) || ContainsPrivacyLeak(in.PolicyVersion) ||
		ContainsPrivacyLeak(in.CreativeID) || ContainsPrivacyLeak(in.JobID) {
		return QueueBackflow{}, ErrPrivacyBlocked
	}
	// Nullable ETA: nil is valid (unknown), negative values are invalid.
	if in.ETASeconds != nil && *in.ETASeconds < 0 {
		return QueueBackflow{}, ErrInvalid
	}

	// Retained authorized asset versions must belong to the handoff's authorized assets.
	if len(in.RetainedAssetVersions) > variantMaxParamActions {
		return QueueBackflow{}, ErrInvalid
	}
	authSet := make(map[string]bool, len(expectedDoc.AuthorizedAssetIDs))
	for _, id := range expectedDoc.AuthorizedAssetIDs {
		authSet[id] = true
	}
	cleanAssets := make([]RetainedAssetVersion, 0, len(in.RetainedAssetVersions))
	seenAsset := map[string]bool{}
	for _, a := range in.RetainedAssetVersions {
		a.AssetID = strings.TrimSpace(a.AssetID)
		a.Version = strings.TrimSpace(a.Version)
		if !tokenRe.MatchString(a.AssetID) || !tokenRe.MatchString(a.Version) {
			return QueueBackflow{}, ErrInvalid
		}
		if ContainsPrivacyLeak(a.AssetID) || ContainsPrivacyLeak(a.Version) {
			return QueueBackflow{}, ErrPrivacyBlocked
		}
		if !authSet[a.AssetID] {
			return QueueBackflow{}, ErrInvalid
		}
		if seenAsset[a.AssetID] {
			continue
		}
		seenAsset[a.AssetID] = true
		cleanAssets = append(cleanAssets, a)
	}
	sort.Slice(cleanAssets, func(i, j int) bool { return cleanAssets[i].AssetID < cleanAssets[j].AssetID })
	in.RetainedAssetVersions = cleanAssets
	return in, nil
}

// RenderBackflow is a completed/candidate render returned from Swarm/Motion.
// It carries full attribution keys and cost/call counters for non-linear guardrails.
type RenderBackflow struct {
	IdempotencyKey        string `json:"idempotency_key"`
	CampaignID            string `json:"campaign_id"`
	CreativeID            string `json:"creative_id"`
	ProjectID             string `json:"project_id"`
	RevisionID            string `json:"revision_id"`
	Digest                string `json:"digest"`
	RenderID              string `json:"render_id"`
	VariantID             string `json:"variant_id"`
	Channel               string `json:"channel"`
	SourceFactsVersion    string `json:"source_facts_version"`
	RenderMode            string `json:"render_mode"` // "parametric_variant" | "baseline"
	HighCostModelCalls    int    `json:"high_cost_model_calls"`
	BilledGenerationCount int    `json:"billed_generation_count"`
	CostCents             int64  `json:"cost_cents"`
	BillingReceiptID      string `json:"billing_receipt_id,omitempty"`
	PayerRef              string `json:"payer_ref,omitempty"`
	BudgetAttributionID   string `json:"budget_attribution_id,omitempty"`
	AutoOutreach          bool   `json:"auto_outreach,omitempty"`
	AutoPublish           bool   `json:"auto_publish,omitempty"`
	AutoRecordConversion  bool   `json:"auto_record_conversion,omitempty"`
	WritebackCreativeFact bool   `json:"writeback_creative_fact,omitempty"`
}

// RenderAccountingState summarizes already-recorded renders for the same
// campaign + project + revision so the guard can enforce non-linear model calls,
// no duplicate billing receipts, and budget caps across all channel variants.
type RenderAccountingState struct {
	ExistingTotalHighCostCalls int
	ExistingTotalCostCents     int64
	ExistingChannelCount       int
	ExistingReceiptIDs         map[string]string // billing_receipt_id -> creative_id|variant_id
}

// ValidateRenderBackflow verifies attribution completeness, project/revision
// binding, non-linear high-cost model call guardrails, no duplicate charges,
// budget caps, and zero automatic outreach/publish/conversion/writeback.
func ValidateRenderBackflow(in RenderBackflow, expectedCampaignID string, expectedDoc Document, acc RenderAccountingState) (RenderBackflow, error) {
	if in.AutoOutreach || in.AutoPublish || in.AutoRecordConversion {
		return RenderBackflow{}, ErrAutoSideEffectForbidden
	}
	if in.WritebackCreativeFact {
		return RenderBackflow{}, ErrCreativeWritebackForbidden
	}

	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	in.CampaignID = strings.TrimSpace(in.CampaignID)
	in.CreativeID = strings.TrimSpace(in.CreativeID)
	in.ProjectID = strings.TrimSpace(in.ProjectID)
	in.RevisionID = strings.TrimSpace(in.RevisionID)
	in.Digest = strings.TrimSpace(in.Digest)
	in.RenderID = strings.TrimSpace(in.RenderID)
	in.VariantID = strings.TrimSpace(in.VariantID)
	in.Channel = strings.TrimSpace(in.Channel)
	in.SourceFactsVersion = strings.TrimSpace(in.SourceFactsVersion)
	in.RenderMode = strings.TrimSpace(in.RenderMode)
	in.BillingReceiptID = strings.TrimSpace(in.BillingReceiptID)
	in.PayerRef = strings.TrimSpace(in.PayerRef)
	in.BudgetAttributionID = strings.TrimSpace(in.BudgetAttributionID)

	for _, tok := range []string{
		in.IdempotencyKey, in.CreativeID, in.RenderID, in.VariantID, in.SourceFactsVersion,
	} {
		if !tokenRe.MatchString(tok) {
			return RenderBackflow{}, ErrInvalid
		}
		if ContainsPrivacyLeak(tok) {
			return RenderBackflow{}, ErrPrivacyBlocked
		}
	}
	if !validChannel(in.Channel) || ContainsPrivacyLeak(in.Channel) {
		return RenderBackflow{}, ErrChannel
	}
	if in.CampaignID != "" && in.CampaignID != expectedCampaignID {
		return RenderBackflow{}, ErrMotion
	}
	in.CampaignID = expectedCampaignID

	if in.ProjectID == "" || in.RevisionID == "" || in.Digest == "" {
		return RenderBackflow{}, ErrMotion
	}
	if !digestRe.MatchString(in.Digest) {
		return RenderBackflow{}, ErrDigest
	}
	if in.ProjectID != expectedDoc.Motion.ProjectID || in.RevisionID != expectedDoc.Motion.RevisionID || in.Digest != expectedDoc.Motion.Digest {
		return RenderBackflow{}, ErrProjectMismatch
	}
	// Channel must be one of the handoff's declared channels.
	channelAllowed := false
	for _, ch := range expectedDoc.Channels {
		if ch == in.Channel {
			channelAllowed = true
			break
		}
	}
	if !channelAllowed {
		return RenderBackflow{}, ErrChannel
	}
	if in.BudgetAttributionID == "" {
		in.BudgetAttributionID = expectedDoc.BudgetAttributionID
	} else if in.BudgetAttributionID != expectedDoc.BudgetAttributionID {
		return RenderBackflow{}, ErrBudgetExceeded
	}
	if in.PayerRef != "" && !tokenRe.MatchString(in.PayerRef) {
		return RenderBackflow{}, ErrBudgetExceeded
	}
	if expectedDoc.Payer != nil && in.PayerRef != "" && in.PayerRef != expectedDoc.Payer.PayerAccountID {
		return RenderBackflow{}, ErrBudgetExceeded
	}

	if in.RenderMode == "" {
		in.RenderMode = RenderModeParametricVariant
	}
	if in.RenderMode != RenderModeParametricVariant && in.RenderMode != RenderModeBaseline {
		return RenderBackflow{}, ErrInvalid
	}
	if in.HighCostModelCalls < 0 || in.BilledGenerationCount < 0 || in.CostCents < 0 {
		return RenderBackflow{}, ErrInvalid
	}

	// Non-linear high-cost model call guardrails:
	// - Parametric variant renders on a fixed project/revision must NOT invoke
	//   high-cost original model generation or bill an original generation pass.
	// - Across all variants of one project/revision, total high-cost model calls
	//   must never exceed 1 (O(1), never O(N) with channel count).
	if in.RenderMode == RenderModeParametricVariant {
		if in.HighCostModelCalls > 0 || in.BilledGenerationCount > 0 {
			return RenderBackflow{}, ErrLinearModelCallSpike
		}
	}
	if acc.ExistingTotalHighCostCalls+in.HighCostModelCalls > 1 {
		return RenderBackflow{}, ErrLinearModelCallSpike
	}

	// Duplicate billing receipt guard.
	if in.BillingReceiptID != "" {
		if !tokenRe.MatchString(in.BillingReceiptID) {
			return RenderBackflow{}, ErrInvalid
		}
		targetKey := in.CreativeID + "|" + in.VariantID
		if prevTarget, exists := acc.ExistingReceiptIDs[in.BillingReceiptID]; exists && prevTarget != targetKey {
			return RenderBackflow{}, ErrDuplicateCharge
		}
	}

	// Campaign budget cap guard.
	if expectedDoc.Budget != nil && expectedDoc.Budget.MaxBudgetCents > 0 {
		if acc.ExistingTotalCostCents+in.CostCents > expectedDoc.Budget.MaxBudgetCents {
			return RenderBackflow{}, ErrBudgetExceeded
		}
	}

	return in, nil
}

// AttributionSummary is the Leads-authoritative read summary for campaign
// motion renders and conversion metrics. It is NEVER written back to a creative project.
type AttributionSummary struct {
	Authority                    string `json:"authority"`
	CreativeProjectWrites        int    `json:"creative_project_writes"`
	TotalRenders                 int    `json:"total_renders"`
	UniqueChannels               int    `json:"unique_channels"`
	AcceptedCreatives            int    `json:"accepted_creatives"`
	TotalHighCostModelCalls      int    `json:"total_high_cost_model_calls"`
	TotalCostCents               int64  `json:"total_cost_cents"`
	CostPerAcceptedCreativeCents *int64 `json:"cost_per_accepted_creative_cents"`
	CostPerChannelVariantCents   *int64 `json:"cost_per_channel_variant_cents"`
}

// RenderMetricItem is the minimal projection used to calculate internal cost metrics.
type RenderMetricItem struct {
	Channel            string
	Status             string
	HighCostModelCalls int
	CostCents          int64
}

// ComputeAttributionSummary computes internal cost-per-accepted-creative and
// cost-per-channel-variant metrics with nullable pointers when denominator is 0.
func ComputeAttributionSummary(items []RenderMetricItem) AttributionSummary {
	summary := AttributionSummary{
		Authority:             "leads_only",
		CreativeProjectWrites: 0,
		TotalRenders:          len(items),
	}
	channels := map[string]bool{}
	for _, it := range items {
		if it.Channel != "" {
			channels[it.Channel] = true
		}
		if it.Status == "accepted" {
			summary.AcceptedCreatives++
		}
		summary.TotalHighCostModelCalls += it.HighCostModelCalls
		summary.TotalCostCents += it.CostCents
	}
	summary.UniqueChannels = len(channels)
	if summary.AcceptedCreatives > 0 {
		v := summary.TotalCostCents / int64(summary.AcceptedCreatives)
		summary.CostPerAcceptedCreativeCents = &v
	}
	if summary.UniqueChannels > 0 {
		v := summary.TotalCostCents / int64(summary.UniqueChannels)
		summary.CostPerChannelVariantCents = &v
	}
	return summary
}
