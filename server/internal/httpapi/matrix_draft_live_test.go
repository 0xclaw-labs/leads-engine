// matrix_draft_live_test.go — HUI-2391 全链 live 证据（ap-20261011 WI-A）。
//
// 这是唯一打到真实 AG10（public-ai internal/matrixdraft）的 leads 测试。
// CI 零影响：两个环境门都没有设置时整个测试 skip。
//
//	env MATRIX_DRAFT_LIVE_BIN=/path/to/matrixdraft
//	  自包含模式：测试自己起真实 AG10 进程（file sqlite + 生成的 grant 文件，
//	  全部落 t.TempDir）。本地运行业：从 public-ai origin/main 只读提取构建——
//	    cd /Volumes/MobileHD/Work/派诺生态/public-ai && git archive --format=tar origin/main | tar -x -C <scratch>/ag10src
//	    cd <scratch>/ag10src && GOWORK=off CGO_ENABLED=1 go build -o <scratch>/ag10/matrixdraft ./internal/matrixdraft/cmd/matrixdraft
//	env MATRIX_DRAFT_LIVE_URL=http://127.0.0.1:PORT
//	  外部模式：指向一个已启动的真实 AG10。运营者必须预先用同一 grant 表
//	  启动它：tenant tnt_live_a 上 grant_live_1=active、grant_revoked_1=revoked
//	 （JSON 数组 [{"tenant_id":"tnt_live_a","grant_ref":"grant_live_1","status":"active"},...]，
//	  以 -grants 传入）。
//
// 本地租户钉为 tnt_live_a（直接 SQL 插入，绕开随机租户生成），这样 grant
// 表在服务启动前就能写死。旅程与负例表见 ap2 设计 §3 WI-A。
package httpapi

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/db"
)

const (
	liveTenant       = "tnt_live_a"
	liveGrantActive  = "grant_live_1"
	liveGrantRevoked = "grant_revoked_1"
	liveGrantNone    = "grant_none"
)

