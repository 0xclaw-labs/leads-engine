package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bianjiefilm/leads-engine/server/internal/platformtask"
)

// TestPhraseModelDraftReplacesAnswerOnlyWhenGrounded checks HUI-1688: a model
// draft replaces the FAQ answer only when every number and promise is grounded.
// The fake task server stands in for the platform task API (fixture evidence).
func TestPhraseModelDraftReplacesAnswerOnlyWhenGrounded(t *testing.T) {
	const faqAnswer = "每天 9:00 到 18:00"
	const visitorSecret = "VISITORSECRET 13800138000"
	var mu sync.Mutex
	var result string
	var posted []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			posted = append([]byte(nil), raw...)
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "tsk_draft", "status": "QUEUED"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "tsk_draft", "status": "SUCCEEDED", "result_json": result})
	}))
	defer srv.Close()

	h := newHarnessOpts(t, harnessOpts{featureReception: true})
	tenantA, _, _ := h.seed()
	h.api.ReceptionPhraser = platformtask.New(srv.URL, "task-token-not-logged", "acct_test", "leads-engine")
	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenantA, `{"default_mode":"ai","language":"zh"}`, 201)
	h.mustDo("POST", "/api/v1/reception/knowledge", sessionOwnerA, tenantA, `{"question":"营业时间","answer":"`+faqAnswer+`"}`, 201)

	send := func(visitorKey, clientMsgID, text string) map[string]any {
		t.Helper()
		opened := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "", `{"visitor_key":"`+visitorKey+`"}`, 201)
		sid := opened["session"].(map[string]any)["id"].(string)
		return h.mustDo("POST", "/api/v1/public/reception/sessions/"+sid+"/messages", "", "",
			`{"visitor_key":"`+visitorKey+`","client_msg_id":"`+clientMsgID+`","text":"`+text+`"}`, 200)
	}

	mu.Lock()
	result = `{"text":"每天 9:00 到 18:00，欢迎到店"}`
	mu.Unlock()
	grounded := send("visitor-key-draft-a", "draft-a", "请问营业时间 "+visitorSecret)
	reply := grounded["reply"].(map[string]any)
	model := grounded["model"].(map[string]any)
	if reply["body"] != "每天 9:00 到 18:00，欢迎到店" || reply["status"] != "sent" || model["billing_verdict"] != "recorded" {
		t.Fatalf("grounded draft not used: %s", mustJSON(grounded))
	}
	mu.Lock()
	outbound := string(posted)
	mu.Unlock()
	if !strings.Contains(outbound, faqAnswer) || strings.Contains(outbound, "VISITORSECRET") || strings.Contains(outbound, "13800138000") {
		t.Fatalf("prompt carried visitor text or lacked the grounded answer: %s", outbound)
	}

	mu.Lock()
	result = `{"text":"每天 10:00 到 18:00，欢迎到店"}`
	mu.Unlock()
	ungrounded := send("visitor-key-draft-b", "draft-b", "请问营业时间")
	reply2 := ungrounded["reply"].(map[string]any)
	model2 := ungrounded["model"].(map[string]any)
	if reply2["body"] != faqAnswer || reply2["status"] != "sent" || model2["billing_verdict"] != "recorded" {
		t.Fatalf("ungrounded draft replaced the FAQ answer: %s", mustJSON(ungrounded))
	}
	if strings.Contains(mustJSON(ungrounded), "10:00") {
		t.Fatalf("ungrounded time leaked into reply: %s", mustJSON(ungrounded))
	}
}
