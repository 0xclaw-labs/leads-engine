// Touch-target evidence for the assign block and follow-up form at 390 (HUI-2626). Test only.
// 测量真实 boundingBox（含 padding 的可点区域），对 spec 的 ≥44px 与 NOT_RUN 提到的 48px 分别给出事实。
import { chromium } from "playwright-core";
import fs from "node:fs";
const [, , tenant, leadRef, out] = process.argv;
const browser = await chromium.launch({ channel: "chrome", headless: true });
const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });
await ctx.addCookies([{ name: "leads_session", value: "tok_owner_walk", domain: "127.0.0.1", path: "/" }]);
const page = await ctx.newPage();
await page.addInitScript((t) => { try { localStorage.setItem("leads_tenant_id", t); } catch {} }, tenant);
const log = [];
const step = (n, d = {}) => { log.push({ step: n, ...d }); console.log(JSON.stringify({ step: n, ...d })); };
try {
  await page.goto(`http://127.0.0.1:29330/leads/${leadRef}`, { waitUntil: "networkidle", timeout: 240000 });
  const block = page.locator('[data-assign-owner="true"]');
  await block.waitFor({ timeout: 60000 });
  const targets = [
    ["assign_trigger", block.getByRole("combobox")],
    ["assign_button", block.getByRole("button", { name: "分配负责人" })],
    ["record_textarea", page.getByLabel("记录")],
    ["next_input", page.getByLabel("下一次")],
    ["followup_button", page.getByRole("button", { name: "记跟进", exact: true }).first()],
  ];
  const measured = [];
  for (const [name, loc] of targets) {
    const box = await loc.boundingBox();
    measured.push({ name, width: box ? Math.round(box.width) : null, height: box ? Math.round(box.height) : null });
  }
  const ge44 = measured.filter((m) => m.height !== null && m.height >= 44).length;
  const ge48 = measured.filter((m) => m.height !== null && m.height >= 48).length;
  step("touch_targets_390", { measured, ge44, ge48, total: measured.length });
  await page.screenshot({ path: out, fullPage: true });
} catch (e) {
  step("ERROR", { message: String(e.message || e).slice(0, 300) });
  await page.screenshot({ path: out.replace(/\.png$/, "-99.png"), fullPage: true });
}
fs.writeFileSync(out.replace(/\.png$/, ".json"), JSON.stringify(log, null, 2));
await browser.close();
