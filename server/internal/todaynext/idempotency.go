package todaynext

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MutationContext describes a follow-up or next-action write attempt coming from
// either the Today view or the Lead Detail view.
type MutationContext struct {
	TenantID         string
	BodyTenantID     string
	PrincipalID      string
	MemberID         string
	MemberRole       string
	MemberEnabled    bool
	RecordTenantID   string
	RecordID         string
	RecordAssigneeID string
	ExpectedOwnerID  string
	IdempotencyKey   string
	PayloadDigest    string
	// ViewSource records the caller surface ("today" / "lead_detail") for
	// observability only; it is intentionally excluded from the dedupe tuple so
	// Today and Lead Detail share a single idempotency domain per record.
	ViewSource string
}

// CachedWriteResult is the committed follow-up write outcome stored under a
// (tenant_id, principal_id, record_id, idempotency_key) tuple.
type CachedWriteResult struct {
	FollowUpID    string
	TodayGroup    string
	PayloadDigest string
	CreatedAt     time.Time
}

// GuardDecision is the fail-closed decision returned by FollowUpIdempotencyGuard.Begin.
type GuardDecision struct {
	Allowed    bool
	Replay     bool
	Conflict   bool
	HTTPStatus int
	Code       string
	Message    string
	Cached     *CachedWriteResult
}

type idempotencyKeyTuple struct {
	tenantID       string
	principalID    string
	recordID       string
	idempotencyKey string
}

type inflightEntry struct {
	done chan struct{}
}

// WriteLease represents an active in-flight write lock for one idempotency tuple.
// Callers MUST call either Commit or Abort (typically `defer lease.Abort()`).
type WriteLease struct {
	guard  *FollowUpIdempotencyGuard
	key    idempotencyKeyTuple
	flight *inflightEntry
	digest string
	once   sync.Once
}

// Commit publishes the persisted write result and wakes any concurrent duplicate callers.
func (l *WriteLease) Commit(res CachedWriteResult) {
	if l == nil || l.guard == nil {
		return
	}
	l.once.Do(func() {
		if res.PayloadDigest == "" {
			res.PayloadDigest = l.digest
		}
		if res.CreatedAt.IsZero() {
			res.CreatedAt = time.Now().UTC()
		}
		l.guard.mu.Lock()
		l.guard.completed[l.key] = res
		if cur, ok := l.guard.inflight[l.key]; ok && cur == l.flight {
			delete(l.guard.inflight, l.key)
		}
		l.guard.mu.Unlock()
		close(l.flight.done)
	})
}

// Abort releases the in-flight reservation without caching a result so a failed
// write can be retried cleanly. Safe to call after Commit (no-op).
func (l *WriteLease) Abort() {
	if l == nil || l.guard == nil {
		return
	}
	l.once.Do(func() {
		l.guard.mu.Lock()
		if cur, ok := l.guard.inflight[l.key]; ok && cur == l.flight {
			delete(l.guard.inflight, l.key)
		}
		l.guard.mu.Unlock()
		close(l.flight.done)
	})
}

// FollowUpIdempotencyGuard deduplicates retries and concurrent double-clicks
// across the Today view and Lead Detail view on (tenant_id, principal_id,
// record_id, idempotency_key), while failing closed BEFORE any cache lookup on
// cross-tenant writes, disabled members, and reassigned records.
type FollowUpIdempotencyGuard struct {
	mu        sync.Mutex
	completed map[idempotencyKeyTuple]CachedWriteResult
	inflight  map[idempotencyKeyTuple]*inflightEntry
}

// NewFollowUpIdempotencyGuard constructs an empty thread-safe guard.
func NewFollowUpIdempotencyGuard() *FollowUpIdempotencyGuard {
	return &FollowUpIdempotencyGuard{
		completed: make(map[idempotencyKeyTuple]CachedWriteResult),
		inflight:  make(map[idempotencyKeyTuple]*inflightEntry),
	}
}

