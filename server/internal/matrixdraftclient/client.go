// Package matrixdraftclient is the leads-side HTTP client for the AG10
// persistent draft API (public-ai internal/matrixdraft, commit a4b352f4).
//
// It sends only the contract fields below. There is no field for phone, email,
// CRM profile, customer list, or message body, so those cannot be sent by
// construction. Published and Charged are read back and never set here.
// A 200 or 201 means a draft row exists. It does not mean a publish.
package matrixdraftclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	requestSchema = "matrix-draft-request/v1"
	tenantHeader  = "X-Matrix-Tenant"
	sourceApp     = "leads"
	copyType      = "copy"
	maxBody       = 1 << 20
)

var (
	// ErrUnavailable means the draft service could not be reached or replied
	// with a body this client cannot read. The caller may retry with the same fields.
	ErrUnavailable = errors.New("matrixdraftclient: unavailable")
	// ErrNotLeadsCopy means the content type is not copy. Leads drafts are copy only.
	ErrNotLeadsCopy = errors.New("matrixdraftclient: leads drafts are copy only")
	// ErrInvalid means a required local field is empty.
	ErrInvalid = errors.New("matrixdraftclient: invalid request")
)

// APIError is a non-2xx answer from the draft service.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("matrixdraftclient: http %d %s", e.Status, e.Code)
}

// Request is one copy draft for one existing legal content version.
type Request struct {
	ContentType      string
	SourceProjectID  string
	CampaignID       string
	Revision         int
	AssetID          string
	AssetHash        string
	GrantRef         string
	TenantID         string
	BrandID          string
	PayerID          string
	LicenseRef       string
	LicenseExpiresAt *time.Time
	ReturnPath       string
}

// Draft is the stored row as the service reports it.
type Draft struct {
	ID               string     `json:"draft_id"`
	SchemaVersion    string     `json:"schema_version"`
	SourceApp        string     `json:"source_app"`
	SourceProjectID  string     `json:"source_project_id"`
	CampaignID       string     `json:"campaign_id"`
	Revision         int        `json:"revision"`
	AssetID          string     `json:"asset_id"`
	AssetHash        string     `json:"asset_hash"`
	ContentType      string     `json:"content_type"`
	TenantID         string     `json:"tenant_id"`
	Status           string     `json:"status"`
	Published        bool       `json:"published"`
	Charged          bool       `json:"charged"`
	LicenseExpiresAt *time.Time `json:"license_expires_at"`
	IdempotentReplay bool       `json:"idempotent_replay"`
	Replay           bool       `json:"-"`
}

// Client talks to one draft service base URL.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

type wireReturn struct {
	App  string `json:"app"`
	Path string `json:"path"`
}

// wireRequest mirrors createBody in the service. Field names and order are the contract.
type wireRequest struct {
	SchemaVersion    string     `json:"schema_version"`
	SourceApp        string     `json:"source_app"`
	SourceProjectID  string     `json:"source_project_id,omitempty"`
	CampaignID       string     `json:"campaign_id"`
	Revision         int        `json:"revision"`
	AssetID          string     `json:"asset_id"`
	AssetHash        string     `json:"asset_hash"`
	ContentType      string     `json:"content_type"`
	GrantRef         string     `json:"grant_ref"`
	TenantID         string     `json:"tenant_id"`
	BrandID          string     `json:"brand_id"`
	PayerID          string     `json:"payer_id"`
	LicenseRef       string     `json:"license_ref"`
	LicenseExpiresAt *string    `json:"license_expires_at"`
	ReturnTarget     wireReturn `json:"return_target"`
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// CreateDraft posts one copy draft. A repeat with the same fields returns the
// same draft with Replay set. Local checks fail before any network call.
func (c *Client) CreateDraft(ctx context.Context, r Request) (Draft, error) {
	if r.ContentType != "" && r.ContentType != copyType {
		return Draft{}, ErrNotLeadsCopy
	}
	if strings.TrimSpace(r.TenantID) == "" || strings.TrimSpace(r.CampaignID) == "" {
		return Draft{}, ErrInvalid
	}
	body := wireRequest{
		SchemaVersion:   requestSchema,
		SourceApp:       sourceApp,
		SourceProjectID: r.SourceProjectID,
		CampaignID:      r.CampaignID,
		Revision:        r.Revision,
		AssetID:         r.AssetID,
		AssetHash:       r.AssetHash,
		ContentType:     copyType,
		GrantRef:        r.GrantRef,
		TenantID:        r.TenantID,
		BrandID:         r.BrandID,
		PayerID:         r.PayerID,
		LicenseRef:      r.LicenseRef,
		ReturnTarget:    wireReturn{App: sourceApp, Path: r.ReturnPath},
	}
	if r.LicenseExpiresAt != nil {
		s := r.LicenseExpiresAt.UTC().Format(time.RFC3339)
		body.LicenseExpiresAt = &s
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Draft{}, ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/matrix-drafts", bytes.NewReader(raw))
	if err != nil {
		return Draft{}, ErrInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(tenantHeader, r.TenantID)
	return c.do(req)
}

// GetReceipt reads the source receipt of one draft for the tenant that owns it.
func (c *Client) GetReceipt(ctx context.Context, tenantID, draftID string) (Draft, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(draftID) == "" {
		return Draft{}, ErrInvalid
	}
	u := c.BaseURL + "/v1/matrix-drafts/" + url.PathEscape(draftID) + "/receipt"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Draft{}, ErrInvalid
	}
	req.Header.Set(tenantHeader, tenantID)
	return c.do(req)
}

func (c *Client) do(req *http.Request) (Draft, error) {
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Draft{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Draft{}, fmt.Errorf("%w: read: %v", ErrUnavailable, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Draft{}, apiError(resp.StatusCode, raw)
	}
	var d Draft
	if err := json.Unmarshal(raw, &d); err != nil || d.ID == "" {
		return Draft{}, fmt.Errorf("%w: unreadable draft body", ErrUnavailable)
	}
	d.Replay = resp.StatusCode == http.StatusOK || d.IdempotentReplay
	return d, nil
}

func apiError(status int, raw []byte) error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	code := "unknown"
	msg := ""
	if err := json.Unmarshal(raw, &env); err == nil && env.Error.Code != "" {
		code = env.Error.Code
		msg = env.Error.Message
	}
	return &APIError{Status: status, Code: code, Message: msg}
}
