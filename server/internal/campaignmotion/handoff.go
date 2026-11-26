// Package campaignmotion builds the leads-side handoff for one campaign.
//
// 只交出活动目标、受众、已确认的品牌或产品事实、已授权素材 id、CTA、渠道名和预算归属 id。
// 聊天、电话、邮箱、未授权线索和客户隐私默认不进交出的 JSON。
// Motion 引用只有 project_id、revision_id、campaign_id、digest。
// 本包不调用模型，不按渠道次数生成，也不写创作工程。转化留在获客侧。
package campaignmotion

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const Notice = "只记下交给动效工程的活动事实。没有生成成片，也没有送出成片。"

var (
	ErrInvalid = errors.New("campaignmotion: invalid")
	ErrDigest  = errors.New("campaignmotion: digest")
	ErrMotion  = errors.New("campaignmotion: motion")
	ErrChannel = errors.New("campaignmotion: channel")
)

var (
	digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	phoneRe  = regexp.MustCompile(`(?:\+?86[-\s]?)?1[3-9]\d{9}`)
	emailRe  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	tokenRe  = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

var allowedTop = map[string]bool{
	"campaign_goal":          true,
	"target_audience":        true,
	"approved_facts":         true,
	"approved_brand_facts":   true,
	"approved_product_facts": true,
	"facts":                  true,
	"assets":                 true,
	"approved_assets":        true,
	"authorized_asset_ids":   true,
	"cta":                    true,
	"channels":               true,
	"target_channels":        true,
	"channel_variants":       true,
	"budget_attribution_id":  true,
	"budget":                 true,
	"payer":                  true,
	"return_url":             true,
	"source_facts_version":   true,
	"expires_at":             true,
	"tenant_id":              true,
	"execution_preference":   true,
	"motion":                 true,
	"variant_binding":        true,
}

var sensitiveKey = map[string]bool{
	"phone": true, "email": true, "mobile": true, "tel": true,
	"chat": true, "chats": true, "chat_record": true, "transcript": true,
	"message": true, "messages": true, "text": true, "conversation": true,
	"lead": true, "leads": true, "unauthorized_leads": true, "unauthorized_lead": true,
	"privacy": true, "customer_privacy": true, "profile": true, "crm": true, "crm_profile": true,
	"customer": true, "name": true, "address": true, "wechat": true, "id_card": true,
	"sales_note": true, "note": true, "contact": true, "contacts": true,
	"prompt": true, "render": true, "timeline": true, "source": true, "ast": true,
	"body": true, "video": true, "url": true, "renderer": true,
}

var forbiddenFactKey = map[string]bool{
	"phone": true, "email": true, "mobile": true, "tel": true,
	"chat": true, "chats": true, "chat_record": true, "transcript": true,
	"message": true, "messages": true, "conversation": true,
	"lead": true, "leads": true, "unauthorized_leads": true,
	"privacy": true, "customer_privacy": true, "profile": true, "crm": true,
	"customer": true, "name": true, "address": true, "wechat": true, "id_card": true,
	"sales_note": true, "note": true, "contact": true, "contacts": true,
}

var motionKey = map[string]bool{
	"project_id": true, "revision_id": true, "campaign_id": true, "digest": true,
	"tenant_id": true, "expires_at": true,
}

var exportKey = map[string]bool{
	"campaign_goal": true, "target_audience": true,
	"approved_facts": true, "approved_brand_facts": true, "approved_product_facts": true,
	"key": true, "value": true,
	"authorized_asset_ids": true, "approved_assets": true, "id": true, "version": true, "expires_at": true,
	"cta": true, "channels": true, "target_channels": true,
	"budget_attribution_id": true, "budget": true, "max_budget_cents": true,
	"payer": true, "payer_kind": true, "payer_account_id": true,
	"return_url": true, "source_facts_version": true,
	"variants": true, "channel": true,
	"aspect_ratio": true, "layout_preset": true, "cta_override": true, "store_label": true, "asset_slot_overrides": true,
	"motion": true, "project_id": true, "revision_id": true, "campaign_id": true, "digest": true,
	"variant_id": true, "origin_context_ref": true, "execution_preference": true,
}

// ApprovedFact is one confirmed brand or product fact.
type ApprovedFact struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Variant records a channel name plus, when a variant binding or parametric
// channel diff was supplied, the per-channel variant facts: a motionref-shaped
// variant id, the sealed digest, the return anchor back to this campaign, and
// strictly parametric differences (never prompts, timelines, or model calls).
type Variant struct {
	Channel            string            `json:"channel"`
	VariantID          string            `json:"variant_id,omitempty"`
	Digest             string            `json:"digest,omitempty"`
	OriginContextRef   string            `json:"origin_context_ref,omitempty"`
	AspectRatio        string            `json:"aspect_ratio,omitempty"`
	LayoutPreset       string            `json:"layout_preset,omitempty"`
	CTAOverride        string            `json:"cta_override,omitempty"`
	StoreLabel         string            `json:"store_label,omitempty"`
	AssetSlotOverrides map[string]string `json:"asset_slot_overrides,omitempty"`
}

// MotionRef is the only Motion citation this handoff may carry.
type MotionRef struct {
	ProjectID  string `json:"project_id"`
	RevisionID string `json:"revision_id"`
	CampaignID string `json:"campaign_id"`
	Digest     string `json:"digest"`
}

// Document is the JSON handed out of leads. Conversion metrics are not on it.
type Document struct {
	CampaignGoal         string             `json:"campaign_goal"`
	TargetAudience       string             `json:"target_audience"`
	ApprovedFacts        []ApprovedFact     `json:"approved_facts"`
	ApprovedBrandFacts   []ApprovedFact     `json:"approved_brand_facts,omitempty"`
	ApprovedProductFacts []ApprovedFact     `json:"approved_product_facts,omitempty"`
	AuthorizedAssetIDs   []string           `json:"authorized_asset_ids"`
	ApprovedAssets       []ApprovedAssetRef `json:"approved_assets,omitempty"`
	CTA                  string             `json:"cta"`
	Channels             []string           `json:"channels"`
	TargetChannels       []string           `json:"target_channels,omitempty"`
	BudgetAttributionID  string             `json:"budget_attribution_id"`
	Budget               *BudgetSpec        `json:"budget,omitempty"`
	Payer                *PayerSpec         `json:"payer,omitempty"`
	ReturnURL            string             `json:"return_url,omitempty"`
	SourceFactsVersion   string             `json:"source_facts_version,omitempty"`
	// ExecutionPreference is a named preference only (immediate/priority/night/idle
	// and C0 aliases night_batch/free_idle/paid_elastic). No numeric priority or
	// internal privilege is ever accepted or exported.
	ExecutionPreference string    `json:"execution_preference,omitempty"`
	Variants            []Variant `json:"variants"`
	Motion              MotionRef `json:"motion"`
}

// Conversion stays on the leads side. It is not written to a creative project.
type Conversion struct {
	Leads        int64 `json:"leads"`
	Conversions  int64 `json:"conversions"`
	RevenueCents int64 `json:"revenue_cents"`
}

// Result is one leads-side handoff. ModelCalls and CreativeProjectWrites stay zero.
type Result struct {
	Document              Document
	Export                []byte
	Local                 Conversion
	ModelCalls            int
	CreativeProjectWrites int
	PieceGenerated        bool
	PieceSent             bool
	Notice                string
}

// CreativeProjectWriter is the creative-project port. Prepare does not call it.
type CreativeProjectWriter interface {
	WriteCreativeProject(projectID, revisionID string, export []byte) error
}

type factCandidate struct {
	key   string
	value string
}

// Prepare builds the export from the raw request JSON for the caller's session
// tenant. writer may be non-nil; this slice still does not call it and does not
// call a model. Channel names are recorded once each. The call count does not
// follow the channel count.
func Prepare(tenantID, campaignID string, raw []byte, writer CreativeProjectWriter) (Result, error) {
	_ = writer
	now := time.Now().UTC()
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || !tokenRe.MatchString(tenantID) {
		return Result{}, ErrInvalid
	}
	campaignID = strings.TrimSpace(campaignID)
	if !tokenRe.MatchString(campaignID) {
		return Result{}, ErrInvalid
	}
	obj, err := decodeObject(raw)
	if err != nil {
		return Result{}, ErrInvalid
	}
	// Reject any global compute/queue/cluster resource configuration upfront.
	var rawTree any
	if err := json.Unmarshal(raw, &rawTree); err != nil {
		return Result{}, ErrInvalid
	}
	if err := ValidateNoGlobalResourceConfig(rawTree); err != nil {
		return Result{}, err
	}
	// Fail-closed on wrong tenant_id declared in request payload.
	if rawTenant, ok := obj["tenant_id"]; ok && !isNull(rawTenant) {
		declaredTenant, err := jsonString(rawTenant)
		if err != nil || declaredTenant == "" {
			return Result{}, ErrInvalid
		}
		if declaredTenant != tenantID {
			return Result{}, ErrForeignTenant
		}
	}
	// Fail-closed on expired top-level handoff timestamp.
	if rawExp, ok := obj["expires_at"]; ok && !isNull(rawExp) {
		expTime, err := parseRFC3339Field(rawExp)
		if err != nil {
			return Result{}, ErrInvalid
		}
		if !now.Before(expTime) {
			return Result{}, ErrVariantExpired
		}
	}

	var secrets []string
	for key, value := range obj {
		if allowedTop[key] {
			continue
		}
		collectSecrets(value, strings.ToLower(key), &secrets)
	}
	local := parseConversion(obj)

	facts, secrets, err := parseFacts(obj, secrets)
	if err != nil {
		return Result{}, err
	}
	brandFacts, err := parseStrictScopedFacts(obj["approved_brand_facts"])
	if err != nil {
		return Result{}, err
	}
	productFacts, err := parseStrictScopedFacts(obj["approved_product_facts"])
	if err != nil {
		return Result{}, err
	}
	assets, secrets, err := parseAssets(obj, secrets)
	if err != nil {
		return Result{}, err
	}
	approvedAssets, approvedAssetIDs, secrets, err := parseApprovedAssets(obj["approved_assets"], now, secrets)
	if err != nil {
		return Result{}, err
	}
	assets = append(assets, approvedAssetIDs...)

	goal, err := requiredText(obj, "campaign_goal", secrets)
	if err != nil {
		return Result{}, err
	}
	audience, err := optionalText(obj, "target_audience", secrets)
	if err != nil {
		return Result{}, err
	}
	cta, err := optionalText(obj, "cta", secrets)
	if err != nil {
		return Result{}, err
	}
	budgetSpec, err := parseBudgetSpec(obj["budget"], secrets)
	if err != nil {
		return Result{}, err
	}
	budget, err := resolveBudgetAttribution(obj, budgetSpec, secrets)
	if err != nil {
		return Result{}, err
	}
	payerSpec, err := parsePayerSpec(obj["payer"], secrets)
	if err != nil {
		return Result{}, err
	}
	returnURL, err := parseReturnURLField(obj["return_url"], secrets)
	if err != nil {
		return Result{}, err
	}
	sourceFactsVersion, err := optionalToken(obj, "source_facts_version", secrets)
	if err != nil {
		return Result{}, err
	}
	channels, hasTargetChannels, err := parseAllChannels(obj["channels"], obj["target_channels"], secrets)
	if err != nil {
		return Result{}, err
	}
	motion, err := parseMotion(obj["motion"], tenantID, campaignID, now)
	if err != nil {
		return Result{}, err
	}
	binding, err := parseVariantBinding(obj["variant_binding"], secrets)
	if err != nil {
		return Result{}, err
	}

	authorized := scrubAssets(assets, secrets)
	if _, ok := obj["priority"]; ok {
		return Result{}, ErrInvalid
	}
	if _, ok := obj["requested_priority"]; ok {
		return Result{}, ErrInvalid
	}
	preference, err := parseExecutionPreference(obj["execution_preference"])
	if err != nil {
		return Result{}, err
	}

	channelDiffs, err := parseChannelVariantDiffs(obj["channel_variants"], authorized)
	if err != nil {
		return Result{}, err
	}
	// Ensure any channel referenced only in channel_variants is also covered or rejected.
	if len(channelDiffs) > 0 {
		chSet := make(map[string]bool, len(channels))
		for _, ch := range channels {
			chSet[ch] = true
		}
		for ch := range channelDiffs {
			if !chSet[ch] {
				return Result{}, ErrChannel
			}
		}
	}

	doc := Document{
		CampaignGoal:         goal,
		TargetAudience:       audience,
		ApprovedFacts:        scrubFacts(facts, secrets),
		ApprovedBrandFacts:   brandFacts,
		ApprovedProductFacts: productFacts,
		AuthorizedAssetIDs:   authorized,
		ApprovedAssets:       approvedAssets,
		CTA:                  cta,
		Channels:             channels,
		BudgetAttributionID:  budget,
		Budget:               budgetSpec,
		Payer:                payerSpec,
		ReturnURL:            returnURL,
		SourceFactsVersion:   sourceFactsVersion,
		ExecutionPreference:  preference,
		Variants:             variantsFrom(channels),
		Motion:               motion,
	}
	if hasTargetChannels {
		doc.TargetChannels = append([]string(nil), channels...)
	}
	if doc.ApprovedFacts == nil {
		doc.ApprovedFacts = []ApprovedFact{}
	}
	if doc.AuthorizedAssetIDs == nil {
		doc.AuthorizedAssetIDs = []string{}
	}
	if binding != nil {
		// A binding turns each channel into a motionref-shaped variant claim
		// owned by the session tenant. Missing receiver, foreign-tenant runs,
		// and expired bindings are refused; the digest self-check refuses an
		// unbound shape. The tenant is the caller's session tenant, never a
		// request field.
		outs, err := BuildVariants(VariantSpec{
			CampaignID:      campaignID,
			TenantID:        tenantID,
			OwnerAccountID:  binding.ownerAccountID,
			OwnerTenantID:   binding.ownerTenantID,
			ProjectID:       motion.ProjectID,
			RevisionID:      motion.RevisionID,
			Channels:        channels,
			AuthorizedAsset: doc.AuthorizedAssetIDs,
			ExpiresAt:       binding.expiresAt,
			Now:             now,
		})
		if err != nil {
			return Result{}, err
		}
		doc.Variants = doc.Variants[:0]
		for _, o := range outs {
			doc.Variants = append(doc.Variants, Variant{
				Channel:          o.Channel,
				VariantID:        o.VariantID,
				Digest:           o.Digest,
				OriginContextRef: o.OriginContextRef,
			})
		}
	}
	if len(channelDiffs) > 0 {
		for i := range doc.Variants {
			if diff, ok := channelDiffs[doc.Variants[i].Channel]; ok {
				doc.Variants[i].AspectRatio = diff.AspectRatio
				doc.Variants[i].LayoutPreset = diff.LayoutPreset
				doc.Variants[i].CTAOverride = diff.CTAOverride
				doc.Variants[i].StoreLabel = diff.StoreLabel
				doc.Variants[i].AssetSlotOverrides = diff.AssetSlotOverrides
			}
		}
	}
	export, err := json.Marshal(doc)
	if err != nil {
		return Result{}, ErrInvalid
	}
	if err := assertExportClean(export, secrets); err != nil {
		return Result{}, err
	}
	return Result{
		Document:              doc,
		Export:                export,
		Local:                 local,
		ModelCalls:            0,
		CreativeProjectWrites: 0,
		PieceGenerated:        false,
		PieceSent:             false,
		Notice:                Notice,
	}, nil
}

// variantBinding is the optional per-campaign motion receiver declaration. The
// tenant is the session tenant; owner_tenant_id may only repeat it.
type variantBinding struct {
	ownerAccountID string
	ownerTenantID  string
	expiresAt      *time.Time
}

// parseVariantBinding reads the optional binding object. Unknown keys fail;
// missing fields stay empty so BuildVariants can refuse them by name.
func parseVariantBinding(raw json.RawMessage, secrets []string) (*variantBinding, error) {
	if isNull(raw) || len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, ErrInvalid
	}
	for key := range obj {
		switch key {
		case "owner_account_id", "owner_tenant_id", "expires_at":
		default:
			return nil, ErrInvalid
		}
	}
	out := &variantBinding{}
	if raw, ok := obj["owner_account_id"]; ok && !isNull(raw) {
		s, err := bindingString(raw, secrets)
		if err != nil {
			return nil, err
		}
		out.ownerAccountID = s
	}
	if raw, ok := obj["owner_tenant_id"]; ok && !isNull(raw) {
		s, err := bindingString(raw, secrets)
		if err != nil {
			return nil, err
		}
		out.ownerTenantID = s
	}
	if raw, ok := obj["expires_at"]; ok && !isNull(raw) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, ErrInvalid
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
		if err != nil {
			return nil, ErrInvalid
		}
		out.expiresAt = &t
	}
	return out, nil
}

