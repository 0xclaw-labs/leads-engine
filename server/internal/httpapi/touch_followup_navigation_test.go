package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/bianjiefilm/leads-engine/server/internal/tenantmap"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func followUpServiceCall(t *testing.T, h *harness, path string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", h.srv.URL+path, bytes.NewReader(raw))
	req.Header.Set("X-Internal-Token", h.api.Cfg.InternalToken)
	req.Header.Set("Content-Type", "application/json")
	mac := hmac.New(sha256.New, []byte(h.api.Cfg.InternalToken))
	mac.Write([]byte("touch-follow-up/v1\n" + path + "\n"))
	mac.Write(raw)
	req.Header.Set("X-Touch-Follow-Up-Signature", hex.EncodeToString(mac.Sum(nil)))
	res, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(payload, &out)
	return res.StatusCode, out
}

func TestTouchFollowUpResolvesOriginalTupleAndCurrentAuthority(t *testing.T) {
	h, src, secret := ingestHarness(t)
	target, _, _ := h.seed()
	source := "touch-source-without-crm-tenant"
	h.api.Cfg.TenantBindings = []tenantmap.Binding{scopedTenantBinding("touch-engine", source, target, 1)}
	expected := map[string]string{}
	for _, ref := range []string{"followup-one", "followup-two"} {
		src.put(ref, recordJSON(source, ref, "同一联系人", "13900000881", "", "", "granted", "lead_submission", true, true, true))
		body := ingestEvent(source, "event-"+ref, ref, 1, "lead.authorized_submitted", "nfc", false, "同一联系人", "13900000881", "")
		res := postIngest(t, h, body, signBody(secret, body))
		if res.status != 200 {
			t.Fatalf("ingest=%d %s", res.status, res.raw)
		}
		expected[ref] = res.json["lead_id"].(string)
	}
	resolve := func(ref, principal string) (int, map[string]any) {
		return followUpServiceCall(t, h, "/internal/v1/touch-follow-ups/resolve", map[string]any{"source_tenant_id": source, "source_ref": ref, "source_revision": 1, "principal_id": principal})
	}
	for ref, lead := range expected {
		status, out := resolve(ref, principalOwnerA)
		if status != 200 || out["crm_tenant_id"] != target || out["lead_id"] != lead {
			t.Errorf("exact source resolution=%d %v want %s/%s", status, out, target, lead)
		}
	}
	if status, out := resolve("followup-one", principalOwnerB); status != 403 {
		t.Errorf("cross member=%d %v", status, out)
	}
	if status, out := resolve("missing", principalOwnerA); status != 404 {
		t.Errorf("pending/missing=%d %v", status, out)
	}
	src.put("followup-one", recordJSON(source, "followup-one", "", "", "", "", "revoked", "lead_submission", false, false, false))
	if status, out := resolve("followup-one", principalOwnerA); status != 409 {
		t.Errorf("source withdrawal before Notify must veto=%d %v", status, out)
	}
	if status, out := resolve("followup-two", principalOwnerA); status != 200 {
		t.Errorf("other source on same phone lost=%d %v", status, out)
	}
}

