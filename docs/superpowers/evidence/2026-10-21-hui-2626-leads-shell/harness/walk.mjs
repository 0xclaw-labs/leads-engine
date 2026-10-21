// HUI-2626 local walkthrough (synthetic identity stand-in + local stack).
// Not a product artifact; no production credentials or real tenant data.
// Run with NODE_PATH pointing at a playwright-core install.
import { createRequire } from "node:module";
import fs from "node:fs";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_CORE || "playwright-core");

const TENANT = process.env.WALK_TENANT || "";
const OUT = process.env.WALK_OUT || "/tmp/leads-hui2626/screens";
const BASE = process.env.WALK_BASE || "http://127.0.0.1:29330";
const WIDTH = Number(process.env.WALK_WIDTH || 1440);
const HEIGHT = Number(process.env.WALK_HEIGHT || 900);
const TAG = process.env.WALK_TAG || `${WIDTH}`;
fs.mkdirSync(OUT, { recursive: true });

const log = [];
const step = (name, data = {}) => {
  const row = { step: name, ...data };
  log.push(row);
  console.log(JSON.stringify(row));
};

const browser = await chromium.launch({ channel: "chrome", headless: true });
const ctx = await browser.newContext({ viewport: { width: WIDTH, height: HEIGHT } });
await ctx.addCookies([
  { name: "leads_session", value: "tok_owner_walk", domain: "127.0.0.1", path: "/" },
]);
await ctx.addInitScript((tenant) => {
  try { localStorage.setItem("leads_tenant_id", tenant); } catch {}
}, TENANT);
const page = await ctx.newPage();
const consoleErrors = [];
page.on("console", (message) => {
  if (message.type() === "error") consoleErrors.push(message.text().slice(0, 240));
});
const shot = (name) => page.screenshot({ path: `${OUT}/${TAG}-${name}.png` });
let clicks = 0, fills = 0;

try {
  await page.goto(`${BASE}/`, { waitUntil: "networkidle", timeout: 180000 });
  await page.waitForSelector('[data-today-home="today-next"]', { timeout: 120000 });
  const todayText = await page.innerText("body");
  step("today_loaded", {
    hasAttention: todayText.includes("排序与金额口径"),
    hasReason: todayText.includes("为什么在今天"),
    hasConversation: todayText.includes("展开会话与回复"),
    hasDraft: todayText.includes("营业时间每天"),
    hasNavFault: todayText.includes("生态导航暂不可用"),
    hasEcoNav: (await page.locator('[data-testid="eco-top-nav"]').count()) > 0,
  });
  await shot("01-today");

  const toggle = page.getByRole("button", { name: "展开会话与回复" }).first();
  await toggle.focus();
  const focusedLabel = await page.evaluate(() => document.activeElement?.textContent?.trim() ?? "");
  await page.keyboard.press("Enter");
  clicks++;
  await page.waitForSelector("[data-conversation-session]", { timeout: 60000 });
  step("keyboard_open", { focusedLabel, expanded: await page.locator("[data-conversation-toggle]").first().getAttribute("aria-expanded") });
  await page.getByLabel("回复内容").waitFor({ timeout: 60000 });
  step("conversation_open", {
    panel: await page.locator("[data-conversation-session]").getAttribute("data-conversation-session"),
    hasThread: (await page.locator(".conversation-thread").innerText()).length > 0,
    state: await page.locator("[data-conversation-session]").getAttribute("data-conversation-state"),
  });
  await shot("02-conversation");

  const takeover = page.getByRole("button", { name: "转人工接管" });
  if ((await takeover.count()) > 0) {
    await takeover.first().click();
    clicks++;
    await page.waitForFunction(
      () => document.body.innerText.includes("已转人工接管"),
      undefined,
      { timeout: 60000 },
    );
    step("takeover_ok", { notice: "已转人工接管" });
  } else {
    step("takeover_skipped", { reason: "already human or session unavailable" });
  }
  await shot("03-takeover");

  const reply = "已确认：今天下午可以到店，营业到 18:00。";
  await page.waitForLoadState("networkidle");
  await page.getByLabel("回复内容").fill(reply);
  fills++;
  await page.waitForTimeout(500);
  const sessionForCheck = await page.locator("[data-conversation-session]").first().getAttribute("data-conversation-session");
  const sendButton = page.getByRole("button", { name: "批准并发送" }).first();
  await sendButton.waitFor({ state: "visible", timeout: 60000 });
  await sendButton.click({ trial: true, timeout: 60000 }).catch(() => {});
  await sendButton.click({ force: true, timeout: 60000 });
  clicks++;
  // 发送成功后页面会重拉 workbench；会话可能离开今天分组，内联面板随之卸载。
  // 因此“已发送”以接待台服务端回复状态为准，而不是要求通知一直留在 DOM。
  await page.waitForFunction(
    () => document.body.innerText.includes("回复已发送") || !document.querySelector("[data-conversation-session]"),
    undefined,
    { timeout: 60000 },
  );
  await page.goto(`${BASE}/reception?session=${sessionForCheck}`, { waitUntil: "networkidle", timeout: 120000 });
  await page.waitForSelector(`[aria-label="会话操作"]`, { timeout: 60000 });
  const receptionText = await page.innerText("body");
  step("sent_verified", {
    reply,
    hasReply: receptionText.includes(reply),
    hasSentStatus: receptionText.includes("已发送"),
  });
  await shot("04-sent");

  await page.goto(`${BASE}/leads`, { waitUntil: "networkidle", timeout: 120000 });
  await page.waitForSelector('[data-page="leads"]', { timeout: 60000 });
  const leadsText = await page.innerText("body");
  step("leads_list", {
    hasSalesOwner: leadsText.includes("销售甲"),
    hasRawMemberId: /mem_[0-9a-f]{16,}/.test(leadsText),
    hasPending: leadsText.includes("待分配"),
    hasTouchSource: leadsText.includes("碰一碰"),
    hasContactName: leadsText.includes("王客户"),
  });
  await shot("05-leads");

  await page.goto(`${BASE}/`, { waitUntil: "networkidle", timeout: 120000 });
  await shot("06-today-final");
  step("final", { url: page.url() });
} catch (error) {
  step("ERROR", { message: String(error?.message || error).slice(0, 400) });
  await shot("99-error").catch(() => {});
}

const errors404 = consoleErrors.filter((text) => text.includes("404"));
step("tally", {
  viewport: `${WIDTH}x${HEIGHT}`,
  clicks,
  fills,
  consoleErrorCount: consoleErrors.length,
  console404Count: errors404.length,
  consoleErrors,
});
fs.writeFileSync(`${OUT}/${TAG}-steps.json`, JSON.stringify(log, null, 2));
await browser.close();
