package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSessionMembershipsDiscoverOnlyCurrentPrincipalScopes(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, _ := h.seed()
	// The same platform identity may hold two different current memberships.
	h.provision("member", tenantB, principalOwnerA, "sales", "Second workspace")
	hidden := h.provision("tenant", "Other person's workspace")
	h.provision("member", hidden, principalOwnerB, "owner", "Other owner")
	disabled := h.provision("tenant", "Disabled workspace")
	h.provision("member", disabled, principalOwnerA, "owner", "Disabled", "disabled")
	const path = "/api/v1/session/memberships"
	for _, session := range []string{"", "not-an-identity-session"} {
		if status, out, _ := h.do("GET", path, session, "", ""); status != 401 {
			t.Errorf("invalid session=%d %v", status, out)
		}
	}
	for _, session := range []string{sessionStranger, sessionDisabled} {
		status, out, _ := h.do("GET", path, session, "", "")
		if status != 200 {
			t.Fatalf("empty scopes=%d %v", status, out)
		}
		if items, ok := out["items"].([]any); !ok || len(items) != 0 {
			t.Errorf("no available memberships must be []=%v", out)
		}
	}
	assertScopes := func(out map[string]any) {
		t.Helper()
		items, ok := out["items"].([]any)
		if !ok || len(items) != 2 {
			t.Fatalf("multi workspace=%v", out)
		}
		got := map[string]string{}
		for _, raw := range items {
			row := raw.(map[string]any)
			if len(row) != 2 {
				t.Errorf("unexpected principal/member data=%v", row)
			}
			got[row["tenant_id"].(string)] = row["display_name"].(string)
		}
		if got[tenantA] != "Tenant A" || got[tenantB] != "Tenant B" {
			t.Errorf("only current enabled memberships=%v", got)
		}
	}
	status, out, _ := h.do("GET", path, sessionOwnerA, "", "")
	if status != 200 {
		t.Fatalf("discover without selected tenant=%d %v", status, out)
	}
	assertScopes(out)
	// Neither a selected tenant nor a claimed principal changes whose scopes
	// are discovered; the identity service remains the only principal source.
	req, _ := http.NewRequest("GET", h.srv.URL+path+"?principal_id="+principalOwnerB, nil)
	req.Header.Set("X-Internal-Token", h.internalToken)
	req.Header.Set("X-Session-Token", sessionOwnerA)
	req.Header.Set("X-Tenant-ID", hidden)
	req.Header.Set("X-Principal-ID", principalOwnerB)
	res, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var forged map[string]any
	_ = json.NewDecoder(res.Body).Decode(&forged)
	if res.StatusCode != 200 {
		t.Fatalf("forged selectors=%d %v", res.StatusCode, forged)
	}
	assertScopes(forged)
	// Discovery is not selection or a replacement for business authorization.
	if status, out, _ := h.do("GET", "/api/v1/whoami", sessionOwnerA, "", ""); status != 400 {
		t.Errorf("whoami tenant contract changed=%d %v", status, out)
	}
	if status, out, _ := h.do("GET", "/api/v1/contacts", sessionOwnerA, hidden, ""); status != 403 {
		t.Errorf("discovery granted another tenant=%d %v", status, out)
	}
}

func TestSessionMembershipsAgentsNeedExistingTenantGrant(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, _ := h.seed()
	h.provision("member", tenantB, principalAgentA, "agent", "Agent B")
	const path = "/api/v1/session/memberships"
	read := func(want []string) {
		t.Helper()
		status, out, _ := h.do("GET", path, sessionAgentA, "", "")
		if status != 200 {
			t.Fatalf("agent discovery=%d %v", status, out)
		}
		items, ok := out["items"].([]any)
		if !ok || len(items) != len(want) {
			t.Fatalf("agent scopes=%v want=%v", out, want)
		}
		for i, id := range want {
			if items[i].(map[string]any)["tenant_id"] != id {
				t.Errorf("agent scope=%v want=%v", out, want)
			}
		}
	}
	read(nil)
	grant, err := h.api.St.CreateAgentGrant(tenantA, principalAgentA, "test-authority")
	if err != nil {
		t.Fatal(err)
	}
	read([]string{tenantA})
	if err := h.api.St.DeleteAgentGrant(grant.ID, tenantA); err != nil {
		t.Fatal(err)
	}
	read(nil)
}

func TestSessionMembershipsVerificationFailuresAreNotEmptyScopes(t *testing.T) {
	t.Run("identity unavailable", func(t *testing.T) {
		h := newHarness(t)
		h.api.ID.BaseURL = "http://127.0.0.1:1"
		status, out, _ := h.do("GET", "/api/v1/session/memberships", sessionOwnerA, "", "")
		if status != 503 || out["error"] != "identity_unavailable" || out["items"] != nil {
			t.Fatalf("identity failure became empty=%d %v", status, out)
		}
	})
	t.Run("membership store unavailable", func(t *testing.T) {
		h := newHarness(t)
		if err := h.api.St.DB.Close(); err != nil {
			t.Fatal(err)
		}
		status, out, _ := h.do("GET", "/api/v1/session/memberships", sessionOwnerA, "", "")
		if status != 503 || out["error"] != "memberships_unavailable" || out["items"] != nil {
			t.Fatalf("store failure became empty=%d %v", status, out)
		}
	})
	t.Run("agent grant authority unavailable", func(t *testing.T) {
		h := newHarness(t)
		h.seed()
		if _, err := h.api.St.DB.Exec(`DROP TABLE agent_grants`); err != nil {
			t.Fatal(err)
		}
		status, out, _ := h.do("GET", "/api/v1/session/memberships", sessionAgentA, "", "")
		if status != 503 || out["error"] != "memberships_unavailable" || out["items"] != nil {
			t.Fatalf("grant failure became empty=%d %v", status, out)
		}
	})
}
