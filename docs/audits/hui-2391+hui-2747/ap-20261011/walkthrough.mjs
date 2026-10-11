// HUI-2391 浏览器走查（ap2 WI-E / R17e）：真实栈
// identity stand-in(:29310) + 真 AG10(:29340) + 真 leads-server(:29301)
// + next BFF(:29330)。channel:'chrome'，1440 与 390 双视口。
// 会话令牌文件路径经环境变量传入（scratchpad 内 chmod600，不入仓不打日志）。
import { readFileSync, writeFileSync } from "node:fs";
import { chromium } from "playwright";

const token = readFileSync(process.env.IDENTITY_TOKEN_FILE, "utf8").trim();
const leadID = process.argv[2];
const base = "http://127.0.0.1:29330";
const shots = process.env.WALKTHROUGH_SHOTS_DIR;
const hash = "cd" + "5".repeat(62); // fresh 64-hex for the UI journey

async function run(viewport, tag) {
  const browser = await chromium.launch({ channel: "chrome", headless: true });
  const ctx = await browser.newContext({ viewport, locale: "zh-CN" });
  await ctx.addCookies([{ name: "leads_session", value: token, url: base }]);
  await ctx.addInitScript((tenant) => {
    window.localStorage.setItem("leads_tenant_id", tenant);
  }, "tnt_walk");
  const page = await ctx.newPage();
  const result = { viewport: `${viewport.width}x${viewport.height}`, steps: {} };
  const t0 = Date.now();

  // 1. 打开活动位（回原位基准）
  await page.goto(`${base}/leads/${leadID}`, { waitUntil: "domcontentloaded" });
  const panel = page.getByTestId("matrix-draft-notice");
  await panel.waitFor({ timeout: 30000 });
  result.steps.opened = page.url();
  await page.screenshot({ path: `${shots}/${tag}-1-campaign-page.png`, fullPage: true });

  // 2. 面板填表（选择→送矩阵）
  await page.getByLabel("内容资产 id").fill("asset_copy_walk_ui");
  await page.getByLabel("内容哈希（64 位十六进制）").fill(hash);
  await page.getByLabel("内容版本").fill("3");
  await page.getByLabel("授权引用 grant_ref").fill("grant_walk_1");
  await page.getByLabel("素材许可 license_ref").fill("license_walk");
  await page.getByLabel("品牌 id").fill("brand_walk");
  await page.getByLabel("付款方 id").fill("payer_walk");
  await page.getByLabel("资产来源声明（可选）").fill("touch/landing/camp-walk");
  await page.screenshot({ path: `${shots}/${tag}-2-filled.png`, fullPage: true });

  // 3. 送矩阵
  await page.getByTestId("matrix-draft-submit").click();
  const facts = page.getByTestId("matrix-draft-facts");
  await facts.waitFor({ timeout: 30000 });
  const draftID = (await facts.textContent()).match(/md_[0-9a-f]+/)?.[0] ?? "";
  result.steps.draftID = draftID;
  await page.screenshot({ path: `${shots}/${tag}-3-sent.png`, fullPage: true });

  // 4. 回查（receipt 读真实 AG10）
  await page.getByTestId("matrix-draft-recheck").click();
  await page.getByTestId("matrix-draft-message").filter({ hasText: "回查完成" }).waitFor({ timeout: 30000 });
  result.steps.rechecked = true;
  await page.screenshot({ path: `${shots}/${tag}-4-recheck.png`, fullPage: true });

  // 5. 断言未知恒"—"、发布/扣费标记 false
  const text = await facts.textContent();
  result.steps.unknownsAreDash = (text.match(/—/g) || []).length >= 5;
  result.steps.publishFlagFalse = /发布标记\s*false/.test(text);
  result.steps.chargeFlagFalse = /扣费标记\s*false/.test(text);

  // 6. 回原位
  await page.getByTestId("matrix-draft-return").click();
  await page.waitForURL(`**/leads/${leadID}`, { timeout: 15000 });
  result.steps.backAt = page.url();
  result.steps.ms = Date.now() - t0;

  writeFileSync(`${shots}/${tag}-result.json`, JSON.stringify(result, null, 2));
  await browser.close();
  return result;
}

const out = {
  desktop: await run({ width: 1440, height: 900 }, "desktop-1440"),
  mobile: await run({ width: 390, height: 844 }, "mobile-390"),
};
writeFileSync(`${shots}/walkthrough.json`, JSON.stringify(out, null, 2));
console.log(JSON.stringify(out, null, 2));
