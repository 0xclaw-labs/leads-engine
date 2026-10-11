package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const matrixHash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

// fakeAG10 mirrors the pinned AG10 draft contract (a4b352f4): a create returns
// 201 once, then 200 with idempotent_replay for the same fields. It records
// what the leads side sent. It is a test double, not a live service.
type fakeAG10 struct {
	srv        *httptest.Server
	mu         sync.Mutex
	calls      int
	creates    int
	lastTenant string
	lastBody   map[string]any
	failStatus int
	failCode   string
}

func newFakeAG10(t *testing.T) *fakeAG10 {
	t.Helper()
	f := &fakeAG10{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/v1/matrix-drafts" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"not found"}}`))
			return
		}
		f.calls++
		f.lastTenant = r.Header.Get("X-Matrix-Tenant")
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.lastBody = body
		if f.failStatus != 0 {
			w.WriteHeader(f.failStatus)
			_, _ = w.Write([]byte(`{"error":{"code":"` + f.failCode + `","message":"refused"}}`))
			return
		}
		replay := f.creates > 0
		if !replay {
			f.creates = 1
		}
		status := http.StatusCreated
		if replay {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"draft_id":"d_fake_1","schema_version":"matrix-draft/v1","source_app":"leads","campaign_id":"` + stringOf(body["campaign_id"]) + `","revision":3,"asset_id":"asset_copy_1","asset_hash":"` + matrixHash + `","content_type":"copy","tenant_id":"` + f.lastTenant + `","status":"draft","published":false,"charged":false,"idempotent_replay":` + boolText(replay) + `}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAG10) stats() (creates int, tenant string, body map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates, f.lastTenant, f.lastBody
}

func (f *fakeAG10) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeAG10) refuseNext(status int, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failStatus = status
	f.failCode = code
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func seedTouchLead(t *testing.T, h *harness, tenant string) string {
	t.Helper()
	intake := h.intake(sessionOwnerA, tenant, `{
		"source_app":"touch","source_ns":"landing","event_id":"evt-matrix-1",
		"contact":{"name":"联系人甲","phone":"13900001111","email":"a@shop.cn"},
		"business_category":"merchant_customer","source_type":"touch_campaign",
		"source":{"source_app":"touch","source_ref":"camp-matrix","auth_scope_snapshot":"scope-matrix"},
		"consent":{"source_submission_ref":"sub-matrix","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":false}
	}`, 201)
	id, _ := intake["lead_id"].(string)
	if id == "" {
		t.Fatalf("intake: %v", intake)
	}
	return id
}

func matrixBody(overrides string) string {
	base := `{"asset_id":"asset_copy_1","asset_hash":"` + matrixHash + `","revision":3,"content_type":"copy",
		"grant_ref":"grant_1","license_ref":"license_1","brand_id":"brand_1","payer_id":"payer_1"}`
	if overrides == "" {
		return base
	}
	return overrides
}

func TestMatrixDraftOffIsInvisible(t *testing.T) {
	h := newHarness(t)
	tenant, _, _ := h.seed()
	status, _, _ := h.do("POST", "/api/v1/campaigns/lead_missing/matrix-drafts", sessionOwnerA, tenant, matrixBody(""))
	if status != 404 {
		t.Fatalf("post with flag off = %d, want 404", status)
	}
	status, _, _ = h.do("GET", "/api/v1/campaigns/lead_missing/matrix-drafts", sessionOwnerA, tenant, "")
	if status != 404 {
		t.Fatalf("get with flag off = %d, want 404", status)
	}
}

func TestMatrixDraftMissingURLFailsClosed(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureMatrixHandoff: true})
	tenant, _, _ := h.seed()
	leadID := seedTouchLead(t, h, tenant)
	status, out, _ := h.do("POST", "/api/v1/campaigns/"+leadID+"/matrix-drafts", sessionOwnerA, tenant, matrixBody(""))
	if status != 503 || stringOf(errCode(out)) != "config_gate_matrix" {
		t.Fatalf("missing URL = %d %v, want 503 config_gate_matrix", status, out)
	}
}

