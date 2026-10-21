import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// ⑩ 删除客户档案不再用浏览器原生 window.confirm，改应用内 vendored
// ConfirmDialog（danger）。确认语义与文案保持原意。

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const detailSrc = () => readFileSync(join(ROOT, "src/app/(shell)/contacts/[id]/page.tsx"), "utf8");

describe("客户档案删除确认", () => {
  it("全站应用代码不再有 window.confirm", () => {
    expect(detailSrc()).not.toContain("window.confirm");
  });

  it("删除按钮只负责打开应用内确认弹窗", () => {
    const src = detailSrc();
    expect(src).toContain('onClick={() => setConfirmDeleteOpen(true)}');
    expect(src).not.toMatch(/<Button[^>]*onClick=\{removeProfile\}/);
    expect(src).toContain("const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);");
  });

  it("ConfirmDialog 为 danger，语义与文案保持原意", () => {
    const src = detailSrc();
    expect(src).toContain("ConfirmDialog");
    expect(src).toContain('title="确认删除该客户档案"');
    expect(src).toContain('consequence="删除后所有角色不可再访问,授权与跟进记录将依法保留最小审计。"');
    expect(src).toContain('confirmLabel="删除"');
    expect(src).toContain('cancelLabel="取消"');
    // danger 属性存在（vendored ConfirmDialogProps）。
    expect(src).toMatch(/danger\b/);
  });

  it("确认后关闭弹窗再执行删除，删除逻辑未被改写", () => {
    const src = detailSrc();
    expect(src).toMatch(/onConfirm=\{async \(\) => \{\s*setConfirmDeleteOpen\(false\);\s*await removeProfile\(\);/);
    expect(src).toContain('scopeInit(scope.tenant, { method: "DELETE" })');
    expect(src).toContain("已删除(软删除:档案脱敏,授权/跟进记录保留审计)");
  });
});
