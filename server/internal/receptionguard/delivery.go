package receptionguard

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// ReplyStage enforces the three-stage delivery lifecycle:
// prepared -> approved -> delivered (or terminal blocked / superseded).
type ReplyStage string

const (
	StagePrepared   ReplyStage = "prepared"
	StageApproved   ReplyStage = "approved"
	StageDelivered  ReplyStage = "delivered"
	StageBlocked    ReplyStage = "blocked"
	StageSuperseded ReplyStage = "superseded"
)

// EntryPoint identifies the caller/channel entry triggering delivery.
type EntryPoint string

const (
	EntryH5           EntryPoint = "h5"
	EntryCRMDesk      EntryPoint = "crm_desk"
	EntryCloudBot     EntryPoint = "cloud_bot"
	EntryDigitalHuman EntryPoint = "digital_human"
)

var (
	ErrReplyNotFound    = errors.New("receptionguard: reply not found")
	ErrDeliveryConflict = errors.New("receptionguard: delivery idempotency key conflict")
	ErrNotApproved      = errors.New("receptionguard: reply must be approved before delivery")
	ErrInvalidReplyKey  = errors.New("receptionguard: tenant_id, session_id, reply_id, and epoch>=1 are required")
)

// DeliveryKey uniquely identifies a reply delivery attempt across all entries
// (H5, CRM desk, Cloud Bot, Digital Human) to guarantee idempotent delivery
// and single billing.
type DeliveryKey struct {
	TenantID  string
	SessionID string
	ReplyID   string
	Epoch     int
}

func (k DeliveryKey) valid() bool {
	return strings.TrimSpace(k.TenantID) != "" &&
		strings.TrimSpace(k.SessionID) != "" &&
		strings.TrimSpace(k.ReplyID) != "" &&
		k.Epoch >= 1
}

// ReplyRecord tracks a reply across the prepared -> approved -> delivered stages.
type ReplyRecord struct {
	TenantID             string
	SessionID            string
	ReplyID              string
	Epoch                int
	Stage                ReplyStage
	Source               OutboundSource
	Body                 string
	Language             string
	AuthorID             string
	ApproverID           string
	ApproverIsOwner      bool
	HumanAuthored        bool
	Citations            []KnowledgeCitation
	EntryPointsDelivered []EntryPoint
	EmitCount            int
	BillCount            int
	PreparedAt           time.Time
	ApprovedAt           time.Time
	DeliveredAt          time.Time
	BlockReason          string
}

// Key returns the canonical deduplication key for this reply record.
func (r ReplyRecord) Key() DeliveryKey {
	return DeliveryKey{
		TenantID:  strings.TrimSpace(r.TenantID),
		SessionID: strings.TrimSpace(r.SessionID),
		ReplyID:   strings.TrimSpace(r.ReplyID),
		Epoch:     r.Epoch,
	}
}

// DeliveryResult summarizes a Deliver call, separating logical success from
// actual physical emission and billing.
type DeliveryResult struct {
	Record       ReplyRecord
	Delivered    bool
	Duplicate    bool
	ShouldEmit   bool
	BillingUnits int
	BlockReason  string
}

// DeliveryLedger enforces 3-stage reply transitions and cross-entry idempotent
// deduplication by (tenant_id, session_id, reply_id, epoch).
type DeliveryLedger struct {
	mu      sync.Mutex
	records map[DeliveryKey]ReplyRecord
}

// NewDeliveryLedger constructs an empty DeliveryLedger.
func NewDeliveryLedger() *DeliveryLedger {
	return &DeliveryLedger{
		records: make(map[DeliveryKey]ReplyRecord),
	}
}

