import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// ⑧ sop 页原生控件替换：原生 select/textarea 换 vendored renderer 控件
// （RendererSelect / Textarea），≤430 触控密度（rendererDensity）继续生效，
// 交互语义与选项文案不变。

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const sop = () => readFileSync(join(ROOT, "src/app/(shell)/sop/page.tsx"), "utf8");

describe("sop 页 renderer 控件替换", () => {
  it("不再有原生 select 与 textarea", () => {
    const src = sop();
    expect(src).not.toMatch(/<select[\s>]/);
    expect(src).not.toMatch(/<textarea[\s>]/);
    expect(src).not.toMatch(/<option[\s>]/);
  });

  it("客户/授权/渠道/用途走 RendererSelect，内容走 vendored Textarea", () => {
    const src = sop();
    expect(src).toContain('RendererSelect');
    expect(src).toContain('label="客户"');
    expect(src).toContain('label="授权"');
    expect(src).toContain('label="渠道"');
    expect(src).toContain('label="用途"');
    expect(src).toContain('<Textarea label="内容"');
    expect(src).toContain('import { RendererSelect } from "@/components/workbench/rendererSelect"');
    expect(src).toContain('Textarea } from "@/vendor/painuo/react/v1/src/index"');
  });

  it("选项语义不变：占位、渠道枚举、用途与营销门控", () => {
    const src = sop();
    for (const text of ["选择客户", "不选择授权", "短信", "邮件", "企微", "跟进", "营销"]) {
      expect(src, `missing choice text ${text}`).toContain(text);
    }
    // 无授权时营销项禁用且保持 onChange 双保险。
    expect(src).toContain("disabled: !marketingOpen");
    expect(src).toContain("if (next === \"marketing\" && !marketingOpen) return;");
  });

  it("授权选择器随 marketingOpen 禁用，客户选择仍触发 loadConsents", () => {
    const src = sop();
    expect(src).toMatch(/label="授权"[\s\S]{0,200}disabled=\{!marketingOpen\}/);
    expect(src).toContain("void loadConsents(tenant, id)");
  });

  it("主行动收敛与错误/空态文案不受控件替换影响", () => {
    const src = sop();
    expect(src.split('data-page-primary="true"').length - 1).toBe(1);
    expect(src).toContain("选择客户并填写内容后，这里可以记下内部提醒");
    expect(src).toContain("还没有可选的客户");
    expect(src).toContain("还没有可选择的授权");
  });
});
