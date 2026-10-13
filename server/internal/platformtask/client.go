package platformtask

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	phraseCapability  = "reception.phrase"
	phraseAmountCNY   = 0.01
	phraseCostCents   = 1
	textAmountCNY     = 0.01
	textCostCents     = 1
	pollStart         = 15 * time.Millisecond
	pollMax           = 500 * time.Millisecond
	phraseInstruction = "请用简短、友好的中文回复访客，只改写语气。不得增加任何价格、库存、承诺或数字，不得引入依据之外的事实。依据："
)

// Client calls the platform task API. It never logs the token or the body.
type Client struct {
	BaseURL   string
	Token     string
	AccountID string
	AppID     string
	HTTP      *http.Client
}

// New returns nil unless base URL, token, and account id are all set.
func New(baseURL, token, accountID, appID string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	accountID = strings.TrimSpace(accountID)
	appID = strings.TrimSpace(appID)
	if baseURL == "" || token == "" || accountID == "" {
		return nil
	}
	if appID == "" {
		appID = "leads-engine"
	}
	return &Client{
		BaseURL:   baseURL,
		Token:     token,
		AccountID: accountID,
		AppID:     appID,
		HTTP:      &http.Client{Timeout: 2 * time.Second},
	}
}

// WithTimeout returns a copy whose per-request and total wait is d.
// The reception path keeps 2s; light copy generation needs longer.
func (c *Client) WithTimeout(d time.Duration) *Client {
	if c == nil {
		return nil
	}
	cp := *c
	cp.HTTP = &http.Client{Timeout: d}
	return &cp
}

type taskBilling struct {
	Mode      string  `json:"mode"`
	AmountCNY float64 `json:"amount_cny"`
}

type taskBody struct {
	AppID          string         `json:"app_id"`
	AccountID      string         `json:"account_id"`
	ProjectID      string         `json:"project_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	Capability     string         `json:"capability"`
	Params         map[string]any `json:"params"`
	Billing        taskBilling    `json:"billing"`
}

type taskSummary struct {
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	ResultJSON string `json:"result_json"`
}

// Phrase submits one grounded answer and polls until the task ends.
// Only SUCCEEDED with a task id is recorded, at one cent. Timeouts stay not_completed.
// The prompt carries the grounded answer only; visitor text never leaves the process.
func (c *Client) Phrase(ctx context.Context, in PhraseInput) PhraseResult {
	ids := in.CitationIDs
	if ids == nil {
		ids = []string{}
	}
	sum, ok := c.run(ctx, phraseCapability, in.IdempotencyKey, map[string]any{
		"language":     in.Language,
		"prompt":       phraseInstruction + in.Body,
		"citation_ids": ids,
	}, phraseAmountCNY)
	if !ok {
		return PhraseResult{TaskID: sum.TaskID, BillingVerdict: "not_completed"}
	}
	if sum.Status != "SUCCEEDED" || strings.TrimSpace(sum.TaskID) == "" {
		return PhraseResult{TaskID: sum.TaskID, BillingVerdict: "not_completed"}
	}
	return PhraseResult{TaskID: sum.TaskID, CostCents: phraseCostCents, BillingVerdict: "recorded", Text: resultText(sum.ResultJSON)}
}

// Text submits one prompt to a text capability and polls until the task ends.
// A succeeded task is recorded at one cent even when its text is unusable,
// because the provider has already run it. Only the text is handed back.
func (c *Client) Text(ctx context.Context, in TextInput) TextResult {
	capability := strings.TrimSpace(in.Capability)
	if capability == "" {
		capability = "text.generate"
	}
	sum, ok := c.run(ctx, capability, in.IdempotencyKey, map[string]any{
		"language": in.Language,
		"prompt":   in.Prompt,
	}, textAmountCNY)
	if !ok || sum.Status != "SUCCEEDED" || strings.TrimSpace(sum.TaskID) == "" {
		return TextResult{TaskID: sum.TaskID, BillingVerdict: "not_completed"}
	}
	return TextResult{TaskID: sum.TaskID, Text: resultText(sum.ResultJSON), CostCents: textCostCents, BillingVerdict: "recorded"}
}

// run submits and polls. ok is false on any transport, decode, or timeout
// failure; the summary then carries whatever task id the submit returned.
func (c *Client) run(ctx context.Context, capability, key string, params map[string]any, amount float64) (taskSummary, bool) {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return taskSummary{}, false
	}
	timeout := 2 * time.Second
	if c.HTTP != nil && c.HTTP.Timeout > 0 {
		timeout = c.HTTP.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	payload := taskBody{
		AppID: c.AppID, AccountID: c.AccountID, IdempotencyKey: key,
		Capability: capability, Params: params,
		Billing: taskBilling{Mode: "hold", AmountCNY: amount},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return taskSummary{}, false
	}
	sum, err := c.post(ctx, capability, raw)
	if err != nil {
		return taskSummary{}, false
	}
	wait := pollStart
	for !terminal(sum.Status) {
		if err := sleep(ctx, wait); err != nil {
			return sum, false
		}
		if wait < pollMax {
			wait *= 2
		}
		next, err := c.get(ctx, sum.TaskID)
		if err != nil {
			return sum, false
		}
		sum = next
	}
	return sum, true
}

// resultText reads {"text": "..."} from result_json. Anything else is no text.
func resultText(raw string) string {
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return ""
	}
	return strings.TrimSpace(out.Text)
}

func terminal(status string) bool {
	switch status {
	case "SUCCEEDED", "FAILED", "CANCELED":
		return true
	default:
		return false
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) post(ctx context.Context, capability string, raw []byte) (taskSummary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/internal/v1/tasks/"+url.PathEscape(capability), bytes.NewReader(raw))
	if err != nil {
		return taskSummary{}, err
	}
	c.authorize(req)
	return c.do(req)
}

func (c *Client) get(ctx context.Context, taskID string) (taskSummary, error) {
	u, err := url.Parse(c.BaseURL + "/internal/v1/tasks/" + url.PathEscape(taskID))
	if err != nil {
		return taskSummary{}, err
	}
	q := u.Query()
	q.Set("app_id", c.AppID)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return taskSummary{}, err
	}
	c.authorize(req)
	return c.do(req)
}

func (c *Client) authorize(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-ID", c.AppID)
	req.Header.Set("X-PilotSeaView-Internal-Token", c.Token)
}

func (c *Client) do(req *http.Request) (taskSummary, error) {
	ht := c.HTTP
	if ht == nil {
		ht = http.DefaultClient
	}
	res, err := ht.Do(req)
	if err != nil {
		return taskSummary{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return taskSummary{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return taskSummary{}, errStatus
	}
	var sum taskSummary
	if err := json.Unmarshal(body, &sum); err != nil {
		return taskSummary{}, err
	}
	return sum, nil
}

var errStatus = errString("task status")

type errString string

func (e errString) Error() string { return string(e) }
