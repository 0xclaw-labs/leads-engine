// HUI-2626 盲评修复复录（2026-10-12）。基于 ap-20261011/harness/walk.mjs，
// 两处差异：
// 1) 截图改视口模式（Root 裁定：fullPage 是采集伪影，非产品缺陷；重录用视口截图）。
// 2) tally 记录全部 console 错误与 404 计数（P2① 验收：产品导致的 404 噪音清零）。
// 路径仍是主路径：今日 → 详情 → 选成员 → 分配负责人 → 记跟进+下一次 → 今日 → 刷新 → 重登详情。
import { chromium } from "playwright-core";
import fs from "node:fs";
const [, , tenant, width, height, leadRef, tag, note, visitor] = process.argv;
const OUT = process.env.WALK_OUT;
const log = [];
const step = (name, data = {}) => { const row = { step: name, ...data }; log.push(row); console.log(JSON.stringify(row)); };
const browser = await chromium.launch({ channel: "chrome", headless: true });
const ctx = await browser.newContext({ viewport: { width: Number(width), height: Number(height) } });
const setSession = async () => ctx.addCookies([{ name: "leads_session", value: "tok_owner_walk", domain: "127.0.0.1", path: "/" }]);
await setSession();
const page = await ctx.newPage();
const consoleErrors = [];
page.on("console", (m) => { if (m.type() === "error") consoleErrors.push(m.text().slice(0, 200)); });
await page.addInitScript((t) => { try { localStorage.setItem("leads_tenant_id", t); } catch {} }, tenant);
let clicks = 0, fills = 0, selects = 0;
// 视口截图：只拍用户实际看到的视口（390/1440 各自的真实首屏）。
const shot = (n) => page.screenshot({ path: `${OUT}/${tag}-${n}.png` });
try {
  await page.goto("http://127.0.0.1:29330/", { waitUntil: "networkidle", timeout: 240000 });
  step("today_loaded", { hasInfoGroup: (await page.innerText("body")).includes("信息缺失或权限异常"), hasVisitor: (await page.innerText("body")).includes(visitor) });
  await shot("01-today-before");
  await page.locator(`a[href="/leads/${leadRef}"]`).first().click(); clicks++;
  await page.waitForURL(`**/leads/${leadRef}`, { timeout: 60000 });
  await page.getByText("分配负责人", { exact: false }).first().waitFor({ timeout: 60000 });
  step("detail_opened_assign_block_visible", { url: page.url() });
  await shot("02-detail-unassigned");
  const block = page.locator('[data-assign-owner="true"]');
  await block.getByRole("combobox").or(block.locator("button")).first().click(); clicks++;
  // 租户 B 的 owner 显示名不同（店主乙）；MEMBER_MATCH 让同一脚本覆盖两个租户。
  await page.getByRole("option", { name: new RegExp(process.env.MEMBER_MATCH || "店主甲") }).first().click(); selects++;
  await shot("03-detail-member-picked");
  await block.getByRole("button", { name: "分配负责人" }).click(); clicks++;
  await page.getByText("已分配负责人").first().waitFor({ timeout: 60000 });
  step("assigned", { message: "已分配负责人" });
  await shot("04-detail-assigned");
  await page.getByLabel("记录").fill(note); fills++;
  await page.getByLabel("下一次").fill("2026-10-13T10:00"); fills++;
  await page.getByRole("button", { name: "记跟进", exact: true }).first().click(); clicks++;
  await page.waitForTimeout(1500);
  step("followup_submitted", { note_len: note.length });
  await shot("05-detail-after-followup");
  await page.getByRole("link", { name: "今天" }).first().click(); clicks++;
  await page.waitForURL("http://127.0.0.1:29330/", { timeout: 60000 });
  await page.waitForLoadState("networkidle");
  const todayText = await page.innerText("body");
  step("today_after_followup", { visitorStillInInfo: todayText.includes("信息缺失或权限异常") && todayText.includes(visitor) });
  await shot("06-today-after");
  await page.reload({ waitUntil: "networkidle" }); clicks++;
  step("reload_ok", { hasVisitor: (await page.innerText("body")).includes(visitor) });
  await ctx.clearCookies(); await setSession();
  await page.goto("http://127.0.0.1:29330/leads/" + leadRef, { waitUntil: "networkidle" }); clicks++;
  const relogText = await page.innerText("body");
  step("relogin_detail", { stillUnassigned: relogText.includes("待分配"), assignBlockPresent: (await page.locator('[data-assign-owner="true"]').count()) > 0 });
  await shot("07-relogin-detail");
} catch (e) {
  step("ERROR", { message: String(e.message || e).slice(0, 300) });
  await shot("99-error");
}
const err404 = consoleErrors.filter((t) => t.includes("404"));
step("tally", { viewport: `${width}x${height}`, clicks, fills, selects, consoleErrorCount: consoleErrors.length, console404Count: err404.length, consoleErrors });
// P2① 验收断言：产品导致的 console 404 必须为 0（能力发现 200+enabled、
// favicon 自带、轻文案只在活动出处存在时挂载）。
step("assert_no_console_404", { pass: err404.length === 0 });
fs.writeFileSync(`${OUT}/${tag}-steps.json`, JSON.stringify(log, null, 2));
await browser.close();
