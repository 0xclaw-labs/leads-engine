// Package receptionguard implements the HUI-1688 2026-11-25 incremental
// hardening guards for unified reception:
//   - Mode & Epoch Fence (ai / ai_assist / human_takeover)
//   - Three-stage reply lifecycle (prepared / approved / delivered) & idempotent delivery
//   - Knowledge citation, live fact grounding, and non-fabrication validation
//   - Entrusted-order isolation & consent / no-forced-lead boundary
//   - Narrow, PII-free presentation projection for Digital Human (HUI-20) & Cloud Bot
//
// This package is pure domain logic with zero network or external I/O.
package receptionguard

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// Mode represents the reception session control mode.
type Mode string

const (
	ModeAI            Mode = "ai"
	ModeAIAssist      Mode = "ai_assist"
	ModeHumanTakeover Mode = "human_takeover"
)

// OutboundChannel identifies the outbound transport channel.
type OutboundChannel string

const (
	ChannelText         OutboundChannel = "text"
	ChannelAudio        OutboundChannel = "audio"
	ChannelCloudBot     OutboundChannel = "cloud_bot"
	ChannelDigitalHuman OutboundChannel = "digital_human"
	ChannelH5           OutboundChannel = "h5"
	ChannelCRMDesk      OutboundChannel = "crm_desk"
)

// OutboundSource identifies who generated an outbound reply candidate.
type OutboundSource string

const (
	SourceAI           OutboundSource = "ai"
	SourceHuman        OutboundSource = "human"
	SourceCloudBot     OutboundSource = "cloud_bot"
	SourceDigitalHuman OutboundSource = "digital_human"
)

var (
	ErrInvalidMode           = errors.New("receptionguard: invalid mode")
	ErrSessionClosed         = errors.New("receptionguard: session closed")
	ErrTenantMismatch        = errors.New("receptionguard: tenant mismatch")
	ErrSessionMismatch       = errors.New("receptionguard: session mismatch")
	ErrStaleEpoch            = errors.New("receptionguard: stale epoch")
	ErrInvalidEpoch          = errors.New("receptionguard: invalid epoch")
	ErrTakeoverConflict      = errors.New("receptionguard: takeover conflict")
	ErrNotHumanMode          = errors.New("receptionguard: session is not in human_takeover mode")
	ErrModeConflict          = errors.New("receptionguard: invalid mode transition")
	ErrUnauthorizedHandback  = errors.New("receptionguard: only current owner or tenant owner may hand back to AI")
	ErrHumanTakeoverActive   = errors.New("receptionguard: human takeover active")
	ErrApprovalRequired      = errors.New("receptionguard: human approval required")
	ErrImplicitResumeBlocked = errors.New("receptionguard: implicit auto-resume to AI is forbidden")
	ErrActorRequired         = errors.New("receptionguard: actor id is required")
	ErrOutboundBlocked       = errors.New("receptionguard: outbound blocked")
)

// NormalizeMode maps canonical and legacy reception mode strings to Mode.
func NormalizeMode(raw string) (Mode, bool) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "ai":
		return ModeAI, true
	case "ai_assist", "assist":
		return ModeAIAssist, true
	case "human_takeover", "human":
		return ModeHumanTakeover, true
	default:
		return "", false
	}
}

// SessionEvent records an immutable state transition or interruption event.
type SessionEvent struct {
	Type    string
	Epoch   int
	Mode    Mode
	ActorID string
	At      time.Time
}

// SessionSnapshot is an immutable point-in-time view of a SessionFence.
type SessionSnapshot struct {
	TenantID  string
	SessionID string
	Mode      Mode
	Epoch     int
	OwnerID   string
	Closed    bool
}

// OutboundRequest describes a candidate outbound send across any channel or source.
type OutboundRequest struct {
	TenantID           string
	SessionID          string
	ReplyID            string
	ReplyEpoch         int
	Channel            OutboundChannel
	Source             OutboundSource
	ActorID            string
	IsTenantOwner      bool
	Approved           bool
	HumanAuthored      bool
	ImplicitAutoResume bool
}

// OutboundDecision is the non-mutating result of AuthorizeOutbound.
// Text and audio share the exact same epoch fence so late TTS/audio is never
// emitted after human takeover or handback.
type OutboundDecision struct {
	Allowed      bool
	Reason       string
	Err          error
	CurrentEpoch int
	CurrentMode  Mode
	CurrentOwner string
	BlockText    bool
	BlockAudio   bool
}

// SessionFence enforces atomic epoch fencing for a single reception session.
type SessionFence struct {
	mu        sync.RWMutex
	tenantID  string
	sessionID string
	mode      Mode
	epoch     int
	ownerID   string
	closed    bool
	events    []SessionEvent
}

