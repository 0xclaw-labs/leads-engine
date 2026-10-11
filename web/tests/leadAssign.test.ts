import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { assignBody, assignableMembers, needsOwner } from "@/lib/leadAssign";

// 2026-10-11 AG06 集成人（HUI-2598/2626 走查缺陷）：Touch 来源线索落在「待分配」时，
// 详情页没有任何分配动作，今日页的「记跟进」因此永远禁用。服务端 PATCH 已支持
// assigned_member_id（仅 owner）；这里只锁定前端纯逻辑，UI 在详情页接线。

describe("needsOwner", () => {
  it("is true for the unassigned label and for an empty owner", () => {
    expect(needsOwner("待分配")).toBe(true);
    expect(needsOwner("")).toBe(true);
    expect(needsOwner(undefined)).toBe(true);
  });

  it("is false once a named owner is shown", () => {
    expect(needsOwner("店主甲")).toBe(false);
  });
});

describe("assignableMembers", () => {
  it("keeps enabled members with an id and labels them by display name and role", () => {
    const out = assignableMembers([
      { id: "mem_a", display_name: "店主甲", role: "owner", enabled: true },
      { id: "mem_b", display_name: "销售甲", role: "sales", enabled: true },
      { id: "mem_c", display_name: "停用", role: "sales", enabled: false },
      { display_name: "无标识", role: "sales", enabled: true },
    ]);
    expect(out).toEqual([
      { id: "mem_a", label: "店主甲（店主）" },
      { id: "mem_b", label: "销售甲（销售）" },
    ]);
  });

  it("falls back to the id when no display name is recorded", () => {
    const out = assignableMembers([{ id: "mem_x", role: "agent", enabled: true }]);
    expect(out).toEqual([{ id: "mem_x", label: "mem_x（代理）" }]);
  });
});

describe("assignBody", () => {
  it("builds the PATCH body the server reads as assigned_member_id", () => {
    expect(assignBody("mem_b")).toEqual({ assigned_member_id: "mem_b" });
  });
});

describe("lead detail wires the assign block without a bare control", () => {
  it("imports the helper and keeps the block off the page-primary count", () => {
    const src = readFileSync("src/app/(shell)/leads/[id]/page.tsx", "utf8");
    expect(src).toContain('from "@/lib/leadAssign"');
    expect(src).toContain("/api/admin/members");
    expect(src).toContain("patchJson(");
    // 分配成功后区块会因负责人已定而卸载，确认信息必须在区块外以 role=status 呈现
    expect(src).toContain('role="status"');
    expect(src).not.toContain("JSON.stringify");
    expect(src).not.toContain("<button");
    // 页面仍只有一个主行动（跟进提交），分配按钮不得抢 data-page-primary
    expect(src.split('data-page-primary="true"').length - 1).toBe(1);
  });
});
