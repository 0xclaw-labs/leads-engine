import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { recallToday, rememberToday, restoredDraftCount, type TodayDraft, type TodayMemory } from "@/lib/workbench";

// ⑪ today 页草稿恢复提示：recallToday 实际恢复到带内容的草稿时展示一次
// 「已恢复上次未提交的草稿（N 条）。」，可关闭，关闭后本会话不再弹（useState）。

const draft = (patch: Partial<TodayDraft> = {}): TodayDraft => ({
  note: "",
  nextAt: "",
  disposition: "next",
  changed: [],
  ...patch,
});

describe("restoredDraftCount", () => {
  it("只数带内容的草稿，空壳（只点过输入框）不算", () => {
    expect(restoredDraftCount({})).toBe(0);
    expect(restoredDraftCount({ a: draft() })).toBe(0);
    expect(restoredDraftCount({ a: draft({ note: "  " }) })).toBe(0);
    expect(restoredDraftCount({ a: draft({ note: "回访了张老板" }) })).toBe(1);
    expect(restoredDraftCount({ a: draft({ nextAt: "2026-10-22T10:00" }) })).toBe(1);
    expect(
      restoredDraftCount({ a: draft({ note: "x" }), b: draft(), c: draft({ note: "y" }) }),
    ).toBe(2);
  });

  it("对存储里的残缺草稿保持防御（缺字段按空处理）", () => {
    expect(restoredDraftCount({ a: { note: "有内容" } as TodayDraft })).toBe(1);
    expect(restoredDraftCount({ a: {} as TodayDraft })).toBe(0);
  });
});

describe("recallToday 与计数配合", () => {
  it("按租户恢复；无租户/无记忆时为空", () => {
    const memory: TodayMemory = rememberToday({}, "tenant-a", "desk_1", { a: draft({ note: "x" }) });
    expect(restoredDraftCount(recallToday(memory, "tenant-a").drafts)).toBe(1);
    expect(restoredDraftCount(recallToday(memory, "tenant-b").drafts)).toBe(0);
    expect(restoredDraftCount(recallToday(memory, null).drafts)).toBe(0);
    expect(restoredDraftCount(recallToday(null, "tenant-a").drafts)).toBe(0);
  });
});

describe("today 页恢复提示接线", () => {
  const src = readFileSync("src/app/(shell)/page.tsx", "utf8");

  it("恢复时展示可关闭提示，关闭后本会话不再弹（useState，不入 storage）", () => {
    expect(src).toContain("setRestoredCount(restoredDraftCount(restored.drafts))");
    expect(src).toContain("已恢复上次未提交的草稿（{restoredCount} 条）。");
    expect(src).toContain('data-today-restore-note="true"');
    expect(src).toContain("setRestoreNoteClosed(true)");
    // 本会话性：提示状态只活在组件 state，不写 localStorage。
    expect(src).toMatch(/const \[restoreNoteClosed, setRestoreNoteClosed\] = useState\(false\);/);
    expect(src).not.toContain("restoreNoteClosed\", JSON");
  });

  it("计数在恢复 effect 里设置，后续编辑不改写提示", () => {
    // restoredCount 只出现在恢复 effect 与渲染条件里，不在 updateDraft 里被改。
    const update = src.slice(src.indexOf("function updateDraft"), src.indexOf("async function submitFollow"));
    expect(update).not.toContain("setRestoredCount");
  });
});