func bindingString(raw json.RawMessage, secrets []string) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrInvalid
	}
	s = scrub(strings.TrimSpace(s), secrets)
	if s != "" && !tokenRe.MatchString(s) {
		return "", ErrInvalid
	}
	return s, nil
}

func variantsFrom(channels []string) []Variant {
	out := make([]Variant, 0, len(channels))
	for _, channel := range channels {
		out = append(out, Variant{Channel: channel})
	}
	return out
}

func decodeObject(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing json")
	}
	if obj == nil {
		return nil, errors.New("null")
	}
	return obj, nil
}

func parseConversion(obj map[string]json.RawMessage) Conversion {
	var c Conversion
	if raw, ok := obj["conversion"]; ok {
		_ = json.Unmarshal(raw, &c)
	}
	return c
}

func parseFacts(obj map[string]json.RawMessage, secrets []string) ([]factCandidate, []string, error) {
	var out []factCandidate
	for _, key := range []string{"approved_facts", "facts"} {
		raw, ok := obj[key]
		if !ok || isNull(raw) {
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, secrets, ErrInvalid
		}
		for _, item := range items {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(item, &fields); err != nil {
				return nil, secrets, ErrInvalid
			}
			for field, value := range fields {
				if field == "key" || field == "value" || field == "confirmed" {
					continue
				}
				collectSecrets(value, strings.ToLower(field), &secrets)
			}
			factKey, err := fieldString(fields, "key")
			if err != nil {
				return nil, secrets, err
			}
			factValue, err := fieldString(fields, "value")
			if err != nil {
				return nil, secrets, err
			}
			confirmed, err := fieldBool(fields, "confirmed")
			if err != nil {
				return nil, secrets, err
			}
			factKey = strings.TrimSpace(factKey)
			factValue = strings.TrimSpace(factValue)
			if factKey == "" {
				continue
			}
			if !confirmed || forbiddenFactKey[strings.ToLower(factKey)] {
				remember(&secrets, factValue)
				remember(&secrets, factKey)
				continue
			}
			out = append(out, factCandidate{key: factKey, value: factValue})
		}
	}
	return out, secrets, nil
}