// Prepare inserts a draft reply in StagePrepared.
// Repeating Prepare with the same key and identical body is idempotent.
// Repeating Prepare with the same key and a different body fails with ErrDeliveryConflict.
func (l *DeliveryLedger) Prepare(rec ReplyRecord, now time.Time) (ReplyRecord, bool, error) {
	rec.TenantID = strings.TrimSpace(rec.TenantID)
	rec.SessionID = strings.TrimSpace(rec.SessionID)
	rec.ReplyID = strings.TrimSpace(rec.ReplyID)
	rec.Body = strings.TrimSpace(rec.Body)
	key := rec.Key()
	if !key.valid() || rec.Body == "" {
		return ReplyRecord{}, false, ErrInvalidReplyKey
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if existing, ok := l.records[key]; ok {
		if existing.Body != rec.Body {
			return cloneReplyRecord(existing), false, ErrDeliveryConflict
		}
		return cloneReplyRecord(existing), true, nil
	}

	rec.Stage = StagePrepared
	rec.PreparedAt = now.UTC()
	rec.EmitCount = 0
	rec.BillCount = 0
	rec.EntryPointsDelivered = nil
	rec.BlockReason = ""
	if rec.Source == "" {
		rec.Source = SourceAI
	}
	if rec.Source == SourceHuman {
		rec.HumanAuthored = true
	}
	l.records[key] = cloneReplyRecord(rec)
	return cloneReplyRecord(rec), false, nil
}

// Approve transitions a prepared reply to StageApproved after verifying the
// session fence is still open and on the exact same epoch.
func (l *DeliveryLedger) Approve(key DeliveryKey, approverID string, isTenantOwner bool, fence *SessionFence, now time.Time) (ReplyRecord, error) {
	if !key.valid() {
		return ReplyRecord{}, ErrInvalidReplyKey
	}
	if fence == nil {
		return ReplyRecord{}, ErrOutboundBlocked
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.records[key]
	if !ok {
		return ReplyRecord{}, ErrReplyNotFound
	}
	if rec.Stage == StageApproved || rec.Stage == StageDelivered {
		return cloneReplyRecord(rec), nil
	}
	if rec.Stage == StageBlocked || rec.Stage == StageSuperseded {
		return cloneReplyRecord(rec), ErrOutboundBlocked
	}

	snap := fence.Snapshot()
	if snap.TenantID != key.TenantID {
		return cloneReplyRecord(rec), ErrTenantMismatch
	}
	if snap.SessionID != key.SessionID {
		return cloneReplyRecord(rec), ErrSessionMismatch
	}
	if snap.Closed {
		rec.Stage = StageBlocked
		rec.BlockReason = "session_closed"
		l.records[key] = rec
		return cloneReplyRecord(rec), ErrSessionClosed
	}
	if key.Epoch < snap.Epoch {
		rec.Stage = StageSuperseded
		rec.BlockReason = "stale_epoch"
		l.records[key] = rec
		return cloneReplyRecord(rec), ErrStaleEpoch
	}
	if key.Epoch > snap.Epoch {
		return cloneReplyRecord(rec), ErrInvalidEpoch
	}
	approverID = strings.TrimSpace(approverID)
	if snap.Mode == ModeHumanTakeover {
		if !isTenantOwner && (approverID == "" || approverID != snap.OwnerID) {
			return cloneReplyRecord(rec), ErrTakeoverConflict
		}
		if rec.Source != SourceHuman && !rec.HumanAuthored {
			rec.Stage = StageBlocked
			rec.BlockReason = "human_takeover_active"
			l.records[key] = rec
			return cloneReplyRecord(rec), ErrHumanTakeoverActive
		}
	}

	rec.Stage = StageApproved
	rec.ApproverID = approverID
	rec.ApproverIsOwner = isTenantOwner
	rec.ApprovedAt = now.UTC()
	l.records[key] = rec
	return cloneReplyRecord(rec), nil
}

// Deliver transitions an approved reply to StageDelivered exactly once per
// (tenant_id, session_id, reply_id, epoch).
//
// Cross-entry replays (H5, CRM desk, Cloud Bot, Digital Human) for an already
// delivered key return Duplicate=true, ShouldEmit=false, BillingUnits=0, and
// never double-send or double-bill.
func (l *DeliveryLedger) Deliver(key DeliveryKey, entry EntryPoint, channel OutboundChannel, fence *SessionFence, now time.Time) (DeliveryResult, error) {
	if !key.valid() {
		return DeliveryResult{}, ErrInvalidReplyKey
	}
	if fence == nil {
		return DeliveryResult{}, ErrOutboundBlocked
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.records[key]
	if !ok {
		return DeliveryResult{}, ErrReplyNotFound
	}

	// Idempotent replay across entries: already delivered once -> zero emit, zero bill.
	if rec.Stage == StageDelivered {
		if entry != "" && !containsEntry(rec.EntryPointsDelivered, entry) {
			rec.EntryPointsDelivered = append(rec.EntryPointsDelivered, entry)
			l.records[key] = rec
		}
		return DeliveryResult{
			Record:       cloneReplyRecord(rec),
			Delivered:    true,
			Duplicate:    true,
			ShouldEmit:   false,
			BillingUnits: 0,
		}, nil
	}

	// Enforce 3-stage separation: prepared drafts cannot skip approval.
	if rec.Stage == StagePrepared {
		return DeliveryResult{
			Record:      cloneReplyRecord(rec),
			Delivered:   false,
			ShouldEmit:  false,
			BlockReason: "approval_required",
		}, ErrNotApproved
	}
	if rec.Stage == StageBlocked || rec.Stage == StageSuperseded {
		return DeliveryResult{
			Record:      cloneReplyRecord(rec),
			Delivered:   false,
			ShouldEmit:  false,
			BlockReason: rec.BlockReason,
		}, ErrOutboundBlocked
	}

	actor := rec.ApproverID
	if actor == "" {
		actor = rec.AuthorID
	}
	dec := fence.AuthorizeOutbound(OutboundRequest{
		TenantID:      rec.TenantID,
		SessionID:     rec.SessionID,
		ReplyID:       rec.ReplyID,
		ReplyEpoch:    rec.Epoch,
		Channel:       channel,
		Source:        rec.Source,
		ActorID:       actor,
		IsTenantOwner: rec.ApproverIsOwner,
		Approved:      rec.Stage == StageApproved,
		HumanAuthored: rec.HumanAuthored,
	})
	if !dec.Allowed {
		if errors.Is(dec.Err, ErrStaleEpoch) {
			rec.Stage = StageSuperseded
		} else {
			rec.Stage = StageBlocked
		}
		rec.BlockReason = dec.Reason
		l.records[key] = rec
		err := dec.Err
		if err == nil {
			err = ErrOutboundBlocked
		}
		return DeliveryResult{
			Record:       cloneReplyRecord(rec),
			Delivered:    false,
			Duplicate:    false,
			ShouldEmit:   false,
			BillingUnits: 0,
			BlockReason:  dec.Reason,
		}, err
	}

	rec.Stage = StageDelivered
	rec.DeliveredAt = now.UTC()
	rec.EmitCount++
	rec.BillCount++
	if entry != "" {
		rec.EntryPointsDelivered = append(rec.EntryPointsDelivered, entry)
	}
	l.records[key] = rec
	return DeliveryResult{
		Record:       cloneReplyRecord(rec),
		Delivered:    true,
		Duplicate:    false,
		ShouldEmit:   true,
		BillingUnits: 1,
	}, nil
}

// SupersedePriorEpochs invalidates all unsent records (prepared/approved) for a
// session whose epoch is strictly less than newEpoch.
func (l *DeliveryLedger) SupersedePriorEpochs(tenantID, sessionID string, newEpoch int) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	count := 0
	for key, rec := range l.records {
		if key.TenantID != tenantID || key.SessionID != sessionID || key.Epoch >= newEpoch {
			continue
		}
		if rec.Stage == StagePrepared || rec.Stage == StageApproved {
			rec.Stage = StageSuperseded
			rec.BlockReason = "stale_epoch"
			l.records[key] = rec
			count++
		}
	}
	return count
}

// Get fetches a copy of the ReplyRecord for key.
func (l *DeliveryLedger) Get(key DeliveryKey) (ReplyRecord, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[key]
	if !ok {
		return ReplyRecord{}, false
	}
	return cloneReplyRecord(rec), true
}

func containsEntry(entries []EntryPoint, target EntryPoint) bool {
	for _, e := range entries {
		if e == target {
			return true
		}
	}
	return false
}

func cloneReplyRecord(r ReplyRecord) ReplyRecord {
	if len(r.Citations) > 0 {
		c := make([]KnowledgeCitation, len(r.Citations))
		copy(c, r.Citations)
		r.Citations = c
	}
	if len(r.EntryPointsDelivered) > 0 {
		e := make([]EntryPoint, len(r.EntryPointsDelivered))
		copy(e, r.EntryPointsDelivered)
		r.EntryPointsDelivered = e
	}
	return r
}