// NewSessionFence creates a new open SessionFence starting at epoch 1.
func NewSessionFence(tenantID, sessionID string, initialMode Mode) (*SessionFence, error) {
	tenantID = strings.TrimSpace(tenantID)
	sessionID = strings.TrimSpace(sessionID)
	if tenantID == "" {
		return nil, ErrTenantMismatch
	}
	if sessionID == "" {
		return nil, ErrSessionMismatch
	}
	norm, ok := NormalizeMode(string(initialMode))
	if !ok {
		return nil, ErrInvalidMode
	}
	return &SessionFence{
		tenantID:  tenantID,
		sessionID: sessionID,
		mode:      norm,
		epoch:     1,
	}, nil
}

// Snapshot returns a concurrency-safe copy of the current fence state.
func (f *SessionFence) Snapshot() SessionSnapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.snapshotLocked()
}

func (f *SessionFence) snapshotLocked() SessionSnapshot {
	return SessionSnapshot{
		TenantID:  f.tenantID,
		SessionID: f.sessionID,
		Mode:      f.mode,
		Epoch:     f.epoch,
		OwnerID:   f.ownerID,
		Closed:    f.closed,
	}
}

// Events returns a copy of recorded session fence events.
func (f *SessionFence) Events() []SessionEvent {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]SessionEvent, len(f.events))
	copy(out, f.events)
	return out
}

// EnterAIAssist transitions an open session from ai to ai_assist and bumps epoch.
func (f *SessionFence) EnterAIAssist(tenantID, actorID string, expectedEpoch int, now time.Time) (SessionSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if strings.TrimSpace(tenantID) != f.tenantID {
		return f.snapshotLocked(), ErrTenantMismatch
	}
	if f.closed {
		return f.snapshotLocked(), ErrSessionClosed
	}
	if f.mode == ModeAIAssist && f.epoch == expectedEpoch {
		return f.snapshotLocked(), nil
	}
	if f.mode != ModeAI {
		return f.snapshotLocked(), ErrModeConflict
	}
	if expectedEpoch != f.epoch {
		return f.snapshotLocked(), ErrStaleEpoch
	}
	f.mode = ModeAIAssist
	f.epoch++
	f.events = append(f.events, SessionEvent{
		Type:    "assist",
		Epoch:   f.epoch,
		Mode:    f.mode,
		ActorID: strings.TrimSpace(actorID),
		At:      now.UTC(),
	})
	return f.snapshotLocked(), nil
}

// Takeover atomically switches the session into human_takeover, increments
// epoch, sets the human owner, and invalidates all prior-epoch outbound work
// (late cloud Bot responses, late LLM completions, late digital-human TTS/audio).
func (f *SessionFence) Takeover(tenantID, ownerID string, expectedEpoch int, now time.Time) (SessionSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if strings.TrimSpace(tenantID) != f.tenantID {
		return f.snapshotLocked(), ErrTenantMismatch
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return f.snapshotLocked(), ErrActorRequired
	}
	if f.closed {
		return f.snapshotLocked(), ErrSessionClosed
	}
	// Idempotent retry by the same owner on the current human_takeover epoch.
	if f.mode == ModeHumanTakeover && f.ownerID == ownerID && f.epoch == expectedEpoch {
		return f.snapshotLocked(), nil
	}
	if expectedEpoch != f.epoch {
		return f.snapshotLocked(), ErrStaleEpoch
	}
	if f.ownerID != "" && f.ownerID != ownerID {
		return f.snapshotLocked(), ErrTakeoverConflict
	}
	f.mode = ModeHumanTakeover
	f.ownerID = ownerID
	f.epoch++
	f.events = append(f.events, SessionEvent{
		Type:    "takeover",
		Epoch:   f.epoch,
		Mode:    f.mode,
		ActorID: ownerID,
		At:      now.UTC(),
	})
	return f.snapshotLocked(), nil
}

