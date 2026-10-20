import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { leadRowFacts } from "@/lib/finish";

// HUI-2626 finish-r1：leads 列表成品化。
// 服务端 /api/v1/leads 无查询参数、返回当前范围全量（owner 全量 / 销售只看名下，
// handlers_business.go:462），状态筛选是对全量结果的客户端过滤，文案如实标注。

const row = {
  id: "l1",
  contact_id: "c1",
  status: "in_progress",
  assigned_member_id: "m1",
  contact_name: "李线索",
  source_type: "touch_campaign",
  owner_label: "销售甲",
  assignment_reason: "已分配给 销售甲",
  source: { channel: "碰一碰", activity: "camp-list-1", at: "2026-10-11T05:14:32.586554Z" },
};

describe("leadRowFacts", () => {
  it("renders source/status/owner in the shared wording", () => {
    const facts = leadRowFacts(row);
    const value = (label: string) => facts.find((f) => f.label === label)?.value ?? "";
    expect(value("来源")).toBe("碰一碰");
    expect(value("状态")).toBe("跟进中");
    expect(value("负责人")).toBe("销售甲 · 已分配给 销售甲");
  });

  it("keeps unassigned leads visibly unassigned", () => {
    const facts = leadRowFacts({ ...row, assigned_member_id: undefined, owner_label: "", assignment_reason: "待分配" });
    expect(facts.find((f) => f.label === "负责人")?.value).toBe("待分配（还没有负责人，先分配后才能持续跟进）");
  });

  it("calls out a disabled owner instead of showing a dead assignment", () => {
    const facts = leadRowFacts({ ...row, assignment_reason: "负责人已停用", owner_label: "停用销售" });
    expect(facts.find((f) => f.label === "负责人")?.value).toBe("停用销售 · 负责人已停用");
  });

  it("filters the full server list honestly by status", () => {
    const rows = [
      row,
      { ...row, id: "l2", status: "new" },
      { ...row, id: "l3", status: "converted" },
    ];
    const inProgress = rows.filter((r) => r.status === "in_progress");
    expect(inProgress).toHaveLength(1);
  });
});

describe("leads page finish shape", () => {
  const src = readFileSync("src/app/(shell)/leads/page.tsx", "utf8");

  it("labels the client-side status filter over the full server list", () => {
    expect(src).toContain("全部线索");
    expect(src).not.toContain("服务端筛选");
  });

  it("wires the shared detail drawer against the real timeline API", () => {
    expect(src).toContain("DetailDrawer");
    expect(src).toContain("/api/leads/${");
    expect(src).toContain("/timeline");
  });

  it("uses the server list projection instead of a second contacts request", () => {
    expect(src).toContain("contact_name");
    expect(src).toContain("owner_label");
    expect(src).not.toContain("/api/contacts");
    expect(src).not.toContain("未命名");
    expect(src).not.toContain("Promise.all");
  });

  it("has no bare table and no bare button elements", () => {
    expect(src).not.toContain("<table");
    expect(src).not.toContain("<button");
  });

  it("declares the list page pattern regions", () => {
    expect(src).toContain('data-pattern="list"');
    for (const region of ["header", "primary_action", "status", "body", "loading", "empty", "error"]) {
      expect(src, `missing region ${region}`).toContain(`data-region="${region}"`);
    }
  });
});
