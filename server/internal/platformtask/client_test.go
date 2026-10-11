package platformtask

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// HUI-1682: the platform task is the only live text seam. Generate follows the
// Phrase pattern exactly: hold 0.01 CNY, submit, poll, record one cent on
// SUCCEEDED only. Everything else stays not_completed and never writes text.

func TestGenerateReturnsTextOnSucceededTask(t *testing.T) {
	var postBody map[string]any
	var gotPath, gotToken, gotApp string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			gotPath = r.URL.Path
			gotToken = r.Header.Get("X-PilotSeaView-Internal-Token")
			gotApp = r.Header.Get("X-App-ID")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &postBody)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "tsk_text_1", "status": "QUEUED"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task_id": "tsk_text_1", "status": "SUCCEEDED",
			"result_json": `{"text":"根据门店活动写好的文案"}`,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "task-token-not-logged", "acct_test", "leads-engine")
	got := c.Generate(context.Background(), GenerateInput{
		IdempotencyKey: "lightcopy:opp:1:reply:v1", Language: "zh",
		Prompt: "title=门店开业 activity_ref=camp-9",
	})
	if got.BillingVerdict != "recorded" || got.CostCents != 1 || got.TaskID != "tsk_text_1" {
		t.Fatalf("generate = %+v", got)
	}
	if got.Text != "根据门店活动写好的文案" {
		t.Fatalf("text = %q", got.Text)
	}
	if gotPath != "/internal/v1/tasks/text.generate" {
		t.Fatalf("submit path = %s", gotPath)
	}
	if gotToken == "" || gotApp != "leads-engine" {
		t.Fatalf("auth headers missing: %q %q", gotToken, gotApp)
	}
	if postBody["capability"] != "text.generate" {
		t.Fatalf("capability = %v", postBody["capability"])
	}
	billing, _ := postBody["billing"].(map[string]any)
	if billing["mode"] != "hold" || billing["amount_cny"].(float64) != 0.01 {
		t.Fatalf("billing = %v", billing)
	}
	params, _ := postBody["params"].(map[string]any)
	if params["prompt"] != "title=门店开业 activity_ref=camp-9" {
		t.Fatalf("params = %v", params)
	}
	if postBody["idempotency_key"] != "lightcopy:opp:1:reply:v1" {
		t.Fatalf("idempotency = %v", postBody["idempotency_key"])
	}
}

func TestGenerateCapabilityAndModelOverride(t *testing.T) {
	var gotPath string
	var postBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &postBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "t2", "status": "SUCCEEDED", "result_json": `{"text":"好"}`})
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "acct", "leads-engine")
	c.TextCapability = "leads.text.v2"
	c.TextModel = "glm-5"
	got := c.Generate(context.Background(), GenerateInput{IdempotencyKey: "k", Language: "zh", Prompt: "p"})
	if got.BillingVerdict != "recorded" || got.Text != "好" {
		t.Fatalf("generate = %+v", got)
	}
	if gotPath != "/internal/v1/tasks/leads.text.v2" || postBody["capability"] != "leads.text.v2" {
		t.Fatalf("capability override lost: %s %v", gotPath, postBody["capability"])
	}
	params, _ := postBody["params"].(map[string]any)
	if params["model"] != "glm-5" {
		t.Fatalf("model = %v", params["model"])
	}
}

func TestGenerateFailedTaskStaysNotCompleted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "tsk_text_f", "status": "QUEUED"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "tsk_text_f", "status": "FAILED", "error_code": "no_model_upstream"})
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "acct", "leads-engine")
	got := c.Generate(context.Background(), GenerateInput{IdempotencyKey: "k", Language: "zh", Prompt: "p"})
	if got.BillingVerdict != "not_completed" || got.CostCents != 0 || got.Text != "" {
		t.Fatalf("generate = %+v", got)
	}
	if got.TaskID != "tsk_text_f" {
		t.Fatalf("task id must survive for reconcile: %+v", got)
	}
}

func TestGenerateStuckTaskTimesOutNotCompleted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "tsk_text_slow", "status": "QUEUED"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "tsk_text_slow", "status": "RUNNING"})
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "acct", "leads-engine")
	c.HTTP.Timeout = 60 * time.Millisecond
	got := c.Generate(context.Background(), GenerateInput{IdempotencyKey: "k", Language: "zh", Prompt: "p"})
	if got.BillingVerdict != "not_completed" || got.CostCents != 0 || got.Text != "" {
		t.Fatalf("generate = %+v", got)
	}
}

func TestGenerateSubmitErrorStaysNotCompleted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "acct", "leads-engine")
	got := c.Generate(context.Background(), GenerateInput{IdempotencyKey: "k", Language: "zh", Prompt: "p"})
	if got.BillingVerdict != "not_completed" || got.CostCents != 0 || got.TaskID != "" {
		t.Fatalf("generate = %+v", got)
	}
}

func TestGenerateSucceededWithoutUsableTextKeepsHonestCharge(t *testing.T) {
	for name, raw := range map[string]string{
		"plain":   "PASS",
		"nottext": `{"audio_url":"https://x"}`,
		"empty":   `{"text":"   "}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPost {
				_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "tsk_x", "status": "QUEUED"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "tsk_x", "status": "SUCCEEDED", "result_json": raw})
		}))
		c := New(srv.URL, "tok", "acct", "leads-engine")
		got := c.Generate(context.Background(), GenerateInput{IdempotencyKey: "k", Language: "zh", Prompt: "p"})
		srv.Close()
		if got.BillingVerdict != "recorded" || got.CostCents != 1 || strings.TrimSpace(got.Text) != "" {
			t.Fatalf("%s: succeeded task with unusable result must stay recorded with empty text: %+v", name, got)
		}
	}
}

func TestGenerateNilClientStaysNotCompleted(t *testing.T) {
	var c *Client
	got := c.Generate(context.Background(), GenerateInput{IdempotencyKey: "k"})
	if got.BillingVerdict != "not_completed" {
		t.Fatalf("generate = %+v", got)
	}
}
