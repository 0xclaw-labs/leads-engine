package lightcopy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SampleCampaign = "campaign"
	SampleProduct  = "product"
	SampleService  = "service"

	FactStatusConfirmed          = "confirmed"
	FactStatusUnknown            = "unknown"
	FactStatusNeedsClarification = "needs_clarification"
)

var (
	placeholderWordRe = regexp.MustCompile(`(?i)\b(TBD|TODO|PLACEHOLDER)\b`)
	emailRe           = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	cnMobileRe        = regexp.MustCompile(`(?:\+?86[\s\-]?)?1[3-9]\d[\s\-]?\d{4}[\s\-]?\d{4}`)
	longDigitRunRe    = regexp.MustCompile(`\d{8,}`)
	cnIDCardRe        = regexp.MustCompile(`\b\d{17}[\dXx]\b`)
	safeRefRe         = regexp.MustCompile(`^[A-Za-z0-9_.:/\-]{1,128}$`)
)

var sensitiveNoteMarkers = []string{
	"内部备注",
	"内部销售备注",
	"销售备注",
	"内部底价",
	"联系人全文",
	"客户全文",
	"未授权画像",
	"手机号列表",
	"crm_profile",
	"sales_note",
	"internal_note",
	"contact_fulltext",
}

// ContainsSensitivePII reports whether text contains phone numbers/lists,
// emails, Chinese ID card numbers, or internal sales note / full-contact markers.
func ContainsSensitivePII(text string) (bool, string) {
	if strings.TrimSpace(text) == "" {
		return false, ""
	}
	if cnMobileRe.MatchString(text) {
		return true, "phone_number"
	}
	if emailRe.MatchString(text) {
		return true, "email"
	}
	if cnIDCardRe.MatchString(text) {
		return true, "id_card"
	}
	if longDigitRunRe.MatchString(text) {
		return true, "phone_or_numeric_id"
	}
	lower := strings.ToLower(text)
	for _, marker := range sensitiveNoteMarkers {
		if strings.Contains(lower, marker) {
			return true, "internal_or_profile_marker:" + marker
		}
	}
	return false, ""
}

// StripSensitivePII removes detected PII tokens (phone numbers, emails, ID
// numbers, and internal note clauses) from a brief fragment. Callers in strict
// mode should reject via ContainsSensitivePII; StripSensitivePII is an
// additional defense-in-depth sanitizer when strip mode is explicitly chosen.
func StripSensitivePII(text string) (string, bool) {
	out := text
	changed := false
	for _, re := range []*regexp.Regexp{cnMobileRe, emailRe, cnIDCardRe, longDigitRunRe} {
		if re.MatchString(out) {
			out = re.ReplaceAllString(out, "")
			changed = true
		}
	}
	lines := strings.Split(out, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		lower := strings.ToLower(line)
		drop := false
		for _, marker := range sensitiveNoteMarkers {
			if strings.Contains(lower, marker) {
				drop = true
				changed = true
				break
			}
		}
		if !drop {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				kept = append(kept, trimmed)
			}
		}
	}
	cleaned := strings.TrimSpace(strings.Join(kept, "\n"))
	return cleaned, changed
}

// ConfirmedFact is a caller-asserted minimal fact item for in-scene light copy
// or restricted professional handoff.
type ConfirmedFact struct {
	Key        string `json:"key"`
	Value      string `json:"value"`
	PriceCents *int64 `json:"price_cents,omitempty"`
	ValidUntil string `json:"valid_until,omitempty"`
	Confirmed  bool   `json:"confirmed"`
	Authorized bool   `json:"authorized"`
}

