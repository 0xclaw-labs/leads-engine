package httpapi

import (
	"net/http"

	"github.com/bianjiefilm/leads-engine/server/internal/authz"
)

// Discovery needs no selected tenant. Its principal is resolved by the same
// platform identity chain as requireSession, never a header or query input.
// It makes no workspace selection and does not change record permissions.
func (s *Server) handleSessionMemberships(w http.ResponseWriter, r *http.Request) {
	principal := callerFrom(r).Principal
	candidates, err := s.St.ListPrincipalMemberships(principal.ID)
	if err != nil {
		fail(w, 503, "memberships_unavailable", "current workspaces could not be verified")
		return
	}
	items := []map[string]string{}
	for _, candidate := range candidates {
		member := candidate.Member
		c := &caller{Principal: principal, Member: &member}
		if authz.Role(member.Role) == authz.RoleAgent {
			grant, err := s.St.GetAgentGrant(member.TenantID, principal.ID)
			if err != nil {
				fail(w, 503, "memberships_unavailable", "current agent grants could not be verified")
				return
			}
			c.Grant = grant
		}
		decision := authz.Authorize(authzMember(c), authzGrant(c), authz.ActionReadList, authz.RecordScope{TenantID: member.TenantID})
		if !decision.Allowed {
			continue
		}
		items = append(items, map[string]string{"tenant_id": member.TenantID, "display_name": candidate.TenantName})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"items": items})
}