func TestMatrixDraftCreatesOnceReplaysAndNeverClaimsPublish(t *testing.T) {
	ag10 := newFakeAG10(t)
	h := newHarnessOpts(t, harnessOpts{featureMatrixHandoff: true, matrixDraftURL: ag10.srv.URL})
	tenant, _, _ := h.seed()
	leadID := seedTouchLead(t, h, tenant)
	path := "/api/v1/campaigns/" + leadID + "/matrix-drafts"

	status, out, _ := h.do("POST", path, sessionOwnerA, tenant, matrixBody(""))
	if status != 201 {
		t.Fatalf("first post = %d %v, want 201", status, out)
	}
	if out["draft_id"] != "d_fake_1" || out["status"] != "draft" || out["published"] != false || out["charged"] != false {
		t.Fatalf("first body = %v", out)
	}
	for _, k := range []string{"matrix_plan_id", "matrix_item_id", "external_post_id"} {
		if out[k] != nil {
			t.Fatalf("%s = %v, want null (unknown, not zero)", k, out[k])
		}
	}
	if out["idempotent_replay"] != false {
		t.Fatalf("first idempotent_replay = %v", out["idempotent_replay"])
	}
	creates, sentTenant, sent := ag10.stats()
	if creates != 1 || sentTenant != tenant {
		t.Fatalf("ag10 creates=%d tenant=%q", creates, sentTenant)
	}
	if sent["source_app"] != "leads" || sent["content_type"] != "copy" || sent["tenant_id"] != tenant {
		t.Fatalf("ag10 body = %v", sent)
	}
	rt, _ := sent["return_target"].(map[string]any)
	if rt["app"] != "leads" || rt["path"] != "/campaigns/"+leadID {
		t.Fatalf("return_target = %v", rt)
	}
	for _, forbidden := range []string{"phone", "email", "contact", "customer_list", "message_body", "crm_profile"} {
		if _, ok := sent[forbidden]; ok {
			t.Fatalf("ag10 received forbidden field %s", forbidden)
		}
	}

	status, out, _ = h.do("POST", path, sessionOwnerA, tenant, matrixBody(""))
	if status != 200 || out["idempotent_replay"] != true || out["draft_id"] != "d_fake_1" {
		t.Fatalf("replay = %d %v, want 200 replay of d_fake_1", status, out)
	}
	if creates, _, _ = ag10.stats(); creates != 1 || ag10.callCount() != 2 {
		t.Fatalf("replay: creates=%d calls=%d, want creates=1 calls=2 (service decides every call)", creates, ag10.callCount())
	}

	list := h.mustDo("GET", path, sessionOwnerA, tenant, "", 200)
	drafts, _ := list["drafts"].([]any)
	if len(drafts) != 1 {
		t.Fatalf("list = %v", list)
	}
	notice, _ := out["notice"].(string)
	for _, phrase := range []string{"已发布", "已送出", "已触达", "已扣费", "已成交"} {
		if strings.Contains(notice, phrase) {
			t.Fatalf("notice claims a finished action: %s", notice)
		}
	}
}

func TestMatrixDraftReplayAfterRevokeIsRefusedAndKeepsRow(t *testing.T) {
	ag10 := newFakeAG10(t)
	h := newHarnessOpts(t, harnessOpts{featureMatrixHandoff: true, matrixDraftURL: ag10.srv.URL})
	tenant, _, _ := h.seed()
	leadID := seedTouchLead(t, h, tenant)
	path := "/api/v1/campaigns/" + leadID + "/matrix-drafts"

	if status, out, _ := h.do("POST", path, sessionOwnerA, tenant, matrixBody("")); status != 201 {
		t.Fatalf("first = %d %v", status, out)
	}
	ag10.refuseNext(http.StatusForbidden, "grant_revoked")
	status, out, _ := h.do("POST", path, sessionOwnerA, tenant, matrixBody(""))
	if status != 403 || stringOf(errCode(out)) != "grant_revoked" {
		t.Fatalf("replay after revoke = %d %v, want 403 grant_revoked", status, out)
	}
	list := h.mustDo("GET", path, sessionOwnerA, tenant, "", 200)
	if drafts, _ := list["drafts"].([]any); len(drafts) != 1 {
		t.Fatalf("revoke refusal changed the stored row: %v", list)
	}
}