// AuthorizedAsset is a reference to an authorized media/brand asset that may
// accompany a restricted professional handoff. Raw customer portraits without
// grant references are rejected.
type AuthorizedAsset struct {
	AssetID    string `json:"asset_id"`
	AssetKind  string `json:"asset_kind,omitempty"`
	GrantRef   string `json:"grant_ref"`
	LicenseRef string `json:"license_ref,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

// FactAssessment describes why a non-confirmed or expired fact was excluded
// from Facts and marked unknown / needs_clarification.
type FactAssessment struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// BriefInput holds the inputs to ProjectBrief.
type BriefInput struct {
	CallerTenant        string
	Subject             Subject
	Selected            []string
	Facts               []ConfirmedFact
	AuthorizedAssets    []AuthorizedAsset
	SampleCategory      string
	AllowUnknownMarking bool
	StripPII            bool
}

// BriefProjection is the minimal, PII-free payload allowed to reach a text
// generation prompt or a restricted professional tool handoff.
type BriefProjection struct {
	SubjectKind        string            `json:"subject_kind"`
	SubjectID          string            `json:"subject_id"`
	SampleCategory     string            `json:"sample_category,omitempty"`
	Facts              map[string]any    `json:"facts"`
	AuthorizedAssets   []AuthorizedAsset `json:"authorized_assets,omitempty"`
	UnknownFields      []string          `json:"unknown_fields,omitempty"`
	ClarificationItems []FactAssessment  `json:"clarification_items,omitempty"`
	InputFactsHash     string            `json:"input_facts_hash"`
	StrippedPII        bool              `json:"stripped_pii,omitempty"`
}

// ValidSampleCategory reports whether category is empty or one of campaign,
// product, or service.
func ValidSampleCategory(category string) bool {
	switch strings.TrimSpace(category) {
	case "", SampleCampaign, SampleProduct, SampleService:
		return true
	default:
		return false
	}
}

// ProjectBrief enforces the minimal confirmed-fact boundary, PII guard,
// authorized-asset check, and expired-price unknown/needs_clarification marking.
// Forbidden keys, cross-tenant requests, and ungranted assets fail closed.
func ProjectBrief(in BriefInput, now time.Time) (BriefProjection, string) {
	empty := BriefProjection{
		SubjectKind: in.Subject.Kind,
		SubjectID:   in.Subject.ID,
		Facts:       map[string]any{},
	}
	if in.CallerTenant == "" || in.CallerTenant != in.Subject.TenantID {
		return empty, ReasonTenant
	}
	category := strings.TrimSpace(in.SampleCategory)
	if !ValidSampleCategory(category) {
		return empty, ReasonUnauthorized
	}

	// Validate authorized assets first so unauthorized/expired media never slip through.
	assets := make([]AuthorizedAsset, 0, len(in.AuthorizedAssets))
	for _, a := range in.AuthorizedAssets {
		id := strings.TrimSpace(a.AssetID)
		grant := strings.TrimSpace(a.GrantRef)
		license := strings.TrimSpace(a.LicenseRef)
		kind := strings.TrimSpace(a.AssetKind)
		expires := strings.TrimSpace(a.ExpiresAt)
		if id == "" || grant == "" || !safeRefRe.MatchString(id) || !safeRefRe.MatchString(grant) {
			return empty, ReasonAssetUnauthorized
		}
		if license != "" && !safeRefRe.MatchString(license) {
			return empty, ReasonAssetUnauthorized
		}
		if hasPII, _ := ContainsSensitivePII(id + " " + grant + " " + license + " " + kind); hasPII {
			return empty, ReasonPIIDetected
		}
		if expires != "" {
			expTime, err := time.Parse(time.RFC3339, expires)
			if err != nil || !expTime.After(now) {
				return empty, ReasonAssetExpired
			}
		}
		assets = append(assets, AuthorizedAsset{
			AssetID:    id,
			AssetKind:  kind,
			GrantRef:   grant,
			LicenseRef: license,
			ExpiresAt:  expires,
		})
	}

	facts := map[string]any{}
	unknownSet := map[string]bool{}
	var clarifications []FactAssessment
	strippedAny := false

	addUnknown := func(key, status, reason string) {
		if !unknownSet[key] {
			unknownSet[key] = true
			clarifications = append(clarifications, FactAssessment{
				Key: key, Status: status, Reason: reason,
			})
		}
	}

	// 1. Legacy selected keys from Subject snapshot.
	if len(in.Selected) > 0 {
		for _, key := range in.Selected {
			if forbiddenFact[key] || !allowedFact[key] {
				return empty, ReasonUnauthorized
			}
		}
		for _, key := range in.Selected {
			switch key {
			case "price_cents":
				if in.Subject.PriceCents == nil || in.Subject.PriceValidUntil.IsZero() || !in.Subject.PriceValidUntil.After(now) {
					if in.AllowUnknownMarking {
						addUnknown("price_cents", FactStatusUnknown, ReasonPrice)
						continue
					}
					return empty, ReasonPrice
				}
				facts["price_cents"] = *in.Subject.PriceCents
			default:
				raw := subjectField(in.Subject, key)
				val := strings.TrimSpace(raw)
				if val == "" {
					if in.AllowUnknownMarking {
						addUnknown(key, FactStatusNeedsClarification, ReasonMissing)
						continue
					}
					return empty, ReasonMissing
				}
				if hasPII, _ := ContainsSensitivePII(val); hasPII {
					if !in.StripPII {
						return empty, ReasonPIIDetected
					}
					cleaned, changed := StripSensitivePII(val)
					if changed {
						strippedAny = true
					}
					if cleaned == "" {
						if in.AllowUnknownMarking {
							addUnknown(key, FactStatusNeedsClarification, ReasonPIIDetected)
							continue
						}
						return empty, ReasonPIIDetected
					}
					val = cleaned
				}
				facts[key] = val
			}
		}
	}

	// 2. Explicit structured ConfirmedFact entries (scene/question/offer/next_step/etc.).
	for _, f := range in.Facts {
		key := strings.TrimSpace(strings.ToLower(f.Key))
		if key == "" || forbiddenFact[key] || !allowedFact[key] {
			return empty, ReasonUnauthorized
		}
		if !f.Authorized {
			return empty, ReasonUnauthorized
		}
		if !f.Confirmed {
			if in.AllowUnknownMarking {
				addUnknown(key, FactStatusNeedsClarification, ReasonUnconfirmed)
				continue
			}
			return empty, ReasonUnconfirmed
		}
		if key == "price_cents" {
			if f.PriceCents == nil || *f.PriceCents < 0 || strings.TrimSpace(f.ValidUntil) == "" {
				if in.AllowUnknownMarking {
					addUnknown("price_cents", FactStatusUnknown, ReasonPrice)
					continue
				}
				return empty, ReasonPrice
			}
			validUntil, err := time.Parse(time.RFC3339, strings.TrimSpace(f.ValidUntil))
			if err != nil || !validUntil.After(now) {
				if in.AllowUnknownMarking {
					addUnknown("price_cents", FactStatusUnknown, ReasonPrice)
					continue
				}
				return empty, ReasonPrice
			}
			facts["price_cents"] = *f.PriceCents
			continue
		}
		// Optional expiry check on text facts (e.g. offer with expiry).
		if strings.TrimSpace(f.ValidUntil) != "" {
			validUntil, err := time.Parse(time.RFC3339, strings.TrimSpace(f.ValidUntil))
			if err != nil || !validUntil.After(now) {
				if in.AllowUnknownMarking {
					addUnknown(key, FactStatusUnknown, ReasonPrice)
					continue
				}
				return empty, ReasonPrice
			}
		}
		val := strings.TrimSpace(f.Value)
		if val == "" {
			if in.AllowUnknownMarking {
				addUnknown(key, FactStatusNeedsClarification, ReasonMissing)
				continue
			}
			return empty, ReasonMissing
		}
		if hasPII, _ := ContainsSensitivePII(val); hasPII {
			if !in.StripPII {
				return empty, ReasonPIIDetected
			}
			cleaned, changed := StripSensitivePII(val)
			if changed {
				strippedAny = true
			}
			if cleaned == "" {
				if in.AllowUnknownMarking {
					addUnknown(key, FactStatusNeedsClarification, ReasonPIIDetected)
					continue
				}
				return empty, ReasonPIIDetected
			}
			val = cleaned
		}
		facts[key] = val
	}

	if len(facts) == 0 && len(clarifications) == 0 {
		return empty, ReasonNothing
	}
	if len(facts) == 0 {
		// No confirmed facts remain at all (e.g. only expired price was provided).
		if unknownSet["price_cents"] {
			return empty, ReasonPrice
		}
		return empty, ReasonNothing
	}

	unknownFields := make([]string, 0, len(unknownSet))
	for k := range unknownSet {
		unknownFields = append(unknownFields, k)
	}
	sort.Strings(unknownFields)
	sort.Slice(clarifications, func(i, j int) bool {
		return clarifications[i].Key < clarifications[j].Key
	})

	proj := BriefProjection{
		SubjectKind:        in.Subject.Kind,
		SubjectID:          in.Subject.ID,
		SampleCategory:     category,
		Facts:              facts,
		AuthorizedAssets:   assets,
		UnknownFields:      unknownFields,
		ClarificationItems: clarifications,
		StrippedPII:        strippedAny,
	}
	proj.InputFactsHash = HashBriefProjection(proj)
	return proj, ""
}

func subjectField(sub Subject, key string) string {
	switch key {
	case "title":
		return sub.Title
	case "activity_ref":
		return sub.ActivityRef
	case "scene":
		return sub.Scene
	case "question":
		return sub.Question
	case "offer":
		return sub.Offer
	case "next_step":
		return sub.NextStep
	default:
		return ""
	}
}

// HashBriefProjection produces a deterministic SHA-256 hex digest of the
// projected facts, authorized assets, and unknown fields for auditability and
// idempotency keys.
func HashBriefProjection(proj BriefProjection) string {
	canon := struct {
		SubjectKind    string            `json:"subject_kind"`
		SubjectID      string            `json:"subject_id"`
		SampleCategory string            `json:"sample_category"`
		Facts          map[string]any    `json:"facts"`
		Assets         []AuthorizedAsset `json:"assets"`
		UnknownFields  []string          `json:"unknown_fields"`
	}{
		SubjectKind:    proj.SubjectKind,
		SubjectID:      proj.SubjectID,
		SampleCategory: proj.SampleCategory,
		Facts:          proj.Facts,
		Assets:         proj.AuthorizedAssets,
		UnknownFields:  proj.UnknownFields,
	}
	raw, _ := json.Marshal(canon)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// PromptFromBrief builds the deterministic generation instruction from a
// BriefProjection only. Unknown or expired fields are explicitly marked
// unknown/needs_clarification so the model never invents prices or placeholders.
func PromptFromBrief(kind, language string, brief BriefProjection) string {
	base := Prompt(kind, language, Packet{
		SubjectKind: brief.SubjectKind,
		SubjectID:   brief.SubjectID,
		Facts:       brief.Facts,
	})
	var b strings.Builder
	b.WriteString(base)
	if brief.SampleCategory != "" {
		b.WriteString("- sample_category: " + brief.SampleCategory + "\n")
	}
	if len(brief.AuthorizedAssets) > 0 {
		ids := make([]string, 0, len(brief.AuthorizedAssets))
		for _, a := range brief.AuthorizedAssets {
			ids = append(ids, a.AssetID)
		}
		sort.Strings(ids)
		b.WriteString("- authorized_assets: " + strings.Join(ids, ",") + "\n")
	}
	if len(brief.ClarificationItems) > 0 {
		b.WriteString("以下字段当前为未知或待澄清（严禁编造数字，严禁使用 {{}}、[待填写]、TBD 等模板占位，需诚实说明待核实）：\n")
		for _, item := range brief.ClarificationItems {
			fmt.Fprintf(&b, "- %s: status=%s reason=%s\n", item.Key, item.Status, item.Reason)
		}
	}
	return b.String()
}

// HandoffDispatchRequest is the minimal restricted brief sent to an existing
// professional production tool adapter (GoBoost / Swarm / DigitalHuman /
// ProductImage / AICut). Raw contact fulltext, phone lists, sales notes, and
// unauthorized profiles are never part of this struct.
type HandoffDispatchRequest struct {
	TenantID         string            `json:"tenant_id"`
	IdempotencyKey   string            `json:"idempotency_key"`
	Tool             string            `json:"tool"`
	SourceKind       string            `json:"source_kind"`
	SourceID         string            `json:"source_id"`
	DraftID          string            `json:"draft_id"`
	ContentVersion   int               `json:"content_version"`
	Facts            map[string]any    `json:"facts"`
	AuthorizedAssets []AuthorizedAsset `json:"authorized_assets,omitempty"`
	PermissionRef    string            `json:"permission_ref,omitempty"`
	PayerRef         string            `json:"payer_ref,omitempty"`
	QuoteExpiresAt   string            `json:"quote_expires_at,omitempty"`
}

// HandoffDispatchResult is the outcome returned by a configured ToolDispatcher.
type HandoffDispatchResult struct {
	Status           string `json:"status"`
	Reason           string `json:"reason"`
	TargetProjectRef string `json:"target_project_ref,omitempty"`
	ExternalTaskRef  string `json:"external_task_ref,omitempty"`
}

// ToolDispatcher is an optional seam to hand off a restricted brief to an
// existing ecosystem tool without rebuilding any engine inside CRM.
type ToolDispatcher interface {
	Dispatch(ctx context.Context, req HandoffDispatchRequest) HandoffDispatchResult
	QueryStatus(ctx context.Context, tenantID, idempotencyKey, tool string) HandoffDispatchResult
}

// ValidateSafeRef checks that an identifier reference (payer_ref, permission_ref,
// asset_ref, task_ref) is non-empty, canonical, and contains no PII.
func ValidateSafeRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || !safeRefRe.MatchString(ref) {
		return false
	}
	if hasPII, _ := ContainsSensitivePII(ref); hasPII {
		return false
	}
	return true
}

// ReturnCandidateInput is the controlled backflow payload from a professional
// tool back to the originating CRM task.
type ReturnCandidateInput struct {
	IdempotencyKey string `json:"idempotency_key"`
	AssetRef       string `json:"asset_ref"`
	TaskRef        string `json:"task_ref"`
	CandidateKind  string `json:"candidate_kind"`
	Summary        string `json:"summary"`
	ContentVersion int    `json:"content_version"`
}

// ValidateReturnCandidate validates a controlled asset/task backflow candidate.
// Backflow never accepts PII, empty refs, or template placeholders.
func ValidateReturnCandidate(in ReturnCandidateInput) (ReturnCandidateInput, string) {
	key := strings.TrimSpace(in.IdempotencyKey)
	assetRef := strings.TrimSpace(in.AssetRef)
	taskRef := strings.TrimSpace(in.TaskRef)
	kind := strings.TrimSpace(in.CandidateKind)
	summary := strings.TrimSpace(in.Summary)

	if key == "" || !safeRefRe.MatchString(key) {
		return ReturnCandidateInput{}, ReasonInvalidReturn
	}
	if assetRef == "" && taskRef == "" {
		return ReturnCandidateInput{}, ReasonInvalidReturn
	}
	if assetRef != "" && !ValidateSafeRef(assetRef) {
		return ReturnCandidateInput{}, ReasonInvalidReturn
	}
	if taskRef != "" && !ValidateSafeRef(taskRef) {
		return ReturnCandidateInput{}, ReasonInvalidReturn
	}
	if kind == "" {
		if assetRef != "" {
			kind = "asset"
		} else {
			kind = "task"
		}
	}
	switch kind {
	case "asset", "task", "copy_candidate", "poster_ref", "video_ref":
	default:
		return ReturnCandidateInput{}, ReasonInvalidReturn
	}
	if summary != "" {
		if hasPII, _ := ContainsSensitivePII(summary); hasPII {
			return ReturnCandidateInput{}, ReasonPIIDetected
		}
		if isPlaceholder(summary) {
			return ReturnCandidateInput{}, ReasonPlaceholder
		}
	}
	return ReturnCandidateInput{
		IdempotencyKey: key,
		AssetRef:       assetRef,
		TaskRef:        taskRef,
		CandidateKind:  kind,
		Summary:        summary,
		ContentVersion: in.ContentVersion,
	}, ""
}
