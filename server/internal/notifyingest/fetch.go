package notifyingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Record is the authorized minimal submission fetched from a registered source.
// Contact fields exist only in this response, which is written into the leads
// database and nowhere else.
type Record struct {
	SourceRevision    int    `json:"source_revision"`
	ConsentRevision   string `json:"consent_revision"`
	Tombstone         bool   `json:"tombstone"`
	SubmissionRef     string `json:"submission_ref"`
	TenantID          string `json:"tenant_id"`
	Name              string `json:"name"`
	Phone             string `json:"phone"`
	Email             string `json:"email"`
	Wechat            string `json:"wechat"`
	ChannelSubjectRef string `json:"channel_subject_ref"`
	// Authorization is granted | revoked | none.
	Authorization string `json:"authorization"`
	// Purpose is lead_submission, or a non-lead purpose such as browse.
	Purpose  string `json:"purpose"`
	Consents struct {
		FormAuthorized bool   `json:"form_authorized"`
		ChannelReply   bool   `json:"channel_reply"`
		MarketingPhone bool   `json:"marketing_phone"`
		MarketingSMS   bool   `json:"marketing_sms"`
		NoticeVersion  string `json:"notice_version"`
	} `json:"consents"`
}

// FetchRecord GETs the deployment-registered source. baseURL is never taken
// from the event. Redirects are refused.
func FetchRecord(ctx context.Context, baseURL, token, tenantID, submissionRef string, serviceID ...string) (Record, error) {
	if strings.TrimSpace(token) == "" {
		return Record{}, errorsNew("fetch token is empty")
	}
	path := "/internal/v1/lead-records/"
	if len(serviceID) > 0 {
		if strings.TrimSpace(serviceID[0]) == "" {
			return Record{}, errorsNew("receiver service identity is empty")
		}
		path = "/api/v1/internal/lead-records/"
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + path + url.PathEscape(submissionRef))
	if err != nil {
		return Record{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Record{}, err
	}
	if len(serviceID) > 0 {
		req.Header.Set("X-Service-ID", serviceID[0])
		req.Header.Set("X-Service-Token", token)
	} else {
		req.Header.Set("X-Internal-Token", token)
	}
	req.Header.Set("X-Leads-Tenant", tenantID)
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errorsNew("redirects are disabled")
		},
	}
	res, err := client.Do(req)
	if err != nil {
		return Record{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if err != nil {
		return Record{}, err
	}
	if res.StatusCode != http.StatusOK {
		return Record{}, fmt.Errorf("source record status %d", res.StatusCode)
	}
	var rec Record
	if err := json.Unmarshal(body, &rec); err != nil {
		return Record{}, err
	}
	if rec.SubmissionRef != submissionRef || rec.TenantID != tenantID {
		return Record{}, errorsNew("source record tenant or submission_ref mismatch")
	}
	return rec, nil
}

func errorsNew(s string) error { return fmt.Errorf("%s", s) }

// ValidateTouchRecord requires the explicit v2 authorization metadata. A
// tombstone is a revocation fact and must carry no usable contact or grant.
func ValidateTouchRecord(rec Record, minimumRevision int) error {
	if rec.SourceRevision < minimumRevision || rec.SourceRevision <= 0 || strings.TrimSpace(rec.ConsentRevision) == "" || rec.Consents.NoticeVersion != rec.ConsentRevision || rec.Purpose != "lead_submission" {
		return errorsNew("invalid source authorization metadata")
	}
	if rec.Tombstone {
		if rec.Authorization != "revoked" || rec.Name != "" || rec.Phone != "" || rec.Email != "" || rec.Wechat != "" || rec.ChannelSubjectRef != "" || rec.Consents.FormAuthorized || rec.Consents.ChannelReply || rec.Consents.MarketingPhone || rec.Consents.MarketingSMS {
			return errorsNew("invalid source tombstone")
		}
	} else if rec.Authorization != "granted" || !rec.Consents.FormAuthorized {
		return errorsNew("invalid live source authorization")
	}
	return nil
}

// Acknowledgement is deliberately the source's strict five-member schema.
type Acknowledgement struct {
	ReceiptID      string `json:"receipt_id"`
	SourceEventID  string `json:"source_event_id"`
	SubmissionID   string `json:"submission_id"`
	SourceRevision int    `json:"source_revision"`
	ReceiverState  string `json:"receiver_state"`
}

// AcknowledgeRecord uses only deployment-registered origin and service identity.
// A conflict (including a not-yet-forwarded original event) remains retryable.
func AcknowledgeRecord(ctx context.Context, baseURL, token, serviceID, tenant string, ack Acknowledgement) error {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(serviceID) == "" {
		return errorsNew("receiver identity is empty")
	}
	body, err := json.Marshal(ack)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/api/v1/internal/lead-records/" + url.PathEscape(ack.SubmissionID) + "/acknowledgements"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-ID", serviceID)
	req.Header.Set("X-Service-Token", token)
	req.Header.Set("X-Leads-Tenant", tenant)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errorsNew("redirects are disabled") }}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		return fmt.Errorf("source acknowledgement status %d", res.StatusCode)
	}
	return nil
}