func parseAssets(obj map[string]json.RawMessage, secrets []string) ([]string, []string, error) {
	var ids []string
	if raw, ok := obj["authorized_asset_ids"]; ok && !isNull(raw) {
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, secrets, ErrInvalid
		}
		ids = append(ids, list...)
	}
	raw, ok := obj["assets"]
	if !ok || isNull(raw) {
		return ids, secrets, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, secrets, ErrInvalid
	}
	for _, item := range items {
		var asString string
		if err := json.Unmarshal(item, &asString); err == nil {
			ids = append(ids, asString)
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			return nil, secrets, ErrInvalid
		}
		for field, value := range fields {
			if field == "id" || field == "authorized" {
				continue
			}
			collectSecrets(value, strings.ToLower(field), &secrets)
		}
		id, err := fieldString(fields, "id")
		if err != nil {
			return nil, secrets, err
		}
		authorized, err := fieldBool(fields, "authorized")
		if err != nil {
			return nil, secrets, err
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !authorized {
			remember(&secrets, id)
			continue
		}
		ids = append(ids, id)
	}
	return ids, secrets, nil
}

func parseAllChannels(rawChannels, rawTargetChannels json.RawMessage, secrets []string) ([]string, bool, error) {
	var combined []string
	hasTarget := false
	if !isNull(rawChannels) && len(bytes.TrimSpace(rawChannels)) > 0 {
		var list []string
		if err := json.Unmarshal(rawChannels, &list); err != nil {
			return nil, false, ErrChannel
		}
		combined = append(combined, list...)
	}
	if !isNull(rawTargetChannels) && len(bytes.TrimSpace(rawTargetChannels)) > 0 {
		var list []string
		if err := json.Unmarshal(rawTargetChannels, &list); err != nil {
			return nil, false, ErrChannel
		}
		hasTarget = true
		combined = append(combined, list...)
	}
	if len(combined) == 0 {
		return nil, false, ErrChannel
	}
	seen := map[string]bool{}
	var out []string
	for _, channel := range combined {
		channel = scrub(channel, secrets)
		if channel == "" || seen[channel] || !validChannel(channel) {
			continue
		}
		seen[channel] = true
		out = append(out, channel)
	}
	if len(out) == 0 {
		return nil, false, ErrChannel
	}
	return out, hasTarget, nil
}

