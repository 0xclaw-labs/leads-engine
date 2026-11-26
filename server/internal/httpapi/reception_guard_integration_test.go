package httpapi

import (
	"strings"
	"testing"
)

func TestReceptionGuardHTTPIntegration(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureReception: true})
	tenantA, _, _ := h.seed()
	h.provision("grant", tenantA, principalAgentA)

	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenantA,
		`{"default_mode":"ai","language":"zh"}`, 201)
	widgetID := widget["id"].(string)

	opened := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widgetID+"/sessions", "", "",
		`{"visitor_key":"visitor-key-guard-http1"}`, 201)
	sid := opened["session"].(map[string]any)["id"].(string)

	// 1. Entrusted-order raw/internal-pricing payload MUST be rejected on regular CRM reception endpoint.
	status, body, _ := h.do("POST", "/api/v1/public/reception/sessions/"+sid+"/messages", "", "",
		`{"visitor_key":"visitor-key-guard-http1","client_msg_id":"msg-entrusted-1","text":"请同步 guanlan-order 发包方底价 8800 元"}`)
	if status != 400 || body["error"] != "scope_boundary_violation" {
		t.Fatalf("entrusted-order message must fail with 400 scope_boundary_violation, got %d %v", status, body)
	}

	// 2. Human takeover + approved human reply containing accidental PII/token -> presentation scrubs PII/token.
	h.mustDo("POST", "/api/v1/reception/sessions/"+sid+"/takeover", sessionAgentA, tenantA, `{"epoch":1}`, 200)
	h.mustDo("POST", "/api/v1/reception/sessions/"+sid+"/replies", sessionAgentA, tenantA,
		`{"client_reply_id":"hr-pii-1","body":"请勿外传：13800138000 / wxid_secret_001 / vip@example.com / sk_live_abcdef123456"}`, 201)

	presStatus, presRaw, _ := h.doRaw("GET", "/api/v1/reception/sessions/"+sid+"/presentation", sessionAgentA, tenantA, "")
	if presStatus != 200 {
		t.Fatalf("presentation status = %d %s", presStatus, presRaw)
	}
	presStr := string(presRaw)
	for _, secret := range []string{"13800138000", "wxid_secret_001", "vip@example.com", "sk_live_abcdef123456"} {
		if strings.Contains(presStr, secret) {
			t.Fatalf("presentation leaked secret %q in %s", secret, presStr)
		}
	}
	if !strings.Contains(presStr, `"text":true`) || !strings.Contains(presStr, `"audio":true`) {
		t.Fatalf("approved current-epoch human reply should remain deliverable on presentation: %s", presStr)
	}
}
