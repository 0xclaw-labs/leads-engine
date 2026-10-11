package campaignmotion

import (
	"errors"
	"strings"
	"testing"
)

func validMotionBody(campaignID string) []byte {
	return []byte(`{
		"campaign_goal":"春季上新",
		"approved_facts":[{"key":"product","value":"冷萃","confirmed":true}],
		"assets":[{"id":"asset_ok_1","authorized":true}],
		"cta":"到店试喝",
		"channels":["抖音"],
		"budget_attribution_id":"budget_spring_1",
		"motion":{"project_id":"prj_motion_1","revision_id":"rev_motion_1","campaign_id":"` + campaignID + `","digest":"sha256:` + strings.Repeat("ab", 32) + `"}
	}`)
}

func TestCountingWriterRefusesAndCounts(t *testing.T) {
	w := &CountingWriter{}
	err := w.WriteCreativeProject("prj_motion_1", "rev_motion_1", []byte("{}"))
	if !errors.Is(err, ErrWriterCalled) {
		t.Fatalf("WriteCreativeProject err = %v, want ErrWriterCalled", err)
	}
	if w.Calls != 1 {
		t.Fatalf("Calls = %d, want 1", w.Calls)
	}
}

func TestPrepareNeverCallsWriterAndObservesZero(t *testing.T) {
	w := &CountingWriter{}
	res, err := Prepare("camp_1", validMotionBody("camp_1"), w)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if w.Calls != 0 {
		t.Fatalf("Prepare called the writer %d times, want 0", w.Calls)
	}
	if res.ModelCalls != 0 || res.CreativeProjectWrites != 0 || res.PieceGenerated || res.PieceSent {
		t.Fatalf("result claims production: %+v", res)
	}
}

func TestPrepareAcceptsNilWriterWithoutPanic(t *testing.T) {
	if _, err := Prepare("camp_1", validMotionBody("camp_1"), nil); err != nil {
		t.Fatalf("Prepare with nil writer: %v", err)
	}
}

func TestAssertZeroFailsWhenWriterWasCalled(t *testing.T) {
	w := &CountingWriter{}
	if err := w.AssertZero(); err != nil {
		t.Fatalf("AssertZero on untouched port = %v", err)
	}
	_ = w.WriteCreativeProject("p", "r", nil)
	if err := w.AssertZero(); !errors.Is(err, ErrWriterCalled) {
		t.Fatalf("AssertZero after a call = %v, want ErrWriterCalled", err)
	}
}