func parseStrictScopedFacts(raw json.RawMessage) ([]ApprovedFact, error) {
	if isNull(raw) || len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, ErrInvalid
	}
	var out []ApprovedFact
	seen := map[string]bool{}
	for _, item := range items {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			return nil, ErrInvalid
		}
		for field := range fields {
			if field != "key" && field != "value" && field != "confirmed" {
				if sensitiveKey[strings.ToLower(field)] || forbiddenFactKey[strings.ToLower(field)] {
					return nil, ErrPrivacyBlocked
				}
				return nil, ErrInvalid
			}
		}
		factKey, err := fieldString(fields, "key")
		if err != nil {
			return nil, err
		}
		factValue, err := fieldString(fields, "value")
		if err != nil {
			return nil, err
		}
		confirmed, err := fieldBool(fields, "confirmed")
		if err != nil {
			return nil, err
		}
		factKey = strings.TrimSpace(factKey)
		factValue = strings.TrimSpace(factValue)
		if factKey == "" || factValue == "" {
			return nil, ErrInvalid
		}
		if !confirmed {
			return nil, ErrPrivacyBlocked
		}
		if forbiddenFactKey[strings.ToLower(factKey)] || sensitiveKey[strings.ToLower(factKey)] {
			return nil, ErrPrivacyBlocked
		}
		if ContainsPrivacyLeak(factKey) || ContainsPrivacyLeak(factValue) {
			return nil, ErrPrivacyBlocked
		}
		if utf8.RuneCountInString(factKey) > 40 || utf8.RuneCountInString(factValue) > 500 {
			return nil, ErrInvalid
		}
		if seen[factKey] {
			continue
		}
		seen[factKey] = true
		out = append(out, ApprovedFact{Key: factKey, Value: factValue})
	}
	return out, nil
}

