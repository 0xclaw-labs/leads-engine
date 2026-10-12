// 补充证据：视口滚动到空态分组（内联 SVG 图标验证）。
import { chromium } from "playwright-core";
const [, , tenant, out] = process.argv;
const browser = await chromium.launch({ channel: "chrome", headless: true });
const ctx = await browser.newContext({ viewport: { width: 390, height: 844 } });
await ctx.addCookies([{ name: "leads_session", value: "tok_owner_walk", domain: "127.0.0.1", path: "/" }]);
const page = await ctx.newPage();
await page.addInitScript((t) => { try { localStorage.setItem("leads_tenant_id", t); } catch {} }, tenant);
await page.goto("http://127.0.0.1:29330/", { waitUntil: "networkidle", timeout: 240000 });
const mark = page.locator('[data-state-mark="empty"]').first();
await mark.waitFor({ timeout: 60000 });
await mark.scrollIntoViewIfNeeded();
await page.waitForTimeout(500);
const hasSvg = await mark.evaluate((el) => el.querySelector("svg") !== null);
console.log(JSON.stringify({ emptyMarkHasInlineSvg: hasSvg }));
await page.screenshot({ path: out });
await browser.close();