// CanonicalPayloadDigest computes a deterministic SHA-256 digest of normalized write inputs.
func CanonicalPayloadDigest(parts ...string) string {
	norm := make([]string, len(parts))
	for i, p := range parts {
		norm[i] = strings.TrimSpace(p)
	}
	sum := sha256.Sum256([]byte(strings.Join(norm, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// ScopedDedupeKey computes the tenant/principal/record-scoped DB dedupe key
// shared by Today follow-through and Lead Detail follow-up writes.
func ScopedDedupeKey(tenantID, principalID, recordID, idempotencyKey string) string {
	return CanonicalPayloadDigest("todaynext-idem-v1", tenantID, principalID, recordID, idempotencyKey)
}

// Begin validates tenant/member/ownership boundaries first (fail-closed), then
// resolves idempotency for (tenant_id, principal_id, record_id, idempotency_key).
func (g *FollowUpIdempotencyGuard) Begin(ctx MutationContext) (GuardDecision, *WriteLease) {
	if d, ok := validateMutationBoundary(ctx); !ok {
		return d, nil
	}
	idemKey := strings.TrimSpace(ctx.IdempotencyKey)
	if idemKey == "" {
		return GuardDecision{Allowed: true, HTTPStatus: http.StatusCreated, Code: "ok"}, nil
	}

	tuple := idempotencyKeyTuple{
		tenantID:       strings.TrimSpace(ctx.TenantID),
		principalID:    strings.TrimSpace(ctx.PrincipalID),
		recordID:       strings.TrimSpace(ctx.RecordID),
		idempotencyKey: idemKey,
	}
	digest := strings.TrimSpace(ctx.PayloadDigest)

	for {
		g.mu.Lock()
		if cached, exists := g.completed[tuple]; exists {
			g.mu.Unlock()
			if digest != "" && cached.PayloadDigest != "" && cached.PayloadDigest != digest {
				return GuardDecision{
					Allowed:    false,
					Conflict:   true,
					HTTPStatus: http.StatusConflict,
					Code:       "idempotency_conflict",
					Message:    "idempotency_key was already used with a different follow-up payload",
				}, nil
			}
			cp := cached
			return GuardDecision{
				Allowed:    true,
				Replay:     true,
				HTTPStatus: http.StatusOK,
				Code:       "replay",
				Cached:     &cp,
			}, nil
		}
		if flight, busy := g.inflight[tuple]; busy {
			g.mu.Unlock()
			<-flight.done
			continue
		}
		flight := &inflightEntry{done: make(chan struct{})}
		g.inflight[tuple] = flight
		g.mu.Unlock()
		return GuardDecision{
				Allowed:    true,
				Replay:     false,
				HTTPStatus: http.StatusCreated,
				Code:       "ok",
			}, &WriteLease{
				guard:  g,
				key:    tuple,
				flight: flight,
				digest: digest,
			}
	}
}

func validateMutationBoundary(ctx MutationContext) (GuardDecision, bool) {
	tenantID := strings.TrimSpace(ctx.TenantID)
	bodyTenantID := strings.TrimSpace(ctx.BodyTenantID)
	recordTenantID := strings.TrimSpace(ctx.RecordTenantID)
	principalID := strings.TrimSpace(ctx.PrincipalID)
	memberID := strings.TrimSpace(ctx.MemberID)
	recordID := strings.TrimSpace(ctx.RecordID)
	role := strings.TrimSpace(strings.ToLower(ctx.MemberRole))
	assignee := strings.TrimSpace(ctx.RecordAssigneeID)
	expectedOwner := strings.TrimSpace(ctx.ExpectedOwnerID)

	if tenantID == "" || principalID == "" || memberID == "" || recordID == "" {
		return GuardDecision{
			Allowed:    false,
			HTTPStatus: http.StatusBadRequest,
			Code:       "invalid_mutation_context",
			Message:    "tenant, principal, member, and record identifiers are required",
		}, false
	}
	if bodyTenantID != "" && bodyTenantID != tenantID {
		return GuardDecision{
			Allowed:    false,
			HTTPStatus: http.StatusForbidden,
			Code:       "cross_tenant",
			Message:    "tenant mismatch detected; stale tenant mutation rejected",
		}, false
	}
	if recordTenantID != "" && recordTenantID != tenantID {
		return GuardDecision{
			Allowed:    false,
			HTTPStatus: http.StatusForbidden,
			Code:       "cross_tenant",
			Message:    "record belongs to another tenant",
		}, false
	}
	if !ctx.MemberEnabled {
		return GuardDecision{
			Allowed:    false,
			HTTPStatus: http.StatusForbidden,
			Code:       "member_disabled",
			Message:    "disabled member cannot mutate follow-ups",
		}, false
	}
	if role != "owner" && (assignee == "" || assignee != memberID) {
		return GuardDecision{
			Allowed:    false,
			HTTPStatus: http.StatusNotFound,
			Code:       "record_reassigned",
			Message:    "record is not assigned to this member",
		}, false
	}
	if expectedOwner != "" && expectedOwner != assignee {
		return GuardDecision{
			Allowed:    false,
			Conflict:   true,
			HTTPStatus: http.StatusConflict,
			Code:       "record_reassigned",
			Message:    "record ownership changed since the view was loaded",
		}, false
	}
	return GuardDecision{}, true
}
