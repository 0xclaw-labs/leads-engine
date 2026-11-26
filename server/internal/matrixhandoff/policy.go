package matrixhandoff

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// Strict five-stage publish status stratification (HUI-2391 J4).
// Unknown or unverified upstream statuses are NEVER fabricated as published.
const (
	StageDraftSaved      = "draft_saved"
	StagePendingApproval = "pending_approval"
	StageScheduled       = "scheduled"
	StagePublished       = "published"
	StageUnknown         = "unknown"
)

var (
	// ErrWrongContentType rejects non-copy assets (image, video, needs_video, audio).
	// An image is not a video, needs_video is not success, and Leads never silently
	// generates, transcodes, or charges to coerce content types.
	ErrWrongContentType = errors.New("matrixhandoff: wrong content_type (only copy is accepted; no silent transcode/generate/charge)")
	// ErrLicenseExpired rejects expired asset licenses fail-closed before calling Matrix.
	ErrLicenseExpired = errors.New("matrixhandoff: asset license expired")
	// ErrStaleReceipt rejects out-of-order / older receipts so they never overwrite an adopted newer version.
	ErrStaleReceipt = errors.New("matrixhandoff: stale receipt cannot overwrite adopted newer version")
	// ErrInvalidMetric rejects negative metric counts (unknown must be nil/null, never negative or fabricated 0).
	ErrInvalidMetric = errors.New("matrixhandoff: negative metric is invalid")
)

var (
	// mainlandPhoneRe catches raw 11-digit mobile numbers smuggled into restricted reference fields.
	mainlandPhoneRe = regexp.MustCompile(`(^|[^0-9])1[3-9][0-9]{9}([^0-9]|$)`)
	// emailHintRe catches email addresses smuggled into restricted reference fields.
	emailHintRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
)

// ClassifyPublishStage maps a raw upstream or stored status into the strict
// five-stage stratification: draft_saved, pending_approval, scheduled,
// published, unknown.
//
// When officialPublishAuthorized is false (no verified official platform publish
// grant/receipt), an upstream "published"/"live" claim is refused fail-closed:
// it stays in StageDraftSaved with published=false.
func ClassifyPublishStage(rawStatus string, officialPublishAuthorized bool) (stage string, published bool) {
	switch strings.ToLower(strings.TrimSpace(rawStatus)) {
	case "draft", "draft_saved", "draft_recorded", "saved":
		return StageDraftSaved, false
	case "pending_approval", "awaiting_approval", "approval_pending", "reviewing":
		return StagePendingApproval, false
	case "scheduled", "queued":
		return StageScheduled, false
	case "published", "live":
		if !officialPublishAuthorized {
			return StageDraftSaved, false
		}
		return StagePublished, true
	case "revoked":
		return StageUnknown, false
	default:
		return StageUnknown, false
	}
}

// StageRank returns the monotonic progression rank of a publish stage.
// Unknown has rank 0 and never overwrites a known stage.
func StageRank(stage string) int {
	switch stage {
	case StageDraftSaved:
		return 1
	case StagePendingApproval:
		return 2
	case StageScheduled:
		return 3
	case StagePublished:
		return 4
	default:
		return 0
	}
}

// CanAdvanceStage reports whether transitioning from currentStage to nextStage
// is monotonic and does not downgrade an adopted stage.
func CanAdvanceStage(currentStage, nextStage string) bool {
	nextRank := StageRank(nextStage)
	if nextRank == 0 {
		return false
	}
	curRank := StageRank(currentStage)
	return nextRank >= curRank
}

// ValidateContentType enforces that Leads-to-Matrix marketing copy handoffs only
// accept "copy" (or empty default). Images, videos, and needs_video fail closed
// without silent generation, transcoding, or billing.
func ValidateContentType(contentType string) error {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if ct == "" || ct == "copy" {
		return nil
	}
	return ErrWrongContentType
}

// ValidateLicenseExpiry rejects already-expired asset licenses fail-closed.
func ValidateLicenseExpiry(expiresAt *time.Time, now time.Time) error {
	if expiresAt == nil {
		return nil
	}
	if !expiresAt.UTC().After(now.UTC()) {
		return ErrLicenseExpired
	}
	return nil
}

// ValidateRestrictedDeclaration ensures restricted reference/declaration fields
// do not smuggle phone numbers, emails, customer lists, or CRM internal notes.
func ValidateRestrictedDeclaration(val string) error {
	s := strings.TrimSpace(val)
	if s == "" {
		return nil
	}
	if mainlandPhoneRe.MatchString(s) || emailHintRe.MatchString(s) {
		return ErrForbidden
	}
	lower := strings.ToLower(s)
	for _, kw := range []string{
		"crm_profile", "crm_note", "internal_note", "customer_list",
		"phone_list", "客户档案", "手机号列表", "内部备注", "私信全文",
	} {
		if strings.Contains(lower, kw) {
			return ErrForbidden
		}
	}
	return nil
}

// ValidateMetrics ensures known metrics are non-negative integers while nil
// remains allowed to represent unknown (serialized as JSON null, never 0).
func ValidateMetrics(reach, likes, leads *int) error {
	for _, p := range []*int{reach, likes, leads} {
		if p != nil && *p < 0 {
			return ErrInvalidMetric
		}
	}
	return nil
}
