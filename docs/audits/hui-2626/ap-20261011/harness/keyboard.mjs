// Keyboard/focus evidence for the assign block (HUI-2626 walk fix). Test only.
// Contract under test (vendored Select = @base-ui/react/select):
// Tab 到达触发器 → Enter/ArrowDown 打开 → Esc 关闭且焦点回触发器 →
// 方向键+Enter 选择成员 → Tab 到「分配负责人」按钮 → Enter 提交 → role=status 确认。
import { chromium } from "playwright-core";
import fs from "node:fs";
const [, , tenant, leadRef, out] = process.argv;
const OUT = process.env.WALK_OUT;
const log = [];
const step = (n, d = {}) => { log.push({ step: n, ...d }); console.log(JSON.stringify({ step: n, ...d })); };
const focusInfo = (page) => page.evaluate(() => {
  const a = document.activeElement;
  return a ? `${a.tagName}:${a.getAttribute("role") ?? ""}:${(a.getAttribute("aria-labelledby") ? "labelled" : "")}:${(a.textContent || "").slice(0, 24)}` : "none";
});
const browser = await chromium.launch({ channel: "chrome", headless: true });
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
await ctx.addCookies([{ name: "leads_session", value: "tok_owner_walk", domain: "127.0.0.1", path: "/" }]);
const page = await ctx.newPage();
await page.addInitScript((t) => { try { localStorage.setItem("leads_tenant_id", t); } catch {} }, tenant);
try {
  await page.goto(`http://127.0.0.1:29330/leads/${leadRef}`, { waitUntil: "networkidle", timeout: 240000 });
  const block = page.locator('[data-assign-owner="true"]');
  await block.waitFor({ timeout: 60000 });
  const trigger = block.getByRole("combobox");
  await page.keyboard.press("Tab"); // 从 body 开始
  let tabs = 1;
  let reached = false;
  while (tabs <= 40) {
    const handle = await trigger.elementHandle();
    const isFocused = handle && (await page.evaluate((el) => document.activeElement === el, handle));
    if (isFocused) { reached = true; break; }
    await page.keyboard.press("Tab");
    tabs++;
  }
  step("tab_reaches_trigger", { reached, tabs, focus: await focusInfo(page) });
  await page.keyboard.press("Enter");
  const popup = page.getByRole("listbox");
  await popup.waitFor({ timeout: 10000 });
  step("enter_opens_popup", { popupVisible: await popup.isVisible() });
  await page.screenshot({ path: `${out}-k01-popup-open.png`, fullPage: true });
  await page.keyboard.press("Escape");
  await page.waitForTimeout(400);
  step("esc_closes_and_focus_returns", { popupGone: (await popup.count()) === 0 || !(await popup.isVisible().catch(() => false)), focus: await focusInfo(page) });
  await page.keyboard.press("Enter");
  await popup.waitFor({ timeout: 10000 });
  await page.keyboard.press("ArrowDown"); // 第一个选项（选择成员是 disabled 占位，Base UI 会跳到第一个可用项）
  await page.waitForTimeout(200);
  await page.keyboard.press("Enter"); // 选中
  await page.waitForTimeout(400);
  const triggerText = (await trigger.innerText()).trim();
  step("keyboard_select", { triggerText });
  await page.screenshot({ path: `${out}-k02-keyboard-selected.png`, fullPage: true });
  const assignBtn = block.getByRole("button", { name: "分配负责人" });
  let btnTabs = 0;
  let btnReached = false;
  while (btnTabs <= 10) {
    const handle = await assignBtn.elementHandle();
    const isFocused = handle && (await page.evaluate((el) => document.activeElement === el, handle));
    if (isFocused) { btnReached = true; break; }
    await page.keyboard.press("Tab");
    btnTabs++;
  }
  step("tab_reaches_assign_button", { btnReached, btnTabs, focus: await focusInfo(page) });
  await page.keyboard.press("Enter");
  await page.getByText("已分配负责人").first().waitFor({ timeout: 60000 });
  const statusEl = page.locator('[data-assign-result="true"]');
  step("keyboard_submit_assigned", { statusText: (await statusEl.innerText()).trim(), statusRole: await statusEl.getAttribute("role") });
  await page.screenshot({ path: `${out}-k03-keyboard-assigned.png`, fullPage: true });
} catch (e) {
  step("ERROR", { message: String(e.message || e).slice(0, 300) });
  await page.screenshot({ path: `${out}-k99-error.png`, fullPage: true });
}
fs.writeFileSync(out.replace(/\.png$/, "") + "-keyboard.json", JSON.stringify(log, null, 2));
await browser.close();
