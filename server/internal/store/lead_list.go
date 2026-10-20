package store

// HUI-2598 D1/D6：线索列表的可读投影。
//
// 页面原先只拿 leads 行，再单独拉 /api/contacts 拼姓名。联系人请求失败时
// 它把所有姓名静默变成「未命名」，负责人则直接显示 mem_* 工程 id。列表是
// 正常路径的第一屏，不能靠两次请求的偶然成功才正确。
//
// 这里一次查询返回：联系人姓名/来源、负责人显示名与可用性、活动归因。
// 联系方式（phone/email）不进列表投影；它们仍只在获权的 timeline 视图出现。

import "strings"

// LeadSourceLine is the list-safe provenance projection consumed by
// sourceText(). It never carries a phone, email or contact body.
type LeadSourceLine struct {
	Form      string `json:"form,omitempty"`
	Activity  string `json:"activity,omitempty"`
	Channel   string `json:"channel,omitempty"`
	At        string `json:"at,omitempty"`
	SourceApp string `json:"source_app,omitempty"`
}

// LeadListRow is one lead plus the display facts the list needs.
// AssignedMemberID stays for permissions/actions; OwnerLabel is what a person
// reads, and is never the raw id.
type LeadListRow struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenant_id"`
	ContactID        string `json:"contact_id"`
	SourceRefID      string `json:"source_ref_id,omitempty"`
	Status           string `json:"status"`
	FilterReason     string `json:"filter_reason,omitempty"`
	AssignedMemberID string `json:"assigned_member_id,omitempty"`
	CreatedBy        string `json:"created_by"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`

	ContactName      string         `json:"contact_name"`
	SourceType       string         `json:"source_type"`
	OwnerLabel       string         `json:"owner_label"`
	OwnerEnabled     bool           `json:"owner_enabled"`
	AssignmentReason string         `json:"assignment_reason"`
	Source           LeadSourceLine `json:"source"`
}

// ListLeadRows is the tenant-scoped lead list projection.
// assigneeFilter keeps the existing sales scope (only rows assigned to that
// member); owner passes "".
func (s *Store) ListLeadRows(tenantID, assigneeFilter string) ([]LeadListRow, error) {
	q := `
		SELECT l.id, l.tenant_id, l.contact_id, COALESCE(l.source_ref_id,''), l.status, l.filter_reason,
		       COALESCE(l.assigned_member_id,''), l.created_by, l.created_at, l.updated_at,
		       c.name, c.source_type,
		       COALESCE(m.display_name,''), COALESCE(m.enabled,0),
		       COALESCE(sr.source_app,''), COALESCE(sr.auth_scope_snapshot,''), COALESCE(sr.created_at,'')
		FROM leads l
		JOIN contacts c ON c.id = l.contact_id AND c.tenant_id = l.tenant_id AND c.deleted_at IS NULL
		LEFT JOIN members m ON m.id = l.assigned_member_id AND m.tenant_id = l.tenant_id
		LEFT JOIN source_refs sr ON sr.id = l.source_ref_id AND sr.tenant_id = l.tenant_id
		WHERE l.tenant_id=?`
	args := []any{tenantID}
	if assigneeFilter != "" {
		q += ` AND l.assigned_member_id=?`
		args = append(args, assigneeFilter)
	}
	q += ` ORDER BY l.created_at DESC, l.id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LeadListRow
	for rows.Next() {
		var row LeadListRow
		var ownerEnabled int
		var app, snapshot, refAt string
		if err := rows.Scan(&row.ID, &row.TenantID, &row.ContactID, &row.SourceRefID, &row.Status, &row.FilterReason,
			&row.AssignedMemberID, &row.CreatedBy, &row.CreatedAt, &row.UpdatedAt,
			&row.ContactName, &row.SourceType, &row.OwnerLabel, &ownerEnabled,
			&app, &snapshot, &refAt); err != nil {
			return nil, err
		}
		row.OwnerEnabled = ownerEnabled == 1
		row.OwnerLabel = strings.TrimSpace(row.OwnerLabel)
		row.AssignmentReason = assignmentReason(row.OwnerLabel, row.AssignedMemberID, row.OwnerEnabled)
		row.Source = LeadSourceLine{
			Activity:  campaignRefFromSnapshot(snapshot),
			Channel:   sourceChannelLabel(row.SourceType),
			At:        refAt,
			SourceApp: strings.TrimSpace(app),
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// assignmentReason is display text, never an id. A raw member id is not a name
// a merchant can act on; a disabled owner is called out instead of silently
// looking assigned.
func assignmentReason(label, memberID string, enabled bool) string {
	if memberID == "" {
		return "待分配"
	}
	if label == "" {
		return "已分配给未命名成员"
	}
	if !enabled {
		return "负责人已停用"
	}
	return "已分配给 " + label
}