func TestTouchFollowUpOneTimeResumeAndFreshRevocation(t *testing.T) {
	h, src, secret := ingestHarness(t)
	target, _, _ := h.seed()
	source := "navigation-source"
	h.api.Cfg.TenantBindings = []tenantmap.Binding{scopedTenantBinding("touch-engine", source, target, 1)}
	ref := "navigation-ref"
	src.put(ref, recordJSON(source, ref, "原线索", "13900000882", "", "", "granted", "lead_submission", true, true, true))
	body := ingestEvent(source, "evt-navigation", ref, 1, "lead.authorized_submitted", "nfc", false, "原线索", "13900000882", "")
	ingested := postIngest(t, h, body, signBody(secret, body))
	if ingested.status != 200 {
		t.Fatalf("ingest=%d %s", ingested.status, ingested.raw)
	}
	register := func(handoff string) {
		t.Helper()
		status, out := followUpServiceCall(t, h, "/internal/v1/touch-follow-ups/register", map[string]any{"source_tenant_id": source, "source_ref": ref, "source_revision": 1, "principal_id": principalOwnerA, "origin_store_id": "original-store", "handoff_id": handoff})
		if status != 201 {
			t.Fatalf("register=%d %v", status, out)
		}
	}
	first := "hnd_11111111111111111111111111111111"
	register(first)
	path := "/api/v1/touch-follow-ups/" + first + "/resume"
	// Knowledge of h never substitutes for a real session or principal.
	if status, out, _ := h.do("POST", path, "", "", ""); status != 401 {
		t.Fatalf("no session=%d %v", status, out)
	}
	if status, out, _ := h.do("POST", path, sessionStranger, "", ""); status != 403 {
		t.Fatalf("foreign member=%d %v", status, out)
	}
	if _, err := h.api.St.DB.Exec(`UPDATE members SET enabled=0 WHERE tenant_id=? AND principal_ref=?`, target, principalOwnerA); err != nil {
		t.Fatal(err)
	}
	if status, out, _ := h.do("POST", path, sessionOwnerA, "", ""); status != 403 {
		t.Fatalf("revoked identity=%d %v", status, out)
	}
	if _, err := h.api.St.DB.Exec(`UPDATE members SET enabled=1 WHERE tenant_id=? AND principal_ref=?`, target, principalOwnerA); err != nil {
		t.Fatal(err)
	}
	status, out, _ := h.do("POST", path, sessionOwnerA, "client-cannot-select-tenant", "")
	if status != 200 || out["crm_tenant_id"] != target || !strings.HasPrefix(fmt.Sprint(out["path"]), "/leads/"+fmt.Sprint(ingested.json["lead_id"])+"?touch_follow_up=fuv_") {
		t.Fatalf("resume=%d %v", status, out)
	}

	// The original detail can request a shell return. A disabled target stays
	// a visible, retryable failure; no URL or return context is fabricated.
	parsed, _ := url.Parse(fmt.Sprint(out["path"]))
	visit := parsed.Query().Get("touch_follow_up")
	returnPath := "/api/v1/touch-follow-up-visits/" + visit + "/return"
	disabledTarget := true
	originalTransport := http.DefaultTransport
	if h.api.ID.HTTP != nil && h.api.ID.HTTP.Transport != nil {
		originalTransport = h.api.ID.HTTP.Transport
	}
	h.api.ID.HTTP = &http.Client{Transport: followUpReturnTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/internal/v1/identity/shell/launch" {
			return originalTransport.RoundTrip(r)
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["principal_id"] != principalOwnerA || in["tenant_id"] != source || in["app_id"] != "touch-engine" || in["target_id"] != "ti-touch-engine-web" {
			t.Errorf("return shell authority=%v", in)
		}
		status := 200
		payload := `{"launch_url":"https://touch-followup.modelxing.com/?code=original&h=hnd_33333333333333333333333333333333"}`
		if disabledTarget {
			status = 403
			payload = `{"error":"target_disabled"}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}
	if status, out, _ := h.do("POST", returnPath, sessionOwnerA, target, ""); status != 503 || out["error"] != "return_target_unavailable" {
		t.Fatalf("unregistered return=%d %v", status, out)
	}
	srcURL, _ := url.Parse(src.url)
	proxy := httputil.NewSingleHostReverseProxy(srcURL)
	registered := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/internal/lead-follow-ups/") && strings.HasSuffix(r.URL.Path, "/returns") {
			if r.Header.Get("X-Leads-Tenant") != source || r.Header.Get("X-Service-ID") == "" || r.Header.Get("X-Service-Token") == "" {
				t.Error("return did not use original receiver identity")
			}
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if len(in) != 2 || in["principal_id"] != principalOwnerA || in["handoff_id"] != "hnd_33333333333333333333333333333333" {
				t.Errorf("return accepts only original principal and shell h=%v", in)
			}
			registered++
			w.WriteHeader(201)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer receiver.Close()
	h.api.Cfg.IngestSources[0].FetchBase = receiver.URL
	disabledTarget = false
	if status, out, _ := h.do("POST", returnPath, sessionOwnerA, target, ""); status != 200 || out["launch_url"] != "https://touch-followup.modelxing.com/?code=original&h=hnd_33333333333333333333333333333333" || registered != 1 {
		t.Fatalf("return target retry=%d %v registered=%d", status, out, registered)
	}
	if status, out, _ := h.do("POST", path, sessionOwnerA, "", ""); status != 410 {
		t.Fatalf("replay=%d %v", status, out)
	}
	// A late source withdrawal is checked again after SSO, without Notify.
	second := "hnd_22222222222222222222222222222222"
	register(second)
	src.put(ref, recordJSON(source, ref, "", "", "", "", "revoked", "lead_submission", false, false, false))
	if status, out, _ := h.do("POST", "/api/v1/touch-follow-ups/"+second+"/resume", sessionOwnerA, "", ""); status != 409 {
		t.Fatalf("withdrawn after mint=%d %v", status, out)
	}
	// Failed resolution/withdrawal does not consume or reset any original fact.
	n, err := h.api.St.GetFollowUpNavigation(second)
	if err != nil || n.UsedAt != 0 {
		t.Fatalf("denied navigation consumed=%+v %v", n, err)
	}
	if _, err := h.api.St.DB.Exec(`UPDATE follow_up_navigation_contexts SET expires_at=1 WHERE handoff_id=?`, second); err != nil {
		t.Fatal(err)
	}
	if status, out, _ := h.do("POST", "/api/v1/touch-follow-ups/"+second+"/resume", sessionOwnerA, "", ""); status != 410 {
		t.Fatalf("expired=%d %v", status, out)
	}
}

func TestTouchFollowUpRequiresServiceProof(t *testing.T) {
	h, _, _ := ingestHarness(t)
	status, out, _ := h.do("POST", "/internal/v1/touch-follow-ups/resolve", sessionOwnerA, "", `{"source_tenant_id":"x","source_ref":"y","source_revision":1,"principal_id":"victim"}`)
	if status != 401 {
		t.Fatalf("browser BFF token cannot assert principal=%d %v", status, out)
	}
}

type followUpReturnTransport func(*http.Request) (*http.Response, error)

func (f followUpReturnTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
