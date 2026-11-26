package store

import (
	"errors"
	"testing"
)

func TestMatrixDraftSourceEventStaleSourceVersionDoesNotOverwriteAdoptedNewer(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	ref := savedRefWithSource(t, s, "d_stale_sv")

	// Newer source_version=2 lands first.
	applied, idem, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_sv2", "mem_1", MatrixDraftSourceFacts{
		Kind:           "result",
		SourceVersion:  2,
		MatrixPlanID:   "plan_sv2",
		MatrixItemID:   "item_sv2",
		ExternalPostID: "post_sv2",
		Reach:          ptrInt(200),
	})
	if err != nil || !applied || idem {
		t.Fatalf("sv2 apply: applied=%v idem=%v err=%v", applied, idem, err)
	}

	// Late older source_version=1 MUST be rejected and NEVER overwrite sv2.
	_, _, err = s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_sv1_late", "mem_1", MatrixDraftSourceFacts{
		Kind:           "result",
		SourceVersion:  1,
		MatrixPlanID:   "plan_sv1",
		MatrixItemID:   "item_sv1",
		ExternalPostID: "post_sv1",
		Reach:          ptrInt(10),
		Likes:          ptrInt(5),
	})
	if !errors.Is(err, ErrStaleEvent) {
		t.Fatalf("stale sv1 err=%v, want ErrStaleEvent", err)
	}

	got, err := s.GetMatrixDraftRef("tnt_1", "d_stale_sv")
	if err != nil {
		t.Fatal(err)
	}
	if got.MatrixPlanID != "plan_sv2" || got.MatrixItemID != "item_sv2" || got.ExternalPostID != "post_sv2" {
		t.Fatalf("stale event overwrote refs: %+v", got)
	}
	if got.MatrixReach == nil || *got.MatrixReach != 200 || got.MatrixLikes != nil || got.MatrixLeads != nil {
		t.Fatalf("stale event mutated metrics or turned unknown into non-null: %+v", got)
	}
}

func TestMatrixDraftSourceEventSupersededRevisionRefused(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)

	v3 := matrixRefIn("d_rev3")
	v3.Revision = 3
	ref3, _, err := s.SaveMatrixDraftRef(v3)
	if err != nil {
		t.Fatal(err)
	}

	v4 := matrixRefIn("d_rev4")
	v4.Revision = 4
	ref4, _, err := s.SaveMatrixDraftRef(v4)
	if err != nil {
		t.Fatal(err)
	}

	adopted, ok, err := s.GetLatestAdoptedMatrixDraftRef("tnt_1", v3.CampaignID)
	if err != nil || !ok || adopted.ID != ref4.ID || adopted.Revision != 4 {
		t.Fatalf("latest adopted = %+v ok=%v err=%v", adopted, ok, err)
	}

	// Late result on superseded revision 3 must be refused.
	_, _, err = s.AppendMatrixDraftSourceEvent("tnt_1", ref3.ID, "evt_old_rev3", "mem_1", resultFacts())
	if !errors.Is(err, ErrStaleEvent) {
		t.Fatalf("superseded rev3 err=%v, want ErrStaleEvent", err)
	}

	// ExpectedRevision lower than target ref4.Revision must also be refused.
	_, _, err = s.AppendMatrixDraftSourceEvent("tnt_1", ref4.ID, "evt_wrong_rev_on_v4", "mem_1", MatrixDraftSourceFacts{
		Kind:             "result",
		ExpectedRevision: 3,
		MatrixPlanID:     "plan_stale",
	})
	if !errors.Is(err, ErrStaleEvent) {
		t.Fatalf("expected revision < ref.Revision err=%v, want ErrStaleEvent", err)
	}

	// Negative metrics are refused fail-closed.
	_, _, err = s.AppendMatrixDraftSourceEvent("tnt_1", ref4.ID, "evt_neg", "mem_1", MatrixDraftSourceFacts{
		Kind:  "result",
		Reach: ptrInt(-1),
	})
	if !errors.Is(err, ErrBadEvent) {
		t.Fatalf("negative metric err=%v, want ErrBadEvent", err)
	}
}

