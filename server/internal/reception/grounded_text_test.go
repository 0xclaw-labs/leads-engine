package reception

import (
	"strings"
	"testing"
)

const faqHours = "每天 9:00 到 18:00"

func TestGroundedReplyKeepsCandidateThatOnlyReusesFAQFacts(t *testing.T) {
	got, ok := GroundedReplyText("  每天 9:00 到 18:00，欢迎到店  ", faqHours)
	if !ok || got != "每天 9:00 到 18:00，欢迎到店" {
		t.Fatalf("grounded = %q %v", got, ok)
	}
}

func TestGroundedReplyAcceptsWordingWithoutFacts(t *testing.T) {
	if got, ok := GroundedReplyText("我们很乐意为您解答", faqHours); !ok || got == "" {
		t.Fatalf("wording only = %q %v", got, ok)
	}
}

func TestGroundedReplyRejectsNewTimeNotInFAQ(t *testing.T) {
	if got, ok := GroundedReplyText("每天 10:00 到 18:00", faqHours); ok || got != "" {
		t.Fatalf("new time accepted: %q %v", got, ok)
	}
}

func TestGroundedReplyRejectsNewPriceNotInFAQ(t *testing.T) {
	if got, ok := GroundedReplyText("只要 99 元，每天 9:00 到 18:00", faqHours); ok || got != "" {
		t.Fatalf("new price accepted: %q %v", got, ok)
	}
}

func TestGroundedReplyRejectsPromiseWordsNotInFAQ(t *testing.T) {
	for _, bad := range []string{"我们保证包退", "免费送货", "这是最低价", "绝对没问题", "一定能办好"} {
		if got, ok := GroundedReplyText(bad, faqHours); ok || got != "" {
			t.Fatalf("promise %q accepted: %q", bad, got)
		}
	}
}

func TestGroundedReplyAllowsPromiseWordsThatTheFAQAlreadyStates(t *testing.T) {
	faq := "全场免费停车，每天 9:00 到 18:00"
	if got, ok := GroundedReplyText("全场免费停车", faq); !ok || got != "全场免费停车" {
		t.Fatalf("stated promise rejected: %q %v", got, ok)
	}
}

func TestGroundedReplyRejectsPlaceholders(t *testing.T) {
	for _, bad := range []string{"您好{{name}}", "模板占位文字", "收到}}"} {
		if got, ok := GroundedReplyText(bad, faqHours); ok || got != "" {
			t.Fatalf("placeholder %q accepted", bad)
		}
	}
}

func TestGroundedReplyRejectsBlankAndOverlong(t *testing.T) {
	if _, ok := GroundedReplyText("   ", faqHours); ok {
		t.Fatal("blank accepted")
	}
	long := strings.Repeat("好", MaxModelReplyRunes+1)
	if _, ok := GroundedReplyText(long, faqHours); ok {
		t.Fatal("overlong accepted")
	}
	exact := strings.Repeat("好", MaxModelReplyRunes)
	if _, ok := GroundedReplyText(exact, faqHours); !ok {
		t.Fatal("limit length rejected")
	}
}
