package store

// PrincipalMembership is an internal discovery candidate, not a grant. The
// HTTP layer applies the original authz policy and current per-tenant agent
// grant before exposing the tenant name to this authenticated principal.
type PrincipalMembership struct {
	Member     Member
	TenantName string
}

func (s *Store) ListPrincipalMemberships(principal string) ([]PrincipalMembership, error) {
	rows, err := s.DB.Query(`SELECT m.id,m.tenant_id,m.principal_ref,m.role,m.enabled,t.name
  FROM members m JOIN tenants t ON t.id=m.tenant_id
  WHERE m.principal_ref=? ORDER BY t.name,t.id`, principal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PrincipalMembership{}
	for rows.Next() {
		var row PrincipalMembership
		var enabled int
		if err := rows.Scan(&row.Member.ID, &row.Member.TenantID, &row.Member.PrincipalRef, &row.Member.Role, &enabled, &row.TenantName); err != nil {
			return nil, err
		}
		row.Member.Enabled = enabled == 1
		out = append(out, row)
	}
	return out, rows.Err()
}
