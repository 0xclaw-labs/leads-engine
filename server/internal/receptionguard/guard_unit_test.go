package receptionguard

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSessionFenceEdgeCases(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	if _, err := NewSessionFence("", "s1", ModeAI); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("empty tenant expected ErrTenantMismatch, got %v", err)
	}
	if _, err := NewSessionFence("t1", "", ModeAI); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("empty session expected ErrSessionMismatch, got %v", err)
	}
	if _, err := NewSessionFence("t1", "s1", Mode("bogus")); !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("bogus mode expected ErrInvalidMode, got %v", err)
	}

	if m, ok := NormalizeMode("assist"); !ok || m != ModeAIAssist {
		t.Fatalf("NormalizeMode(assist) = %q %v", m, ok)
	}
	if m, ok := NormalizeMode("human"); !ok || m != ModeHumanTakeover {
		t.Fatalf("NormalizeMode(human) = %q %v", m, ok)
	}

	fence, err := NewSessionFence("t1", "s1", ModeAI)
	if err != nil {
		t.Fatal(err)
	}

	// Cross-tenant takeover rejected.
	if _, err := fence.Takeover("t2", "agent1", 1, now); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("cross-tenant takeover = %v", err)
	}
	// Empty owner rejected.
	if _, err := fence.Takeover("t1", "", 1, now); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("empty owner takeover = %v", err)
	}
	// Takeover succeeds 1 -> 2.
	snap, err := fence.Takeover("t1", "agent1", 1, now)
	if err != nil || snap.Epoch != 2 || snap.OwnerID != "agent1" {
		t.Fatalf("takeover = %+v %v", snap, err)
	}
	// Same owner retry on current epoch 2 is idempotent (no epoch bump).
	snap, err = fence.Takeover("t1", "agent1", 2, now)
	if err != nil || snap.Epoch != 2 {
		t.Fatalf("idempotent takeover = %+v %v", snap, err)
	}
	// Another member trying to steal current epoch 2 fails with ErrTakeoverConflict.
	if _, err := fence.Takeover("t1", "agent2", 2, now); !errors.Is(err, ErrTakeoverConflict) {
		t.Fatalf("takeover conflict = %v", err)
	}
	// Record interruption does not mutate epoch/owner.
	snap, err = fence.RecordInterruption("t1", now)
	if err != nil || snap.Epoch != 2 || snap.OwnerID != "agent1" {
		t.Fatalf("interruption mutated fence = %+v %v", snap, err)
	}
	// Tenant owner may hand back even if agent1 holds the session.
	snap, err = fence.HandBackToAI("t1", "owner_admin", true, 2, now)
	if err != nil || snap.Mode != ModeAI || snap.Epoch != 3 || snap.OwnerID != "" {
		t.Fatalf("tenant owner handback = %+v %v", snap, err)
	}
	// HandBackToAI from AI mode is rejected.
	if _, err := fence.HandBackToAI("t1", "owner_admin", true, 3, now); !errors.Is(err, ErrNotHumanMode) {
		t.Fatalf("handback from AI = %v", err)
	}
	if len(fence.Events()) != 3 {
		t.Fatalf("expected 3 events (takeover, interruption, handback), got %+v", fence.Events())
	}
}

func TestDeliveryLedgerSupersedeAndInvalidKeys(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	ledger := NewDeliveryLedger()
	fence, err := NewSessionFence("t1", "s1", ModeAI)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := ledger.Prepare(ReplyRecord{TenantID: "t1", SessionID: "s1", ReplyID: "", Epoch: 1, Body: "hi"}, now); !errors.Is(err, ErrInvalidReplyKey) {
		t.Fatalf("invalid key = %v", err)
	}

	k1 := DeliveryKey{TenantID: "t1", SessionID: "s1", ReplyID: "r1", Epoch: 1}
	if _, _, err := ledger.Prepare(ReplyRecord{TenantID: "t1", SessionID: "s1", ReplyID: "r1", Epoch: 1, Body: "draft1"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := fence.Takeover("t1", "agent1", 1, now); err != nil {
		t.Fatal(err)
	}
	if n := ledger.SupersedePriorEpochs("t1", "s1", 2); n != 1 {
		t.Fatalf("expected 1 superseded record, got %d", n)
	}
	rec, ok := ledger.Get(k1)
	if !ok || rec.Stage != StageSuperseded {
		t.Fatalf("expected StageSuperseded, got %+v ok=%v", rec, ok)
	}
	if _, err := ledger.Approve(k1, "agent1", false, fence, now); !errors.Is(err, ErrOutboundBlocked) {
		t.Fatalf("approving superseded record expected ErrOutboundBlocked, got %v", err)
	}
}

func TestEntrustedOrderScopeRejectsCRMActionsAndClosedProjection(t *testing.T) {
	dec := GuardScopeBoundary(ScopeBoundaryInput{
		TenantID:     "t1",
		SessionScope: ScopeEntrustedOrder,
		Action:       BoundaryActionCreateLead,
		InquiryKind:  InquirySalesConsult,
	})
	if dec.Allowed || !containsStr(dec.Violations, "entrusted_order_crm_action_forbidden") {
		t.Fatalf("entrusted order scope must block CRM lead creation: %+v", dec)
	}

	closedProj := BuildPresentationProjection(ProjectionInput{
		Consumer:  ConsumerCloudBot,
		TenantID:  "t1",
		SessionID: "s_closed",
		Mode:      ModeAI,
		Epoch:     3,
		Language:  "zh",
		Closed:    true,
		Replies: []ProjectionSourceReply{
			{ReplyID: "r3", TenantID: "t1", SessionID: "s_closed", Epoch: 3, Stage: StageApproved, Text: "hello"},
		},
		Events: []ProjectionSourceEvent{
			{Type: "close", Epoch: 3},
		},
	})
	if len(closedProj.Replies) != 0 || len(closedProj.Events) != 1 || closedProj.Events[0].Type != "close" {
		t.Fatalf("closed projection must omit replies and keep close event: %+v", closedProj)
	}

	scrubbed := ScrubSensitiveText("联系 13800138000 / 微信：abc_12345 / Bearer secretToken123456 / a@b.com")
	for _, secret := range []string{"13800138000", "abc_12345", "secretToken123456", "a@b.com"} {
		if strings.Contains(scrubbed, secret) {
			t.Fatalf("ScrubSensitiveText leaked %q in %q", secret, scrubbed)
		}
	}
}
