// R5-D：Leads 日期/时间复合件纯函数口径 + 迁移点源码约束。
// node SSR 环境（无 jsdom）；不打生产。
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

import {
  combineLocalDateTime,
  formatDateLabel,
  formatDateTimeLabel,
  isValidLocalDateTime,
  parseDatePart,
  splitLocalDateTime,
} from "@/lib/date-time-input";

const webRoot = path.resolve(__dirname, "..");
const read = (p: string) => readFileSync(path.join(webRoot, p), "utf8");

describe("date-time-input 纯函数口径", () => {
  it("本地墙钟往返：datetime 拆分/合并一致，不做 UTC 偏移", () => {
    const value = "2026-11-24T18:30";
    const parts = splitLocalDateTime(value);
    expect(parts).toEqual({ datePart: "2026-11-24", timePart: "18:30" });
    expect(combineLocalDateTime(parts!.datePart, parts!.timePart)).toBe(value);
    expect(isValidLocalDateTime(value)).toBe(true);
  });

  it("拒绝格式错误、越界日期与进位回绕", () => {
    for (const bad of ["", "2026-13-01T00:00", "2026-02-30T12:00", "2026/11/24 18:30", "2026-11-24T25:00", "2026-11-24T12:61"]) {
      expect(isValidLocalDateTime(bad)).toBe(false);
      expect(splitLocalDateTime(bad)).toBeNull();
    }
    expect(parseDatePart("2026-02-29")).toBeNull();
    expect(parseDatePart("2024-02-29")).not.toBeNull();
  });

  it("触发器文案：合法值回显墙钟串，空值回落占位", () => {
    expect(formatDateTimeLabel("2026-11-24T18:30")).toBe("2026-11-24 18:30");
    expect(formatDateTimeLabel("")).toBe("选择日期时间");
    expect(formatDateLabel("2026-11-24")).toBe("2026-11-24");
  });
});

describe("DateTimeField 迁移点源码约束", () => {
  it("跟进记录表单不再外露原生 datetime-local/date 输入，改走复合件", () => {
    const panel = read("src/app/reception/session-panel.tsx");
    for (const native of ['type="datetime-local"', 'type="date"']) {
      expect(panel).not.toContain(native);
    }
    expect(panel).toContain("DateTimeField");
  });

  it("共享 wrapper 内只有复合件内时间槽 type=time，Popover+Calendar 在位", () => {
    const field = read("src/components/workbench/dateTimeField.tsx");
    expect(field).toContain("Popover");
    expect(field).toContain("Calendar");
    expect(field).toContain('type="time"');
    expect(field).not.toContain('type="datetime-local"');
    expect(field).not.toContain('type="date"');
  });

  it("三个衍生站点仍由共享 wrapper 承载，日期类型站点口径不变", () => {
    const home = read("src/app/(shell)/page.tsx");
    const leadDetail = read("src/app/(shell)/leads/[id]/page.tsx");
    const opp = read("src/app/(shell)/opportunities/[id]/page.tsx");
    expect(home).toContain("DateTimeField");
    expect(leadDetail).toContain("DateTimeField");
    expect(opp).toContain("DateTimeField");
    expect(opp).toContain('type="date"');
  });
});
