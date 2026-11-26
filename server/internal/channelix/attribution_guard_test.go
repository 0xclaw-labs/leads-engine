package channelix

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/receptionguard"
)

func TestCommentAndDMReadReplyCapabilitiesVerifiedSeparately(t *testing.T) {
	// comment.read + comment.reply can read & reply comments, NOT direct messages.
	commentOK := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapCommentRead, CapCommentReply}
		in.Event.Kind = KindComment
		in.WantReply = true
	}))
	if !commentOK.OK || !commentOK.ReplyAllowed || commentOK.ReplyAuthority != ReplyAuthorityReceptionGuard || commentOK.SecondReplyBotStarted {
		t.Fatalf("comment read+reply should succeed: %+v", commentOK)
	}

	// comment.reply does NOT grant message.reply even when message.read is present.
	dmReplyDenied := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapMessageRead, CapCommentReply}
		in.Event.Kind = KindMessage
		in.WantReply = true
	}))
	if !dmReplyDenied.OK || dmReplyDenied.ReplyAllowed || dmReplyDenied.ReplyRefusal != RefusalCapability {
		t.Fatalf("comment.reply must not authorize DM reply: %+v", dmReplyDenied)
	}

	// message.reply does NOT grant comment.reply.
	commentReplyDenied := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapCommentRead, CapMessageReply}
		in.Event.Kind = KindComment
		in.WantReply = true
	}))
	if !commentReplyDenied.OK || commentReplyDenied.ReplyAllowed || commentReplyDenied.ReplyRefusal != RefusalCapability {
		t.Fatalf("message.reply must not authorize comment reply: %+v", commentReplyDenied)
	}

	// message.read + message.reply authorizes DM reply draft (still never delivered).
	dmOK := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapMessageRead, CapMessageReply}
		in.Event.Kind = KindMessage
		in.WantReply = true
	}))
	if !dmOK.OK || !dmOK.ReplyAllowed || dmOK.Delivered || dmOK.AutoReach {
		t.Fatalf("message read+reply should allow reply draft only: %+v", dmOK)
	}
}

func TestRetractedEventCannotBeResurrectedOrRestoreMarketingConsent(t *testing.T) {
	// Retraction itself must force phone/sms marketing false even if payload sets consent=true.
	retract := Admit(sampleInput(func(in *Input) {
		in.Event.Retracted = true
		in.Event.Phone = "13800000000"
		in.Event.PhoneMarketingConsent = true
		in.Event.SMSMarketingConsent = true
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
		in.Prior = &Stored{InteractionID: "ix_1", CandidateID: "cand_1", TextSHA256: "old"}
	}))
	if !retract.Retract || retract.PhoneMarketing || retract.SMSMarketing || retract.CreateCandidate || !retract.WithdrawCandidate {
		t.Fatalf("retract must clear consent and withdraw candidate: %+v", retract)
	}

	// Late edit / un-retracted replay / legacy route arrival after retraction MUST NOT
	// update, create a new candidate, allow reply, or restore marketing consent.
	late := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapCommentRead, CapCommentReply}
		in.Event.Retracted = false
		in.Event.Text = "晚到编辑文本"
		in.Event.Phone = "13800000000"
		in.Event.PhoneMarketingConsent = true
		in.Event.SMSMarketingConsent = true
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
		in.WantReply = true
		in.Prior = &Stored{InteractionID: "ix_1", CandidateID: "", TextSHA256: "old", Retracted: true}
	}))
	if !late.Idempotent || late.Update || late.CreateCandidate || late.PhoneMarketing || late.SMSMarketing || late.ReplyAllowed {
		t.Fatalf("late arrival after retract resurrected state or consent: %+v", late)
	}
}

