package jevintent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultEndpoint = "https://modelxing.com/v1/systemone"
	DefaultModel    = "jev-1.14.0"
	DefaultTimeout  = 1500 * time.Millisecond
)

// SystemOneRequest matches the live POST /v1/systemone wire contract (jev-1.14.0).
type SystemOneRequest struct {
	Model     string                  `json:"model"`
	State     map[string]any          `json:"state"`
	Questions map[string]QuestionSpec `json:"questions"`
}

// QuestionSpec defines one JEV primitive.
// IMPORTANT: "choice" MUST use Criteria map (never "options"/"choices"), and
// "noul" uses Criteria {"true": ..., "false": ...}. Only "choice" and "noul"
// are used (never "score").
type QuestionSpec struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

// SystemOneResponse matches the live /v1/systemone JSON response.
type SystemOneResponse struct {
	ID      string                  `json:"id"`
	Model   string                  `json:"model"`
	Answers map[string]AnswerDetail `json:"answers"`
	Usage   *Usage                  `json:"usage,omitempty"`
}

// AnswerDetail holds either a choice or a noul answer.
type AnswerDetail struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
}

// Usage reports token accounting returned by /v1/systemone.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Client is a lightweight pure-stdlib HTTP client for /v1/systemone.
type Client struct {
	Endpoint   string
	APIKey     string
	Model      string
	Timeout    time.Duration
	HTTPClient *http.Client
}

// NewClient constructs a /v1/systemone client with pinned defaults (jev-1.14.0, 1.5s timeout).
func NewClient(endpoint, apiKey, model string, timeout time.Duration) *Client {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultEndpoint
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		Endpoint: strings.TrimSpace(endpoint),
		APIKey:   strings.TrimSpace(apiKey),
		Model:    strings.TrimSpace(model),
		Timeout:  timeout,
		HTTPClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Evaluate sends a single parallel /v1/systemone request and decodes the structured answers.
func (c *Client) Evaluate(ctx context.Context, req SystemOneRequest) (SystemOneResponse, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return SystemOneResponse{}, errors.New("jevintent: missing API key")
	}
	if req.Model == "" {
		req.Model = c.Model
		if req.Model == "" {
			req.Model = DefaultModel
		}
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return SystemOneResponse{}, fmt.Errorf("jevintent: marshal request: %w", err)
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return SystemOneResponse{}, fmt.Errorf("jevintent: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: timeout}
	}
	res, err := hc.Do(httpReq)
	if err != nil {
		return SystemOneResponse{}, fmt.Errorf("jevintent: request failed: %w", err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 512<<10))
	if err != nil {
		return SystemOneResponse{}, fmt.Errorf("jevintent: read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(raw))
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return SystemOneResponse{}, fmt.Errorf("jevintent: upstream status %d: %s", res.StatusCode, snippet)
	}

	var parsed SystemOneResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return SystemOneResponse{}, fmt.Errorf("jevintent: decode response: %w", err)
	}
	if len(parsed.Answers) == 0 {
		return SystemOneResponse{}, errors.New("jevintent: empty answers in response")
	}
	return parsed, nil
}
