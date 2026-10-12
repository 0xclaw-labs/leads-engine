// 补充证据：视口滚动到时间线卡片（不改动 walk 主路径脚本）。
import { chromium } from "playwright-core";
const [, , tenant, leadRef, out, w, h] = process.argv;
const browser = await chromium.launch({ channel: "chrome", headless: true });
const ctx = await browser.newContext({ viewport: { width: Number(w), height: Number(h) } });
await ctx.addCookies([{ name: "leads_session", value: "tok_owner_walk", domain: "127.0.0.1", path: "/" }]);
const page = await ctx.newPage();
await page.addInitScript((t) => { try { localStorage.setItem("leads_tenant_id", t); } catch {} }, tenant);
await page.goto(`http://127.0.0.1:29330/leads/${leadRef}`, { waitUntil: "networkidle", timeout: 240000 });
await page.getByText("时间线", { exact: true }).first().waitFor({ timeout: 60000 });
await page.getByText("时间线", { exact: true }).first().scrollIntoViewIfNeeded();
await page.waitForTimeout(600);
await page.screenshot({ path: out });
await browser.close();