func TestMatrixDraftReceiptUpdateNeverUnrevokesOrFabricatesPublished(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	ref := savedRefWithSource(t, s, "d_rcpt_guard")

	// Attempt to write "published" without official publish authority stays "draft" and published=false.
	if err := s.UpdateMatrixDraftReceipt("tnt_1", "d_rcpt_guard", "published"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMatrixDraftRef("tnt_1", "d_rcpt_guard")
	if err != nil || got.Status != "draft" || got.Published || got.Charged {
		t.Fatalf("fabricated published status persisted: %+v err=%v", got, err)
	}

	// Revoke latches the row: UpdateMatrixDraftReceipt must not un-revoke it.
	if _, _, err := s.AppendMatrixDraftSourceEvent("tnt_1", ref.ID, "evt_rev_latch", "mem_1", MatrixDraftSourceFacts{Kind: "revoke"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMatrixDraftReceipt("tnt_1", "d_rcpt_guard", "draft"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("update after revoke err=%v, want ErrRevoked", err)
	}
	got, _ = s.GetMatrixDraftRef("tnt_1", "d_rcpt_guard")
	if got.Status != "revoked" {
		t.Fatalf("revoked row was overwritten: %+v", got)
	}
}

func TestChannelRetractAndLateLegacyReplayNeverDuplicateOrRestoreConsent(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	grant := ChannelGrantIn{
		Provider: "connector", AccountID: "acct_1", AppID: "app_auth",
		SubjectNS: "connector.subject", MessageNS: "connector.message", PostNS: "connector.post",
		Capabilities: []string{"comment.read", "comment.reply", "message.read"},
	}
	if err := s.SaveChannelGrant("tnt_1", grant); err != nil {
		t.Fatal(err)
	}

	// 1. Initial sales inquiry with marketing consent granted.
	ev := channelEvent("evt_retract_1", "comment", true)
	ev.Phone = "13800001234"
	ev.PhoneMarketingConsent = true
	ev.SMSMarketingConsent = true
	first, err := s.IngestChannelEvent("tnt_1", ev)
	if err != nil || first.CandidateID == "" || !first.PhoneMarketing || !first.SMSMarketing {
		t.Fatalf("first ingest = %+v err=%v", first, err)
	}

	// 2. Retract clears marketing consent and withdraws candidate.
	ev.Retracted = true
	retracted, err := s.IngestChannelEvent("tnt_1", ev)
	if err != nil || retracted.CandidateStatus != "withdrawn" || retracted.PhoneMarketing || retracted.SMSMarketing {
		t.Fatalf("retract = %+v err=%v", retracted, err)
	}

	// Verify DB columns cleared phone_marketing & sms_marketing to 0.
	rows, err := s.ListChannelInteractions("tnt_1")
	if err != nil || len(rows) != 1 || !rows[0].Retracted || rows[0].PhoneMarketing || rows[0].SMSMarketing {
		t.Fatalf("DB row after retract = %+v err=%v", rows, err)
	}

	// 3. Late un-retracted edit / legacy-route replay with consent=true MUST NOT resurrect candidate or consent.
	ev.Retracted = false
	ev.LegacyRoute = true
	ev.Text = "晚到旧路由重放"
	ev.PhoneMarketingConsent = true
	ev.SMSMarketingConsent = true
	late, err := s.IngestChannelEvent("tnt_1", ev)
	if err != nil || !late.Idempotent || late.PhoneMarketing || late.SMSMarketing || late.CandidateStatus != "withdrawn" {
		t.Fatalf("late arrival after retract = %+v err=%v", late, err)
	}
	if n := channelCandidateCount(t, s, "tnt_1"); n != 1 {
		t.Fatalf("candidate count = %d, want 1", n)
	}
}