// HandBackToAI explicitly returns a human_takeover session to AI mode and
// increments epoch cleanly. Returning to AI is NEVER implicit from a callback.
func (f *SessionFence) HandBackToAI(tenantID, actorID string, isTenantOwner bool, expectedEpoch int, now time.Time) (SessionSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if strings.TrimSpace(tenantID) != f.tenantID {
		return f.snapshotLocked(), ErrTenantMismatch
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return f.snapshotLocked(), ErrActorRequired
	}
	if f.closed {
		return f.snapshotLocked(), ErrSessionClosed
	}
	if f.mode != ModeHumanTakeover {
		return f.snapshotLocked(), ErrNotHumanMode
	}
	if !isTenantOwner && f.ownerID != actorID {
		return f.snapshotLocked(), ErrUnauthorizedHandback
	}
	if expectedEpoch != f.epoch {
		return f.snapshotLocked(), ErrStaleEpoch
	}
	f.mode = ModeAI
	f.ownerID = ""
	f.epoch++
	f.events = append(f.events, SessionEvent{
		Type:    "handback",
		Epoch:   f.epoch,
		Mode:    f.mode,
		ActorID: actorID,
		At:      now.UTC(),
	})
	return f.snapshotLocked(), nil
}

// RecordInterruption records a visitor interruption event at the current epoch
// without mutating owner, mode, or epoch.
func (f *SessionFence) RecordInterruption(tenantID string, now time.Time) (SessionSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if strings.TrimSpace(tenantID) != f.tenantID {
		return f.snapshotLocked(), ErrTenantMismatch
	}
	if f.closed {
		return f.snapshotLocked(), ErrSessionClosed
	}
	f.events = append(f.events, SessionEvent{
		Type:  "interruption",
		Epoch: f.epoch,
		Mode:  f.mode,
		At:    now.UTC(),
	})
	return f.snapshotLocked(), nil
}

// Close closes the session and increments epoch, blocking all subsequent sends.
func (f *SessionFence) Close(tenantID, actorID string, now time.Time) (SessionSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if strings.TrimSpace(tenantID) != f.tenantID {
		return f.snapshotLocked(), ErrTenantMismatch
	}
	if f.closed {
		return f.snapshotLocked(), nil
	}
	f.closed = true
	f.epoch++
	f.events = append(f.events, SessionEvent{
		Type:    "close",
		Epoch:   f.epoch,
		Mode:    f.mode,
		ActorID: strings.TrimSpace(actorID),
		At:      now.UTC(),
	})
	return f.snapshotLocked(), nil
}

// AuthorizeOutbound checks whether an outbound text/audio/bot/digital-human
// message is eligible to send right now.
//
// It is strictly read-only: rejected or late callbacks NEVER mutate session
// mode, owner, or epoch, and NEVER implicitly resume AI mode.
func (f *SessionFence) AuthorizeOutbound(req OutboundRequest) OutboundDecision {
	f.mu.RLock()
	defer f.mu.RUnlock()

	deny := func(reason string, err error) OutboundDecision {
		return OutboundDecision{
			Allowed:      false,
			Reason:       reason,
			Err:          err,
			CurrentEpoch: f.epoch,
			CurrentMode:  f.mode,
			CurrentOwner: f.ownerID,
			BlockText:    true,
			BlockAudio:   true,
		}
	}

	if strings.TrimSpace(req.TenantID) != f.tenantID {
		return deny("tenant_mismatch", ErrTenantMismatch)
	}
	if strings.TrimSpace(req.SessionID) != f.sessionID {
		return deny("session_mismatch", ErrSessionMismatch)
	}
	if req.ImplicitAutoResume {
		return deny("implicit_resume_forbidden", ErrImplicitResumeBlocked)
	}
	if f.closed {
		return deny("session_closed", ErrSessionClosed)
	}
	if req.ReplyEpoch < f.epoch {
		return deny("stale_epoch", ErrStaleEpoch)
	}
	if req.ReplyEpoch > f.epoch {
		return deny("future_epoch", ErrInvalidEpoch)
	}

	switch f.mode {
	case ModeHumanTakeover:
		isHumanSource := req.Source == SourceHuman || (req.Source == SourceDigitalHuman && req.HumanAuthored)
		if !isHumanSource {
			return deny("human_takeover_active", ErrHumanTakeoverActive)
		}
		actor := strings.TrimSpace(req.ActorID)
		if !req.IsTenantOwner && (actor == "" || actor != f.ownerID) {
			return deny("not_current_owner", ErrTakeoverConflict)
		}
		if !req.Approved {
			return deny("approval_required", ErrApprovalRequired)
		}
	case ModeAIAssist:
		if !req.Approved {
			return deny("approval_required", ErrApprovalRequired)
		}
	case ModeAI:
		if !req.Approved {
			return deny("approval_required", ErrApprovalRequired)
		}
	default:
		return deny("invalid_mode", ErrInvalidMode)
	}

	return OutboundDecision{
		Allowed:      true,
		Reason:       "ok",
		CurrentEpoch: f.epoch,
		CurrentMode:  f.mode,
		CurrentOwner: f.ownerID,
		BlockText:    false,
		BlockAudio:   false,
	}
}
