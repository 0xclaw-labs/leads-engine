package platformtask

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewRequiresAllThreeKeys(t *testing.T) {
	if New("http://127.0.0.1:9", "token", "acct", "leads-engine") == nil {
		t.Fatal("three keys must build a client")
	}
	if New("", "token", "acct", "leads-engine") != nil || New("http://127.0.0.1:9", "", "acct", "leads-engine") != nil || New("http://127.0.0.1:9", "token", "", "leads-engine") != nil {
		t.Fatal("a missing key must stay text-degraded")
	}
}

// fakeTasks imitates the public-ai task API for one submit and one poll.
// result is the result_json the summary carries once the task is terminal.
type fakeTasks struct {
	mu        sync.Mutex
	status    string
	result    string
	pollDelay time.Duration
	posted    []byte
	taskID    string
}

func (f *fakeTasks) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			f.mu.Lock()
			f.posted = append([]byte(nil), raw...)
			f.mu.Unlock()
			if r.Header.Get("X-PilotSeaView-Internal-Token") == "" || r.Header.Get("X-App-ID") != "leads-engine" {
				t.Errorf("task auth headers missing")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": f.taskID, "status": "QUEUED"})
			return
		}
		if f.pollDelay > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(f.pollDelay):
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": f.taskID, "status": f.status, "result_json": f.result})
	})
}

func newFake(t *testing.T, status, result string) (*fakeTasks, *httptest.Server) {
	f := &fakeTasks{status: status, result: result, taskID: "tsk_text_1"}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return f, srv
}

func postedParams(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body struct {
		Capability string         `json:"capability"`
		Params     map[string]any `json:"params"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("posted body is not JSON: %v", err)
	}
	return body.Params
}

func TestTextReadsGeneratedTextFromResultJSON(t *testing.T) {
	f, srv := newFake(t, "SUCCEEDED", `{"text":"门店开业活动欢迎到店"}`)
	c := New(srv.URL, "task-token-not-logged", "acct_test", "leads-engine")
	got := c.Text(context.Background(), TextInput{IdempotencyKey: "lc:k1", Capability: "text.generate", Prompt: "写一句开业邀请", Language: "zh"})
	if got.Text != "门店开业活动欢迎到店" || got.BillingVerdict != "recorded" || got.CostCents != 1 || got.TaskID != "tsk_text_1" {
		t.Fatalf("text = %+v", got)
	}
	params := postedParams(t, f.posted)
	if params["prompt"] != "写一句开业邀请" {
		t.Fatalf("prompt not sent under prompt key: %v", params)
	}
	if _, ok := params["body"]; ok {
		t.Fatalf("body key would be dropped by the MaaS adapter: %v", params)
	}
}

func TestTextNonJSONResultIsRecordedWithoutText(t *testing.T) {
	_, srv := newFake(t, "SUCCEEDED", "PASS")
	c := New(srv.URL, "task-token-not-logged", "acct_test", "leads-engine")
	got := c.Text(context.Background(), TextInput{IdempotencyKey: "lc:k2", Capability: "text.generate", Prompt: "x"})
	if got.Text != "" || got.BillingVerdict != "recorded" || got.TaskID != "tsk_text_1" {
		t.Fatalf("succeeded without text = %+v", got)
	}
	if strings.Contains(got.Text, "PASS") {
		t.Fatalf("verdict text leaked into body: %+v", got)
	}
}

func TestTextEmptyTextFieldIsNotUsable(t *testing.T) {
	_, srv := newFake(t, "SUCCEEDED", `{"text":"   "}`)
	c := New(srv.URL, "task-token-not-logged", "acct_test", "leads-engine")
	got := c.Text(context.Background(), TextInput{IdempotencyKey: "lc:k3", Capability: "text.generate", Prompt: "x"})
	if got.Text != "" {
		t.Fatalf("blank text accepted: %+v", got)
	}
}

func TestTextFailedTaskIsNotCompleted(t *testing.T) {
	_, srv := newFake(t, "FAILED", "")
	c := New(srv.URL, "task-token-not-logged", "acct_test", "leads-engine")
	got := c.Text(context.Background(), TextInput{IdempotencyKey: "lc:k4", Capability: "text.generate", Prompt: "x"})
	if got.BillingVerdict != "not_completed" || got.CostCents != 0 || got.Text != "" {
		t.Fatalf("failed task = %+v", got)
	}
}

func TestTextTimeoutKeepsTaskIDAsNotCompleted(t *testing.T) {
	f, srv := newFake(t, "RUNNING", "")
	f.pollDelay = 2 * time.Second
	c := New(srv.URL, "task-token-not-logged", "acct_test", "leads-engine").WithTimeout(60 * time.Millisecond)
	got := c.Text(context.Background(), TextInput{IdempotencyKey: "lc:k5", Capability: "text.generate", Prompt: "x"})
	if got.BillingVerdict != "not_completed" || got.CostCents != 0 || got.TaskID != "tsk_text_1" {
		t.Fatalf("timeout = %+v", got)
	}
}

func TestPhraseSendsPromptAndReturnsText(t *testing.T) {
	f, srv := newFake(t, "SUCCEEDED", `{"text":"每天 9:00 到 18:00，欢迎到店"}`)
	c := New(srv.URL, "task-token-not-logged", "acct_test", "leads-engine")
	got := c.Phrase(context.Background(), PhraseInput{IdempotencyKey: "sess:msg", Language: "zh", Body: "每天 9:00 到 18:00", CitationIDs: []string{"kn_1"}})
	if got.Text != "每天 9:00 到 18:00，欢迎到店" || got.BillingVerdict != "recorded" || got.CostCents != 1 {
		t.Fatalf("phrase = %+v", got)
	}
	params := postedParams(t, f.posted)
	if prompt, _ := params["prompt"].(string); !strings.Contains(prompt, "每天 9:00 到 18:00") {
		t.Fatalf("phrase prompt missing grounded answer: %v", params)
	}
	if _, ok := params["body"]; ok {
		t.Fatalf("phrase must not send body key: %v", params)
	}
}