func TestLegacyRouteLateArrivalDoesNotOverwriteOrRestoreConsent(t *testing.T) {
	lateLegacy := Admit(sampleInput(func(in *Input) {
		in.Event.LegacyRoute = true
		in.Event.Text = "旧路由晚到正文"
		in.Event.Phone = "13800000000"
		in.Event.PhoneMarketingConsent = true
		in.Event.SMSMarketingConsent = true
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
		in.Prior = &Stored{
			InteractionID:  "ix_1",
			CandidateID:    "",
			TextSHA256:     "newer_hash",
			Retracted:      false,
			PhoneMarketing: false,
			SMSMarketing:   false,
		}
	}))
	if !lateLegacy.Idempotent || lateLegacy.Update || lateLegacy.CreateCandidate {
		t.Fatalf("legacy route late arrival mutated interaction or created lead: %+v", lateLegacy)
	}
	if lateLegacy.PhoneMarketing || lateLegacy.SMSMarketing {
		t.Fatalf("legacy route late arrival restored revoked marketing consent: %+v", lateLegacy)
	}
}

func TestReceptionGuardSoleTakeoverAuthorityAndZeroSecondReplyBot(t *testing.T) {
	// 1. Matrix second reply bot is ALWAYS forbidden.
	secondBot := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapCommentRead, CapCommentReply}
		in.WantReply = true
		in.ReplySpeaker = SpeakerMatrix
	}))
	if secondBot.ReplyAllowed || secondBot.ReplyRefusal != RefusalSecondBot || secondBot.SecondReplyBotStarted {
		t.Fatalf("second reply bot was not blocked: %+v", secondBot)
	}

	// 2. Human takeover via receptionguard.SessionFence is the sole authority.
	fence, err := receptionguard.NewSessionFence("tnt_a", "ses_1", receptionguard.ModeAI)
	if err != nil {
		t.Fatal(err)
	}
	beforeTakeover := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapCommentRead, CapCommentReply}
		in.WantReply = true
		in.ReceptionFence = fence
		in.ReceptionEpoch = 1
		in.ReceptionApproved = true
	}))
	if !beforeTakeover.ReplyAllowed || beforeTakeover.ReplyAuthority != ReplyAuthorityReceptionGuard {
		t.Fatalf("before takeover reply should be allowed by receptionguard: %+v", beforeTakeover)
	}

	// Human takes over session -> epoch advances to 2, mode = human_takeover.
	if _, err := fence.Takeover("tnt_a", "agent_human_1", 1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	duringTakeover := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapCommentRead, CapCommentReply}
		in.WantReply = true
		in.ReceptionFence = fence
		in.ReceptionEpoch = 2
		in.ReceptionApproved = true
	}))
	if duringTakeover.ReplyAllowed || duringTakeover.ReplyRefusal != RefusalHumanTakeover || duringTakeover.SecondReplyBotStarted {
		t.Fatalf("during human takeover receptionguard must block AI reply: %+v", duringTakeover)
	}

	// Stale epoch is also rejected by receptionguard.
	staleEpoch := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapCommentRead, CapCommentReply}
		in.WantReply = true
		in.ReceptionFence = fence
		in.ReceptionEpoch = 1
		in.ReceptionApproved = true
	}))
	if staleEpoch.ReplyAllowed || staleEpoch.ReplyRefusal != "stale_epoch" {
		t.Fatalf("stale epoch must be rejected by receptionguard: %+v", staleEpoch)
	}
}

func TestMinimalNotifyRefOmitsSensitiveContent(t *testing.T) {
	ref, err := BuildMinimalNotifyRef("tnt_a", "connector", "acct_1", "ix_123", "evt_456")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 6 || m["path"] != "/channel-interactions/ix_123" {
		t.Fatalf("unexpected minimal notify ref: %s", raw)
	}
	for _, bad := range []string{"text", "body", "phone", "nickname", "crm"} {
		if strings.Contains(string(raw), `"`+bad+`"`) {
			t.Fatalf("minimal notify ref contains sensitive field %s: %s", bad, raw)
		}
	}
	if _, err := BuildMinimalNotifyRef("tnt_a", "connector", "acct_1", "bad/text 敏感", "evt_1"); err == nil {
		t.Fatal("invalid interaction id accepted in notify ref")
	}
}