func TestMatrixDraftRejectsImageAndPrivateFieldsBeforeAG10(t *testing.T) {
	ag10 := newFakeAG10(t)
	h := newHarnessOpts(t, harnessOpts{featureMatrixHandoff: true, matrixDraftURL: ag10.srv.URL})
	tenant, _, _ := h.seed()
	leadID := seedTouchLead(t, h, tenant)
	path := "/api/v1/campaigns/" + leadID + "/matrix-drafts"

	status, out, _ := h.do("POST", path, sessionOwnerA, tenant, matrixBody(`{"asset_id":"asset_img_1","asset_hash":"`+matrixHash+`","revision":3,"content_type":"image","grant_ref":"g","license_ref":"l","brand_id":"b","payer_id":"p"}`))
	if status != 422 || stringOf(errCode(out)) != "wrong_type" {
		t.Fatalf("image = %d %v, want 422 wrong_type", status, out)
	}
	status, out, _ = h.do("POST", path, sessionOwnerA, tenant, matrixBody(`{"asset_id":"asset_copy_1","asset_hash":"`+matrixHash+`","revision":3,"grant_ref":"g","license_ref":"l","brand_id":"b","payer_id":"p","phone":"13900002222"}`))
	if status != 400 {
		t.Fatalf("private field = %d %v, want 400", status, out)
	}
	if creates, _, _ := ag10.stats(); creates != 0 {
		t.Fatalf("rejected requests reached ag10: creates=%d", creates)
	}
}

func TestMatrixDraftPassesGrantRefusalThroughWithoutLocalRow(t *testing.T) {
	ag10 := newFakeAG10(t)
	ag10.refuseNext(http.StatusForbidden, "grant_revoked")
	h := newHarnessOpts(t, harnessOpts{featureMatrixHandoff: true, matrixDraftURL: ag10.srv.URL})
	tenant, _, _ := h.seed()
	leadID := seedTouchLead(t, h, tenant)
	path := "/api/v1/campaigns/" + leadID + "/matrix-drafts"

	status, out, _ := h.do("POST", path, sessionOwnerA, tenant, matrixBody(""))
	if status != 403 || stringOf(errCode(out)) != "grant_revoked" {
		t.Fatalf("revoked = %d %v, want 403 grant_revoked", status, out)
	}
	list := h.mustDo("GET", path, sessionOwnerA, tenant, "", 200)
	if drafts, _ := list["drafts"].([]any); len(drafts) != 0 {
		t.Fatalf("refused draft was stored: %v", list)
	}
}

func TestMatrixDraftUnavailableWritesNothingAndSaysSo(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureMatrixHandoff: true, matrixDraftURL: "http://127.0.0.1:1"})
	tenant, _, _ := h.seed()
	leadID := seedTouchLead(t, h, tenant)
	path := "/api/v1/campaigns/" + leadID + "/matrix-drafts"

	status, out, _ := h.do("POST", path, sessionOwnerA, tenant, matrixBody(""))
	if status != 503 || stringOf(errCode(out)) != "matrix_unavailable" {
		t.Fatalf("unreachable = %d %v, want 503 matrix_unavailable", status, out)
	}
	list := h.mustDo("GET", path, sessionOwnerA, tenant, "", 200)
	if drafts, _ := list["drafts"].([]any); len(drafts) != 0 {
		t.Fatalf("unreachable service left a row: %v", list)
	}
}

func TestMatrixDraftOtherTenantCannotReachCampaign(t *testing.T) {
	ag10 := newFakeAG10(t)
	h := newHarnessOpts(t, harnessOpts{featureMatrixHandoff: true, matrixDraftURL: ag10.srv.URL})
	tenantA, tenantB, _ := h.seed()
	leadID := seedTouchLead(t, h, tenantA)
	path := "/api/v1/campaigns/" + leadID + "/matrix-drafts"

	status, _, _ := h.do("POST", path, sessionOwnerB, tenantB, matrixBody(""))
	if status != 404 {
		t.Fatalf("tenant b post = %d, want 404", status)
	}
	if creates, _, _ := ag10.stats(); creates != 0 {
		t.Fatalf("tenant b reached ag10: creates=%d", creates)
	}
}

// errCode reads the code from fail(): {"error": "<code>", "message": "..."}.
func errCode(out map[string]any) any {
	return out["error"]
}
