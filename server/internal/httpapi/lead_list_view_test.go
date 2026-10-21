package httpapi

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bianjiefilm/leads-engine/server/internal/store"
)

// HUI-2598 D1/D6：线索列表必须是“可直接读”的投影。
// 负责人显示成员名而不是 mem_* 工程 id；联系人姓名/来源随同一次响应返回，
// 页面不再二次拉 /contacts（那条路径失败时会把所有姓名静默变成“未命名”）。
// 手机号等联系方式不得进入列表 JSON（咨询详情仍走 timeline 的受权视图）。
func TestLeadListProjectsContactOwnerAndSource(t *testing.T) {
	h := newHarness(t)
	tenantA, tenantB, contactA1 := h.seed()
	sales1ID := h.memberID(tenantA, principalSalesA1)

	created := h.mustDo("POST", "/api/v1/leads", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"contact_id":%q,"source":{"source_app":"touch-engine","source_ref":"touch-ref-list-1","auth_scope_snapshot":"{\"campaign_ref\":\"camp-list-1\"}"}}`, contactA1), 201)
	assignedID, _ := created["id"].(string)
	if assignedID == "" {
		t.Fatalf("created lead missing id: %v", created)
	}
	h.mustDo("PATCH", "/api/v1/leads/"+assignedID, sessionOwnerA, tenantA,
		fmt.Sprintf(`{"assigned_member_id":%q}`, sales1ID), 200)

	// 未分配线索：走 store 落一行（HTTP 创建路径默认自派给创建者）。
	unassigned, err := h.api.St.CreateLead(store.Lead{
		TenantID: tenantA, ContactID: contactA1, Status: "new",
	}, "test-seed", "")
	if err != nil {
		t.Fatalf("seed unassigned lead: %v", err)
	}

	out := h.mustDo("GET", "/api/v1/leads", sessionOwnerA, tenantA, "", 200)
	items, _ := out["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("owner list = %d items, want 2: %v", len(items), out)
	}
	byID := map[string]map[string]any{}
	for _, raw := range items {
		row, _ := raw.(map[string]any)
		id, _ := row["id"].(string)
		byID[id] = row
	}
	got := byID[assignedID]
	if got == nil {
		t.Fatalf("assigned lead missing from list: %v", out)
	}
	for key, want := range map[string]string{
		"contact_name":      "甲商家",
		"source_type":       "manual",
		"owner_label":       "Sales A1",
		"assignment_reason": "已分配给 Sales A1",
	} {
		if got[key] != want {
			t.Fatalf("%s = %v, want %q (row %v)", key, got[key], want, got)
		}
	}
	source, _ := got["source"].(map[string]any)
	if source["activity"] != "camp-list-1" || source["channel"] != "手工录入" {
		t.Fatalf("source projection = %v", source)
	}
	raw := mustJSON(out)
	if strings.Contains(raw, "13812345678") || strings.Contains(raw, "jia@shop.cn") {
		t.Fatalf("lead list leaked contact details: %s", raw)
	}
	if strings.Contains(raw, sales1ID) && got["owner_label"] == sales1ID {
		t.Fatalf("owner label still an engineering id: %v", got)
	}

	pending := byID[unassigned.ID]
	if pending == nil {
		t.Fatalf("unassigned lead missing: %v", out)
	}
	if pending["owner_label"] != "" || pending["assignment_reason"] != "待分配" {
		t.Fatalf("unassigned projection = %v", pending)
	}

	// 销售只看自己名下；列表不是 404 掩码，但也不能多出别人的行。
	salesOut := h.mustDo("GET", "/api/v1/leads", sessionSalesA1, tenantA, "", 200)
	salesItems, _ := salesOut["items"].([]any)
	if len(salesItems) != 1 {
		t.Fatalf("sales list = %d, want only own lead: %v", len(salesItems), salesOut)
	}
	if row, _ := salesItems[0].(map[string]any); row["id"] != assignedID {
		t.Fatalf("sales saw another lead: %v", row)
	}
	otherSales := h.mustDo("GET", "/api/v1/leads", sessionSalesA2, tenantA, "", 200)
	if items, _ := otherSales["items"].([]any); len(items) != 0 {
		t.Fatalf("unassigned sales list = %v, want empty", otherSales)
	}
	foreign := h.mustDo("GET", "/api/v1/leads", sessionOwnerB, tenantB, "", 200)
	if items, _ := foreign["items"].([]any); len(items) != 0 {
		t.Fatalf("cross-tenant list = %v, want empty", foreign)
	}
}

// HUI-2598 D5：把线索派给已停用成员会让记录从所有销售视图消失。
// 分配校验必须拒绝停用成员，并且不改变现有负责人。
func TestAssignRejectsDisabledMember(t *testing.T) {
	h := newHarness(t)
	tenantA, _, contactA1 := h.seed()
	sales1ID := h.memberID(tenantA, principalSalesA1)
	disabledID := h.memberID(tenantA, principalDisabled)

	created := h.mustDo("POST", "/api/v1/leads", sessionOwnerA, tenantA,
		fmt.Sprintf(`{"contact_id":%q}`, contactA1), 201)
	leadID := created["id"].(string)
	h.mustDo("PATCH", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA,
		fmt.Sprintf(`{"assigned_member_id":%q}`, sales1ID), 200)

	status, body, _ := h.do("PATCH", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA,
		fmt.Sprintf(`{"assigned_member_id":%q}`, disabledID))
	if status != 400 || body["error"] != "bad_assignee" {
		t.Fatalf("disabled assignee = %d %v, want 400 bad_assignee", status, body)
	}
	after := h.mustDo("GET", "/api/v1/leads/"+leadID, sessionOwnerA, tenantA, "", 200)
	if after["assigned_member_id"] != sales1ID {
		t.Fatalf("owner changed despite rejection: %v", after)
	}
}
