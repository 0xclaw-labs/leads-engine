// Timed metric round (R5 protocol). Test only.
// Usage: node metric.mjs <new|old> <task> <round> <lead_id> <visitor> <roundTag>
// Env: BFF_PORT, TENANT, SUBMIT_LABEL, RECOVERY(0|1), METRICS_OUT
// 主动时间 = 点击/填写动作本身的耗时之和；等待时间 = 总时长 - 主动时间
// （页面加载、渲染、网络、脚本定位都在等待侧）。步数=点击数；输入次数=填写数。
import { chromium } from "playwright-core";
import fs from "node:fs";
const [version, task, roundStr, leadId, visitor, roundTag] = process.argv.slice(2);
const round = Number(roundStr);
const BFF = `http://127.0.0.1:${process.env.BFF_PORT}`;
const TENANT = process.env.TENANT;
const SUBMIT = process.env.SUBMIT_LABEL || "记跟进";
const RECOVERY = process.env.RECOVERY === "1";
const OUT = process.env.METRICS_OUT;
const HEADERS = { "content-type": "application/json", cookie: "leads_session=tok_owner_walk", "x-tenant-id": TENANT };
const nextLocal = "2026-10-13T10:00";
const nextISO = new Date("2026-10-13T10:00:00").toISOString().replace(/\.\d{3}Z$/, "Z");
const browser = await chromium.launch({ channel: "chrome", headless: true });
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
await ctx.addCookies([{ name: "leads_session", value: "tok_owner_walk", domain: "127.0.0.1", path: "/" }]);
const page = await ctx.newPage();
await page.addInitScript((t) => { try { localStorage.setItem("leads_tenant_id", t); } catch {} }, TENANT);
const t0 = Date.now();
let active = 0;
let clicks = 0, fills = 0, missteps = [];
const timed = async (kind, fn) => { const s = Date.now(); await fn(); const d = Date.now() - s; if (kind === "active") active += d; return d; };
let faultArmed = RECOVERY;
if (faultArmed) {
  let n = 0;
  await page.route(`**/api/leads/${leadId}/follow-through`, async (route) => {
    n++;
    if (n === 1) return route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "unavailable", message: "服务暂未响应" }) });
    return route.fallback();
  });
}
let completed = false, recovery = "not_injected", noteKeptAfterFault = null, responseMs = null;
try {
  await page.goto(BFF + "/", { waitUntil: "networkidle", timeout: 240000 });
  const link = page.locator(`a[href="/leads/${leadId}"]`).first();
  await timed("wait", () => link.waitFor({ timeout: 60000 }));
  const cardVisible = await link.isVisible();
  if (!cardVisible) missteps.push("lead_card_not_visible_on_home");
  await timed("active", async () => { await link.click(); clicks++; });
  await timed("wait", () => page.waitForURL(`**/leads/${leadId}`, { timeout: 60000 }));
  const scope = page.locator("main");
  const recordBox = scope.getByLabel("记录");
  await timed("wait", () => recordBox.waitFor({ timeout: 60000 }));
  const note = RECOVERY ? "故障恢复轮：客户说考虑一下，周五再跟进一次" : `度量轮${round}：电话沟通顺利，客户需要报价单，周五发过去并确认`;
  await timed("active", async () => { await recordBox.fill(note); fills++; });
  await timed("active", async () => { await scope.getByLabel("下一次").fill(nextLocal); fills++; });
  const submitBtn = scope.getByRole("button", { name: SUBMIT, exact: true }).first();
  const respPromise = page.waitForResponse((r) => r.url().includes(`/leads/${leadId}/follow-through`) && r.request().method() === "POST", { timeout: 60000 });
  await timed("active", async () => { await submitBtn.click(); clicks++; });
  let resp;
  await timed("wait", async () => { resp = await respPromise; });
  responseMs = Date.now() - t0;
  if (resp.status() === 503) {
    const kept = await recordBox.inputValue();
    noteKeptAfterFault = kept === note;
    const body = await page.innerText("body");
    const recoveryShown = body.includes("服务暂未响应");
    recovery = recoveryShown && noteKeptAfterFault ? "entry_available_input_kept" : "missing";
    missteps.push("first_submit_503");
    // 恢复：直接重试（故障路由只拦第一次）
    const resp2Promise = page.waitForResponse((r) => r.url().includes(`/leads/${leadId}/follow-through`) && r.request().method() === "POST", { timeout: 60000 });
    await timed("active", async () => { await submitBtn.click(); clicks++; });
    let resp2;
    await timed("wait", async () => { resp2 = await resp2Promise; });
    completed = resp2.ok();
    recovery = completed ? `${recovery};retried_ok` : `${recovery};retry_failed_${resp2.status()}`;
  } else {
    completed = resp.ok();
    if (!completed) missteps.push(`submit_http_${resp.status()}`);
  }
  const total = Date.now() - t0;
  // 出组核对：新版看 today 分组；旧版看对应 desk 桶（旧 unprocessed 不随跟进出组，状态仍是 new，
  // 这是旧系统的真实摩擦，如实记录）。
  const wb = await fetch(`${BFF}/api/workbench`, { headers: HEADERS });
  const wbJson = await wb.json();
  let leftSurface = null;
  if (version === "new") {
    const today = wbJson.today ?? {};
    leftSurface = !Object.values(today).some((items) => (items ?? []).some((it) => it.lead_id === leadId));
  } else if (task === "due") {
    leftSurface = !((wbJson.buckets?.due_today ?? []).some((it) => it.lead_id === leadId));
  } else if (task === "waiting") {
    leftSurface = !((wbJson.buckets?.needs_schedule ?? []).some((it) => it.lead_id === leadId));
  } else {
    leftSurface = null; // 旧版新咨询任务无“出组”语义（status 停留 new）
  }
  const rec = { version, task, round, lead: leadId, visitor, steps: clicks, inputs: fills, active_ms: active, wait_ms: total - active, total_ms: total, missteps, completed, recovery, noteKeptAfterFault, leftSurface, submit_status: resp.status() };
  console.log(JSON.stringify(rec));
  fs.writeFileSync(`${OUT}/${roundTag}.json`, JSON.stringify(rec, null, 2));
} catch (e) {
  const total = Date.now() - t0;
  const rec = { version, task, round, lead: leadId, visitor, steps: clicks, inputs: fills, active_ms: active, wait_ms: total - active, total_ms: total, missteps: [...missteps, "ERROR"], completed, recovery, error: String(e.message || e).slice(0, 200) };
  console.log(JSON.stringify(rec));
  fs.writeFileSync(`${OUT}/${roundTag}.json`, JSON.stringify(rec, null, 2));
  await page.screenshot({ path: `${OUT}/${roundTag}-error.png`, fullPage: true });
}
await browser.close();