func parseApprovedAssets(raw json.RawMessage, now time.Time, secrets []string) ([]ApprovedAssetRef, []string, []string, error) {
	if isNull(raw) || len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil, secrets, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, secrets, ErrInvalid
	}
	var refs []ApprovedAssetRef
	var ids []string
	seen := map[string]bool{}
	for _, item := range items {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			return nil, nil, secrets, ErrInvalid
		}
		for field := range fields {
			switch field {
			case "id", "authorized", "version", "expires_at":
			default:
				if sensitiveKey[strings.ToLower(field)] {
					return nil, nil, secrets, ErrPrivacyBlocked
				}
				return nil, nil, secrets, ErrInvalid
			}
		}
		id, err := fieldString(fields, "id")
		if err != nil {
			return nil, nil, secrets, err
		}
		authorized, err := fieldBool(fields, "authorized")
		if err != nil {
			return nil, nil, secrets, err
		}
		version, err := fieldString(fields, "version")
		if err != nil {
			return nil, nil, secrets, err
		}
		id = strings.TrimSpace(id)
		version = strings.TrimSpace(version)
		if !authorized {
			remember(&secrets, id)
			return nil, nil, secrets, ErrPrivacyBlocked
		}
		if id == "" || !tokenRe.MatchString(id) || ContainsPrivacyLeak(id) {
			return nil, nil, secrets, ErrInvalid
		}
		if version != "" && (!tokenRe.MatchString(version) || ContainsPrivacyLeak(version)) {
			return nil, nil, secrets, ErrInvalid
		}
		expStr := ""
		if rawExp, ok := fields["expires_at"]; ok && !isNull(rawExp) {
			expTime, err := parseRFC3339Field(rawExp)
			if err != nil {
				return nil, nil, secrets, ErrInvalid
			}
			if !now.Before(expTime) {
				return nil, nil, secrets, ErrVariantExpired
			}
			expStr = expTime.UTC().Format(time.RFC3339)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		refs = append(refs, ApprovedAssetRef{ID: id, Version: version, ExpiresAt: expStr})
		ids = append(ids, id)
	}
	return refs, ids, secrets, nil
}

