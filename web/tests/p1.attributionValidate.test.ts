import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { productError } from "@/lib/productShell";
import { citationJsonError } from "@/lib/roi";

// ⑨ attribution 引用编辑改进（最小诚实版）：服务端 /api/roi/recalculate 的请求体
// 是 links/events/costs/revenues/refunds/allocations/experiments 七类引用数组
// 组成的复合文档（handlers_roi.go），不适合拍平成结构化字段 → 保留 JSON 输入，
// 提交前校验，非法 JSON 给出具体可读位置并阻止提交，产品语气文案。

describe("citationJsonError 引用 JSON 校验", () => {
  it("合法 JSON 与空输入都放行", () => {
    expect(citationJsonError("")).toBeNull();
    expect(citationJsonError("   ")).toBeNull();
    expect(citationJsonError("{}")).toBeNull();
    expect(citationJsonError('{"links": [{"id": "l1"}]}')).toBeNull();
  });

  it("非法 JSON 给出具体行列位置", () => {
    // V8（node 25）对「缺属性名」给出 position + line/column。
    expect(citationJsonError('{"a": 1,}')).toBe(
      "引用不是有效的 JSON：第 1 行第 9 列附近有格式错误，请检查标点与括号后再复算。",
    );
    expect(citationJsonError('{\n  "a": 1,\n}')).toMatch(
      /^引用不是有效的 JSON：第 \d+ 行第 \d+ 列附近有格式错误，请检查标点与括号后再复算。$/,
    );
  });

  it("放错位置的符号与截断分别给出诚实提示（不编造位置）", () => {
    // V8 对意外 token 不给位置：不给行列，只说符号放错。
    expect(citationJsonError('{"links": [1,}')).toBe(
      "引用不是有效的 JSON：有一个放错位置的符号，请检查标点与括号后再复算。",
    );
    expect(citationJsonError('{"links": ')).toBe(
      "引用不是有效的 JSON，内容可能没有写完，请检查是否缺少结尾的括号后再复算。",
    );
  });

  it("截断的 JSON 提示内容没写完", () => {
    const message = citationJsonError('{"links": ');
    expect(message).toBe("引用不是有效的 JSON，内容可能没有写完，请检查是否缺少结尾的括号后再复算。");
  });

  it("错误文案是产品语气，可原样通过 productError 显示", () => {
    for (const raw of ['{"a": 1,}', "{", "[1, 2"]) {
      const message = citationJsonError(raw) ?? "";
      expect(message.length).toBeGreaterThan(0);
      expect(productError(message)).toBe(message);
    }
  });
});

describe("attribution 页接线", () => {
  const src = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src/app/(shell)/attribution/page.tsx"), "utf8");

  it("提交前先校验并阻止非法 JSON", () => {
    expect(src).toContain("const invalid = citationJsonError(raw)");
    expect(src).toContain("if (invalid) {");
    // 阻止时清掉旧报告，不保留过时结果。
    const block = src.slice(src.indexOf("const invalid = citationJsonError(raw)"), src.indexOf("setErr(\"\","));
    expect(block).toContain("setReport(null)");
  });

  it("保留 JSON 输入形态（引用是复合文档，不拍平成字段）", () => {
    expect(src).toContain("<textarea");
    expect(src).toContain("来源、事件与费用引用");
  });
});
