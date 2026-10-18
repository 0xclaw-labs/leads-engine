package campaignmotion

// preference_test.go: HUI-2747 MX-OPS 增量 — 受权执行偏好只以命名模板交出，
// 不发数字 priority；伪造 priority 直接拒绝。

import (
	"errors"
	"strings"
	"testing"
)

func TestPrepareCarriesNamedExecutionPreference(t *testing.T) {
	const campaignID = "led_campaign_pref"
	got, err := Prepare("tnt_motion", campaignID, handoffRaw(t, campaignID, validDigest(),
		`"execution_preference":"night_batch"`), panicWriter{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if got.Document.ExecutionPreference != "night_batch" {
		t.Fatalf("preference = %q, want night_batch", got.Document.ExecutionPreference)
	}
	if !strings.Contains(string(got.Export), `"execution_preference":"night_batch"`) {
		t.Fatalf("export must carry the named preference: %s", got.Export)
	}
	if got.ModelCalls != 0 || got.PieceGenerated || got.PieceSent {
		t.Fatalf("preference must not generate/send: %+v", got)
	}
}

func TestPrepareRejectsUnknownPreferenceAndNumericPriority(t *testing.T) {
	const campaignID = "led_campaign_pref_bad"
	if _, err := Prepare("tnt_motion", campaignID, handoffRaw(t, campaignID, validDigest(),
		`"execution_preference":"sneaky_high"`), panicWriter{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown preference must be ErrInvalid, got %v", err)
	}
	if _, err := Prepare("tnt_motion", campaignID, handoffRaw(t, campaignID, validDigest(),
		`"priority":9`), panicWriter{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged priority must be ErrInvalid, got %v", err)
	}
	if _, err := Prepare("tnt_motion", campaignID, handoffRaw(t, campaignID, validDigest(),
		`"requested_priority":"high"`), panicWriter{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged requested_priority must be ErrInvalid, got %v", err)
	}
}

func TestPrepareOmitsPreferenceWhenAbsent(t *testing.T) {
	const campaignID = "led_campaign_pref_empty"
	got, err := Prepare("tnt_motion", campaignID, handoffRaw(t, campaignID, validDigest(), ""), panicWriter{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if got.Document.ExecutionPreference != "" {
		t.Fatalf("preference = %q, want empty", got.Document.ExecutionPreference)
	}
	if strings.Contains(string(got.Export), "execution_preference") {
		t.Fatalf("absent preference must be omitted: %s", got.Export)
	}
}