func parseBudgetSpec(raw json.RawMessage, secrets []string) (*BudgetSpec, error) {
	if isNull(raw) || len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, ErrInvalid
	}
	for key := range obj {
		switch key {
		case "budget_attribution_id", "max_budget_cents":
		default:
			if sensitiveKey[strings.ToLower(key)] {
				return nil, ErrPrivacyBlocked
			}
			return nil, ErrInvalid
		}
	}
	spec := &BudgetSpec{}
	if rawID, ok := obj["budget_attribution_id"]; ok && !isNull(rawID) {
		s, err := jsonString(rawID)
		if err != nil || !tokenRe.MatchString(s) || ContainsPrivacyLeak(s) {
			return nil, ErrInvalid
		}
		for _, secret := range secrets {
			if secret != "" && strings.Contains(s, secret) {
				return nil, ErrPrivacyBlocked
			}
		}
		spec.BudgetAttributionID = s
	}
	if rawMax, ok := obj["max_budget_cents"]; ok && !isNull(rawMax) {
		var cents int64
		if err := json.Unmarshal(rawMax, &cents); err != nil || cents < 0 {
			return nil, ErrInvalid
		}
		spec.MaxBudgetCents = cents
	}
	return spec, nil
}

func resolveBudgetAttribution(obj map[string]json.RawMessage, budgetSpec *BudgetSpec, secrets []string) (string, error) {
	if raw, ok := obj["budget_attribution_id"]; ok && !isNull(raw) {
		topID, err := requiredToken(obj, "budget_attribution_id", secrets)
		if err != nil {
			return "", err
		}
		if budgetSpec != nil {
			if budgetSpec.BudgetAttributionID == "" {
				budgetSpec.BudgetAttributionID = topID
			} else if budgetSpec.BudgetAttributionID != topID {
				return "", ErrInvalid
			}
		}
		return topID, nil
	}
	if budgetSpec != nil && budgetSpec.BudgetAttributionID != "" {
		return budgetSpec.BudgetAttributionID, nil
	}
	return "", ErrInvalid
}

func parsePayerSpec(raw json.RawMessage, secrets []string) (*PayerSpec, error) {
	if isNull(raw) || len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, ErrInvalid
	}
	for key := range obj {
		switch key {
		case "payer_kind", "payer_account_id":
		default:
			if sensitiveKey[strings.ToLower(key)] {
				return nil, ErrPrivacyBlocked
			}
			return nil, ErrInvalid
		}
	}
	kind, err := fieldString(obj, "payer_kind")
	if err != nil {
		return nil, err
	}
	acct, err := fieldString(obj, "payer_account_id")
	if err != nil {
		return nil, err
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	acct = strings.TrimSpace(acct)
	if (kind != "organization" && kind != "authorized") || !tokenRe.MatchString(acct) || ContainsPrivacyLeak(acct) {
		return nil, ErrBudgetExceeded
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(acct, secret) {
			return nil, ErrPrivacyBlocked
		}
	}
	return &PayerSpec{PayerKind: kind, PayerAccountID: acct}, nil
}

func parseReturnURLField(raw json.RawMessage, secrets []string) (string, error) {
	if isNull(raw) || len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrInvalid
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(s, secret) {
			return "", ErrPrivacyBlocked
		}
	}
	return ValidateReturnURL(s)
}