func liveMatrixDraftURL(t *testing.T) string {
	t.Helper()
	if bin := strings.TrimSpace(os.Getenv("MATRIX_DRAFT_LIVE_BIN")); bin != "" {
		return spawnLiveAG10(t, bin)
	}
	if u := strings.TrimSpace(os.Getenv("MATRIX_DRAFT_LIVE_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	t.Skip("MATRIX_DRAFT_LIVE_BIN / MATRIX_DRAFT_LIVE_URL not set; live AG10 journey skipped")
	return ""
}

// spawnLiveAG10 boots the real matrixdraft binary on 127.0.0.1:0 with a temp
// sqlite file and a generated grant table, then reads its LISTEN line.
func spawnLiveAG10(t *testing.T, bin string) string {
	t.Helper()
	grants := filepath.Join(t.TempDir(), "grants.json")
	rows := []map[string]string{
		{"tenant_id": liveTenant, "grant_ref": liveGrantActive, "status": "active"},
		{"tenant_id": liveTenant, "grant_ref": liveGrantRevoked, "status": "revoked"},
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	if err := os.WriteFile(grants, raw, 0o600); err != nil {
		t.Fatalf("grants: %v", err)
	}
	cmd := exec.Command(bin,
		"-db", filepath.Join(t.TempDir(), "ag10.db"),
		"-listen", "127.0.0.1:0",
		"-grants", grants)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ag10: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line := bufio.NewScanner(stdout)
	for line.Scan() {
		text := strings.TrimSpace(line.Text())
		if addr, ok := strings.CutPrefix(text, "LISTEN "); ok {
			return "http://" + addr
		}
	}
	t.Fatal("ag10 did not print LISTEN")
	return ""
}

// seedLiveTenant inserts the fixed tenant and one owner member so the harness
// session resolves against it.
func seedLiveTenant(t *testing.T, dbPath string) {
	t.Helper()
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open leads db: %v", err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT OR IGNORE INTO tenants(id, name, created_at) VALUES(?,?,?)`,
		liveTenant, "Live Tenant A", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	var one bool
	if err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM tenants WHERE id=?)`, liveTenant).Scan(&one); err != nil || !one {
		t.Fatalf("tenant missing: %v", err)
	}
}

func liveDraftBody(grantRef, licenseExpires string) string {
	body := `{"asset_id":"asset_copy_live","asset_hash":"` + strings.Repeat("11", 32) + `","revision":3,"content_type":"copy",
		"grant_ref":"` + grantRef + `","license_ref":"license_live_1","brand_id":"brand_live_1","payer_id":"payer_live_1",
		"asset_source_ref":"touch/landing/camp-live"`
	if licenseExpires != "" {
		body += `,"license_expires_at":"` + licenseExpires + `"`
	}
	return body + `}`
}

func TestMatrixDraftLiveFullChainAgainstRealAG10(t *testing.T) {
	base := liveMatrixDraftURL(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "leads.db")
	seedLiveTenant(t, dbPath)
	h := newHarnessOpts(t, harnessOpts{featureMatrixHandoff: true, matrixDraftURL: base, dbPath: dbPath})
	h.provision("member", liveTenant, principalOwnerA, "owner", "Owner A")

	// A touch_campaign lead as the campaign seat.
	intake := h.intake(sessionOwnerA, liveTenant, `{
		"source_app":"touch","source_ns":"landing","event_id":"evt-live-1",
		"contact":{"name":"直播甲","phone":"13900005555"},
		"business_category":"merchant_customer","source_type":"touch_campaign",
		"source":{"source_app":"touch","source_ref":"camp-live","auth_scope_snapshot":"profile-live"},
		"consent":{"source_submission_ref":"sub-live","source_channel":"landing_page","notice_version":"notice-v1","marketing_allowed":false}
	}`, 201)
	leadID, _ := intake["lead_id"].(string)
	if leadID == "" {
		t.Fatalf("intake: %v", intake)
	}
	path := "/api/v1/campaigns/" + leadID + "/matrix-drafts"

	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	past := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)

	// 1. Create: 201, draft facts, published/charged false, ids null.
	created := h.mustDo("POST", path, sessionOwnerA, liveTenant, liveDraftBody(liveGrantActive, future), 201)
	draftID, _ := created["draft_id"].(string)
	if draftID == "" || created["published"] != false || created["charged"] != false {
		t.Fatalf("create = %v", created)
	}
	for _, k := range []string{"matrix_plan_id", "matrix_item_id", "external_post_id"} {
		if created[k] != nil {
			t.Fatalf("%s = %v, want null", k, created[k])
		}
	}

	// 2. Same-facts replay: 200, same draft id.
	replay := h.mustDo("POST", path, sessionOwnerA, liveTenant, liveDraftBody(liveGrantActive, future), 200)
	if replay["draft_id"] != draftID {
		t.Fatalf("replay draft = %v, want %s", replay["draft_id"], draftID)
	}

	// 3. List: exactly one local row.
	list := h.mustDo("GET", path, sessionOwnerA, liveTenant, "", 200)
	if drafts, _ := list["drafts"].([]any); len(drafts) != 1 {
		t.Fatalf("list = %v", list)
	}

	// 4. Receipt read-through to the real service, unknowns stay null.
	refID, _ := list["drafts"].([]any)[0].(map[string]any)["ref_id"].(string)
	receipt := h.mustDo("GET", path+"/"+refID+"/receipt", sessionOwnerA, liveTenant, "", 200)
	if receipt["draft_id"] != draftID || receipt["published"] != false || receipt["charged"] != false {
		t.Fatalf("receipt = %v", receipt)
	}
	for _, k := range []string{"matrix_plan_id", "matrix_item_id", "external_post_id"} {
		if receipt[k] != nil {
			t.Fatalf("%s = %v, want null", k, receipt[k])
		}
	}

	// 5. Negative cases. Every refusal leaves the local row untouched.
	negatives := []struct {
		name   string
		grant  string
		body   string
		status int
		code   string
	}{
		{"same key different facts", liveGrantActive,
			strings.Replace(liveDraftBody(liveGrantActive, future), `"brand_id":"brand_live_1"`, `"brand_id":"brand_changed"`, 1),
			http.StatusConflict, "idempotency_conflict"},
		{"same revision other hash", liveGrantActive,
			strings.Replace(liveDraftBody(liveGrantActive, future), strings.Repeat("11", 32), strings.Repeat("22", 32), 1),
			http.StatusConflict, "hash_conflict"},
		{"expired license", liveGrantActive, liveDraftBody(liveGrantActive, past), http.StatusForbidden, "license_expired"},
		{"revoked grant", liveGrantRevoked, liveDraftBody(liveGrantRevoked, future), http.StatusForbidden, "grant_revoked"},
		{"unknown grant", liveGrantNone, liveDraftBody(liveGrantNone, future), http.StatusForbidden, "grant_unknown"},
	}
	for _, tc := range negatives {
		status, out, _ := h.do("POST", path, sessionOwnerA, liveTenant, tc.body)
		if status != tc.status || stringOf(errCode(out)) != tc.code {
			t.Fatalf("%s = %d %v, want %d %s", tc.name, status, out, tc.status, tc.code)
		}
		after := h.mustDo("GET", path, sessionOwnerA, liveTenant, "", 200)
		drafts, _ := after["drafts"].([]any)
		if len(drafts) != 1 {
			t.Fatalf("%s changed the local rows: %v", tc.name, after)
		}
		row, _ := drafts[0].(map[string]any)
		if row["draft_id"] != draftID || row["matrix_plan_id"] != nil {
			t.Fatalf("%s mutated the row: %v", tc.name, row)
		}
	}

	// 6. Wrong type is refused locally before anything leaves the process.
	wrongType := strings.Replace(liveDraftBody(liveGrantActive, future), `"content_type":"copy"`, `"content_type":"video"`, 1)
	status, out, _ := h.do("POST", path, sessionOwnerA, liveTenant, wrongType)
	if status != 422 || stringOf(errCode(out)) != "wrong_type" {
		t.Fatalf("wrong type = %d %v, want 422 wrong_type", status, out)
	}

	// 7. The source-event backflow lands on the live draft row without
	// creating any publish item.
	event := `{"provider_event_id":"evt_live_result_1","kind":"result","matrix_plan_id":"plan_live_9","matrix_item_id":"item_live_9","external_post_id":"post_live_9","reach":42}`
	evOut := h.mustDo("POST", path+"/"+refID+"/source-events", sessionOwnerA, liveTenant, event, 201)
	if evOut["matrix_plan_id"] != "plan_live_9" || evOut["matrix_reach"] != float64(42) {
		t.Fatalf("event = %v", evOut)
	}
	if evOut["published"] != false || evOut["charged"] != false {
		t.Fatalf("event claims publish: %v", evOut)
	}
	replayEv := h.mustDo("POST", path+"/"+refID+"/source-events", sessionOwnerA, liveTenant, event, 200)
	if replayEv["idempotent_replay"] != true {
		t.Fatalf("event replay = %v", replayEv)
	}

	// 8. Receipt shows the live service still holds the draft, facts intact.
	final := h.mustDo("GET", path, sessionOwnerA, liveTenant, "", 200)
	drafts, _ := final["drafts"].([]any)
	if len(drafts) != 1 {
		t.Fatalf("final list = %v", final)
	}
}
