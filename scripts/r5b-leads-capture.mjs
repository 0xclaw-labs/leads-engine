// r5b-leads-capture.mjs —— R5B 接待工作台取证截图（dev-only，仅回环本地栈）。
//
// 前置：先跑 r5b-leads-stack.mjs；本脚本用本机 Chrome headless 打开已挂载
// 跟进复合件的接待会话，采 1280×800 与 390×844 fullPage、console 与溢出度量，
// 并点开一次日期浮层确认复合件可交互（不提交跟进记录）。不改鉴权、不改守卫。
//
// 用法：node scripts/r5b-leads-capture.mjs
// 环境：R5B_DIR（栈状态目录，默认 ~/.painuo-r5b-leads）、R5B_AUDIT_DIR、PW_CORE_PATH、CHROME_PATH。
import { readFileSync, mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import os from "node:os";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const DIR = process.env.R5B_DIR ?? path.join(os.homedir(), ".painuo-r5b-leads");
const AUDIT = process.env.R5B_AUDIT_DIR ??
  "/Users/goodman/DoubaoWork/chats/2026-11-21/new-chat-4/audit/screenshots/r5/leads";
const PW_CORE = process.env.PW_CORE_PATH ??
  "/Users/goodman/works/派诺生态/product-image-engine/web/node_modules/playwright-core/index.mjs";
const CHROME = process.env.CHROME_PATH ?? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

const state = JSON.parse(readFileSync(path.join(DIR, "r5b-leads-state.json"), "utf8"));
const { chromium } = await import(PW_CORE);
const browser = await chromium.launch({ executablePath: CHROME, headless: true });
const consoleErrors = [];
const pages = {};

async function shoot(name, viewport) {
  const ctx = await browser.newContext({ viewport, deviceScaleFactor: 1 });
  await ctx.addCookies([{ name: "leads_session", value: state.token, domain: "127.0.0.1", path: "/" }]);
  const page = await ctx.newPage();
  page.on("console", (m) => { if (m.type() === "error") consoleErrors.push(`${name}: ${m.text()}`); });
  await page.addInitScript((t) => localStorage.setItem("leads_tenant_id", t), state.tenant);
  await page.goto(`${state.web}/reception?session=${state.sessionId}`, { waitUntil: "networkidle", timeout: 60_000 });
  await page.waitForSelector("#follow-next[aria-haspopup='dialog'], #follow-next", { timeout: 30_000 });

  // 可交互探测：点开日期浮层，确认复合件内时间槽渲染；随后收起，不提交跟进。
  let dialogOpened = false;
  try {
    await page.click("#follow-next");
    await page.waitForSelector('input[type="time"]', { timeout: 5_000 });
    dialogOpened = await page.$('input[type="time"]') !== null;
    await page.keyboard.press("Escape");
  } catch { /* 浮层探测不阻断挂载态取证 */ }

  const metrics = await page.evaluate(() => ({
    vw: window.innerWidth, vh: window.innerHeight,
    scrollW: document.documentElement.scrollWidth, scrollH: document.documentElement.scrollHeight,
    hOverflow: document.documentElement.scrollWidth > window.innerWidth + 1,
    compositeMarkers: document.querySelectorAll('button[aria-haspopup="dialog"]').length,
    timeSlots: document.querySelectorAll('input[type="time"]').length,
    text: document.body.innerText.slice(0, 1200),
  }));
  mkdirSync(AUDIT, { recursive: true });
  const file = path.join(AUDIT, `${name}.png`);
  await page.screenshot({ path: file, fullPage: true });
  pages[name] = { url: `${state.web}/reception?session=${state.sessionId}`, dialogOpened, ...metrics, file };
  await ctx.close();
}

await shoot("leads-reception-1280-mounted", { width: 1280, height: 800 });
await shoot("leads-reception-390-mounted", { width: 390, height: 844 });
writeFileSync(path.join(AUDIT, "r5b-mounted-metrics.json"), JSON.stringify({ pages, consoleErrors }, null, 2));
console.log(JSON.stringify({ ok: true, pages: Object.keys(pages), consoleErrors }, null, 2));
await browser.close();