func optionalToken(obj map[string]json.RawMessage, key string, secrets []string) (string, error) {
	raw, ok := obj[key]
	if !ok || isNull(raw) {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrInvalid
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if !tokenRe.MatchString(s) || ContainsPrivacyLeak(s) {
		return "", ErrInvalid
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(s, secret) {
			return "", ErrPrivacyBlocked
		}
	}
	return s, nil
}

func parseChannelVariantDiffs(raw json.RawMessage, authorizedAssets []string) (map[string]VariantParamDiff, error) {
	if isNull(raw) || len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, ErrInvalid
	}
	out := make(map[string]VariantParamDiff, len(items))
	for _, item := range items {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil || fields == nil {
			return nil, ErrInvalid
		}
		for key := range fields {
			switch key {
			case "channel", "aspect_ratio", "layout_preset", "cta_override", "store_label", "asset_slot_overrides":
			case "prompt", "model", "model_call", "regenerate", "original_generation", "high_cost_model_calls", "billing_charge":
				return nil, ErrLinearModelCallSpike
			default:
				if sensitiveKey[strings.ToLower(key)] {
					return nil, ErrPrivacyBlocked
				}
				return nil, ErrInvalid
			}
		}
		var diff VariantParamDiff
		if err := json.Unmarshal(item, &diff); err != nil {
			return nil, ErrInvalid
		}
		clean, err := ValidateParametricVariantDiff(diff, authorizedAssets)
		if err != nil {
			return nil, err
		}
		if _, dup := out[clean.Channel]; dup {
			return nil, ErrInvalid
		}
		out[clean.Channel] = clean
	}
	return out, nil
}

func parseRFC3339Field(raw json.RawMessage) (time.Time, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return time.Time{}, ErrInvalid
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, ErrInvalid
	}
	return t.UTC(), nil
}

func parseMotion(raw json.RawMessage, tenantID, campaignID string, now time.Time) (MotionRef, error) {
	if isNull(raw) || len(bytes.TrimSpace(raw)) == 0 {
		return MotionRef{}, ErrMotion
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return MotionRef{}, ErrMotion
	}
	for key := range obj {
		if !motionKey[key] {
			return MotionRef{}, ErrMotion
		}
	}
	if rawTenant, ok := obj["tenant_id"]; ok && !isNull(rawTenant) {
		mt, err := jsonString(rawTenant)
		if err != nil || mt == "" {
			return MotionRef{}, ErrMotion
		}
		if mt != tenantID {
			return MotionRef{}, ErrForeignTenant
		}
	}
	if rawExp, ok := obj["expires_at"]; ok && !isNull(rawExp) {
		expTime, err := parseRFC3339Field(rawExp)
		if err != nil {
			return MotionRef{}, ErrMotion
		}
		if !now.Before(expTime) {
			return MotionRef{}, ErrVariantExpired
		}
	}
	project, err := motionString(obj, "project_id")
	if err != nil {
		return MotionRef{}, err
	}
	revision, err := motionString(obj, "revision_id")
	if err != nil {
		return MotionRef{}, err
	}
	// 空字符串配上合法 digest 也不能交出。tokenRe 同时要求非空且只含现有记号。
	if project == "" || revision == "" || !tokenRe.MatchString(project) || !tokenRe.MatchString(revision) {
		return MotionRef{}, ErrMotion
	}
	if rawCampaign, ok := obj["campaign_id"]; ok {
		requested, err := jsonString(rawCampaign)
		if err != nil || requested != campaignID {
			return MotionRef{}, ErrMotion
		}
	}
	digest, err := motionString(obj, "digest")
	if err != nil {
		return MotionRef{}, err
	}
	if !digestRe.MatchString(digest) {
		return MotionRef{}, ErrDigest
	}
	// 导出的 campaign_id 只用路径 id，不用请求里的另一个 id。
	return MotionRef{ProjectID: project, RevisionID: revision, CampaignID: campaignID, Digest: digest}, nil
}

func jsonString(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func motionString(obj map[string]json.RawMessage, key string) (string, error) {
	raw, ok := obj[key]
	if !ok {
		return "", ErrMotion
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrMotion
	}
	return strings.TrimSpace(s), nil
}

func scrubFacts(facts []factCandidate, secrets []string) []ApprovedFact {
	var out []ApprovedFact
	seen := map[string]bool{}
	for _, fact := range facts {
		key := scrub(fact.key, secrets)
		value := scrub(fact.value, secrets)
		if key == "" || value == "" || forbiddenFactKey[strings.ToLower(key)] || seen[key] {
			continue
		}
		if utf8.RuneCountInString(key) > 40 || utf8.RuneCountInString(value) > 500 {
			continue
		}
		if phoneRe.MatchString(key+value) || emailRe.MatchString(key+value) {
			continue
		}
		seen[key] = true
		out = append(out, ApprovedFact{Key: key, Value: value})
	}
	return out
}

func scrubAssets(ids, secrets []string) []string {
	var out []string
	seen := map[string]bool{}
	blocked := map[string]bool{}
	for _, secret := range secrets {
		blocked[secret] = true
	}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || blocked[id] || !tokenRe.MatchString(id) {
			continue
		}
		if phoneRe.MatchString(id) || emailRe.MatchString(id) {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func requiredText(obj map[string]json.RawMessage, key string, secrets []string) (string, error) {
	text, err := optionalText(obj, key, secrets)
	if err != nil || text == "" {
		return "", ErrInvalid
	}
	return text, nil
}

func optionalText(obj map[string]json.RawMessage, key string, secrets []string) (string, error) {
	raw, ok := obj[key]
	if !ok || isNull(raw) {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrInvalid
	}
	s = scrub(s, secrets)
	if utf8.RuneCountInString(s) > 500 {
		return "", ErrInvalid
	}
	if phoneRe.MatchString(s) || emailRe.MatchString(s) {
		return "", ErrInvalid
	}
	return s, nil
}

func requiredToken(obj map[string]json.RawMessage, key string, secrets []string) (string, error) {
	raw, ok := obj[key]
	if !ok {
		return "", ErrInvalid
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrInvalid
	}
	s = scrub(s, secrets)
	if !tokenRe.MatchString(s) || phoneRe.MatchString(s) || emailRe.MatchString(s) {
		return "", ErrInvalid
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(s, secret) {
			return "", ErrInvalid
		}
	}
	return s, nil
}

func validChannel(s string) bool {
	n := utf8.RuneCountInString(s)
	if n < 1 || n > 40 {
		return false
	}
	if strings.ContainsAny(s, "@\n\r\t") || phoneRe.MatchString(s) || emailRe.MatchString(s) {
		return false
	}
	return true
}

func scrub(s string, secrets []string) string {
	ordered := append([]string(nil), secrets...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, secret := range ordered {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "")
	}
	s = emailRe.ReplaceAllString(s, "")
	s = phoneRe.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

func assertExportClean(export []byte, secrets []string) error {
	var parsed any
	if err := json.Unmarshal(export, &parsed); err != nil {
		return ErrInvalid
	}
	if err := ValidateNoGlobalResourceConfig(parsed); err != nil {
		return err
	}
	keys := map[string]struct{}{}
	if err := walkExportKeys(parsed, keys); err != nil {
		return err
	}
	for key := range keys {
		if !exportKey[key] {
			return ErrInvalid
		}
	}
	text := string(export)
	if phoneRe.MatchString(text) || emailRe.MatchString(text) || idCardRe.MatchString(text) {
		return ErrPrivacyBlocked
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			return ErrInvalid
		}
	}
	return nil
}

func walkExportKeys(v any, keys map[string]struct{}) error {
	switch t := v.(type) {
	case map[string]any:
		for key, child := range t {
			keys[key] = struct{}{}
			if key == "asset_slot_overrides" {
				slots, ok := child.(map[string]any)
				if !ok {
					return ErrInvalid
				}
				for slot := range slots {
					if !tokenRe.MatchString(slot) || sensitiveKey[strings.ToLower(slot)] || forbiddenFactKey[strings.ToLower(slot)] {
						return ErrPrivacyBlocked
					}
				}
				continue
			}
			if err := walkExportKeys(child, keys); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range t {
			if err := walkExportKeys(child, keys); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectSecrets(raw json.RawMessage, parent string, into *[]string) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return
	}
	walkSecrets(v, parent, into)
}

func walkSecrets(v any, parent string, into *[]string) {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return
		}
		if sensitiveKey[parent] || utf8.RuneCountInString(s) >= 12 || phoneRe.MatchString(s) || emailRe.MatchString(s) {
			remember(into, s)
		}
	case map[string]any:
		for key, child := range t {
			walkSecrets(child, strings.ToLower(key), into)
		}
	case []any:
		for _, child := range t {
			walkSecrets(child, parent, into)
		}
	}
}

func remember(into *[]string, s string) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) < 4 {
		return
	}
	*into = append(*into, s)
}

func fieldString(fields map[string]json.RawMessage, key string) (string, error) {
	raw, ok := fields[key]
	if !ok || isNull(raw) {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrInvalid
	}
	return s, nil
}

func fieldBool(fields map[string]json.RawMessage, key string) (bool, error) {
	raw, ok := fields[key]
	if !ok || isNull(raw) {
		return false, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, ErrInvalid
	}
	return b, nil
}

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// parseExecutionPreference accepts only canonical MX-OPS / C0 named preferences.
// A client cannot declare a numeric priority or internal privilege.
func parseExecutionPreference(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err != nil {
		return "", ErrInvalid
	}
	_, wire, err := NormalizeExecutionPreference(text)
	if err != nil {
		return "", err
	}
	return wire, nil
}
