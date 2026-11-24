// r5b-leads-stack.mjs —— R5B 接待工作台取证本地栈（dev-only，仅 127.0.0.1 回环）。
//
// 仅本地开发/截图取证用，生产绝不加载；不改生产鉴权、不放宽 fail-closed、
// 不动业务守卫判定条件。组成：
//   - 内联工程身份桩（复用 touch-followup-identity-fixture.mjs）：session/resolve/login；
//   - leads-server（18230，FEATURE_RECEPTION + FEATURE_FOLLOWUPS + 双 pepper）；
//   - Next Web（18330，BFF 同源转发）。
//
// 种子口径（全部走正式业务链与审计，不手写授权/会话表）：
//   - provision CLI 建租户 + owner 成员（principal usr_rcpt_owner）；
//   - 身份桩 login 换会话令牌；
//   - 员工侧建接待挂件 → 公开面开访客会话 → 员工侧挂受权线索（sales_followup 口径）；
//   - 跟进复合件在会话已挂线索后才挂载，与产品守卫一致。
//
// 用法：
//   node scripts/r5b-leads-stack.mjs
//   环境：R5B_DIR（隔离目录，默认 ~/.painuo-r5b-leads）、R5B_SERVER_BIN、KEEP_STACK（默认 1）。
import { spawn, execFileSync } from "node:child_process";
import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import os from "node:os";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(here, "..");
const SERVER_ROOT = path.join(ROOT, "server");
const WEB_ROOT = path.join(ROOT, "web");
const FIXTURE = path.join(here, "touch-followup-identity-fixture.mjs");

const DIR = process.env.R5B_DIR ?? path.join(os.homedir(), ".painuo-r5b-leads");
const DB = path.join(DIR, "data", "leads.db");
const IDENTITY_PORT = Number(process.env.R5B_PORT_IDENTITY ?? 18101);
const SERVER_PORT = Number(process.env.R5B_PORT_SERVER ?? 18230);
const WEB_PORT = Number(process.env.R5B_PORT_WEB ?? 18330);
const SERVER = `http://127.0.0.1:${SERVER_PORT}`;
const WEB = `http://127.0.0.1:${WEB_PORT}`;
const IDENTITY = `http://127.0.0.1:${IDENTITY_PORT}`;

const INTERNAL_TOKEN = "local-internal-r5b";
const IDENTITY_TOKEN = "fixture-token-r5b";
const APP_ID = "leads-engine";
const PRINCIPAL = "usr_rcpt_owner";
const EMAIL = "owner@r5b.local";
const PASSWORD = "r5b-pass";

fs.mkdirSync(path.join(DIR, "data"), { recursive: true });
fs.mkdirSync(path.join(DIR, "logs"), { recursive: true });
for (const suffix of ["", "-wal", "-shm"]) fs.rmSync(DB + suffix, { force: true });

const SERVER_BIN = process.env.R5B_SERVER_BIN ?? path.join(DIR, "bin", "leads-server");
if (!fs.existsSync(SERVER_BIN)) {
  fs.mkdirSync(path.dirname(SERVER_BIN), { recursive: true });
  execFileSync("go", ["build", "-o", SERVER_BIN, "./cmd/leads-server"], { cwd: SERVER_ROOT, stdio: "inherit" });
}

// 1) provision：租户 + owner 成员（CLI 写成员行，带 cli-bootstrap 来源）。
function provision(args) {
  return execFileSync(SERVER_BIN, args, { cwd: SERVER_ROOT, encoding: "utf8" }).trim();
}
const tenant = provision(["provision-tenant", "-db", DB, "-name", "R5B 取证租户"]);
const member = provision(["provision-member", "-db", DB, "-tenant", tenant, "-principal", PRINCIPAL, "-role", "owner", "-name", "接待负责人"]);

// 2) 身份桩配置（config mode 0600 口径；只含本地演示 principal）。
const fixtureConfig = {
  port: IDENTITY_PORT,
  apps: { [APP_ID]: { token: IDENTITY_TOKEN, host: `127.0.0.1:${WEB_PORT}` } },
  users: [{ principal_id: PRINCIPAL, email: EMAIL, password: PASSWORD, enabled: true, tenants: { [APP_ID]: [tenant] } }],
};
const fixtureConfigPath = path.join(DIR, "fixture-config.json");
fs.writeFileSync(fixtureConfigPath, JSON.stringify(fixtureConfig, null, 2), { mode: 0o600 });

const procs = new Set();
function launch(name, cmd, args, opts) {
  const log = fs.openSync(path.join(DIR, "logs", `${name}.log`), "a");
  const child = spawn(cmd, args, { ...opts, stdio: ["ignore", log, log] });
  procs.add(child);
  child.on("exit", () => procs.delete(child));
  return child;
}
async function stop(c) { if (c && c.exitCode === null) { c.kill("SIGTERM"); await new Promise((r) => c.once("exit", r)); } }
async function shutdown(code) { for (const c of [...procs]) await stop(c); process.exit(code); }
process.on("SIGINT", () => void shutdown(130));

const fixture = launch("identity", process.execPath, [FIXTURE, fixtureConfigPath]);

const serverEnv = {
  PATH: process.env.PATH, HOME: os.homedir(),
  LEADS_HTTP_ADDR: `127.0.0.1:${SERVER_PORT}`,
  LEADS_DB_PATH: DB,
  LEADS_INTERNAL_TOKEN: INTERNAL_TOKEN,
  PLATFORM_IDENTITY_BASE_URL: IDENTITY,
  PLATFORM_IDENTITY_TOKEN: IDENTITY_TOKEN,
  LEADS_APP_ID: APP_ID,
  FEATURE_RECEPTION: "on", FEATURE_FOLLOWUPS: "on",
  RECEPTION_VISITOR_PEPPER: "r5b-pepper-r5b",
  LEADS_DEDUP_PEPPER: "r5b-dedup-pepper",
  NEXT_TELEMETRY_DISABLED: "1",
};
const server = launch("server", SERVER_BIN, [], { env: serverEnv });

async function waitFor(url, tries = 120) {
  for (let i = 0; i < tries; i++) {
    try { const res = await fetch(url, { signal: AbortSignal.timeout(3000) }); if (res.status < 500) return; } catch { /* wait */ }
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`not healthy: ${url}`);
}
await waitFor(`${SERVER}/healthz`);

const nextBin = path.join(WEB_ROOT, "node_modules", "next", "dist", "bin", "next");
launch("web", process.execPath, [nextBin, "dev", "-p", String(WEB_PORT), "-H", "127.0.0.1"], {
  cwd: WEB_ROOT,
  env: { ...process.env, LEADS_SERVER_URL: SERVER, LEADS_INTERNAL_TOKEN: INTERNAL_TOKEN, NEXT_TELEMETRY_DISABLED: "1" },
});
await waitFor(`${WEB}/reception`, 180);

// 3) 身份桩 login 换会话令牌（不手写会话表）。
const login = await fetch(`${IDENTITY}/internal/v1/identity/login`, {
  method: "POST", headers: { "Content-Type": "application/json", "X-App-ID": APP_ID, "X-PilotSeaView-Internal-Token": IDENTITY_TOKEN },
  body: JSON.stringify({ email: EMAIL, password: PASSWORD, app_id: APP_ID }),
});
if (!login.ok) throw new Error(`fixture login ${login.status}`);
const token = (await login.json()).access_token;

async function api(pathname, { method = "GET", body, staff = true } = {}) {
  const headers = { "X-Internal-Token": INTERNAL_TOKEN, "x-tenant-id": tenant };
  if (staff) headers.Cookie = `leads_session=${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(`${SERVER}/api/v1${pathname}`, {
    method, headers, body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let json = {}; try { json = text ? JSON.parse(text) : {}; } catch { json = { raw: text }; }
  if (!res.ok) throw new Error(`${pathname} -> ${res.status} ${text}`);
  return json;
}

// 4) 挂件 → 访客会话 → 受权线索。
const widget = await api("/reception/widgets", { method: "POST", body: { default_mode: "ai", persona_wording: "R5B 取证接待", language: "zh" } });
const opened = await api(`/public/reception/widgets/${widget.id}/sessions`, {
  method: "POST", staff: false, body: { visitor_key: "visitor-r5b-0001", tenant_id: tenant },
});
const sessionId = opened.session.id;
const lead = await api(`/reception/sessions/${sessionId}/lead`, {
  method: "POST",
  body: {
    purpose: "sales_followup", allow_contact: true, notice_version: "r5b-notice-v1",
    contact_name: "R5B 客户", phone: "13800001111", email: "customer@r5b.local", marketing_allowed: false,
  },
});

const state = {
  at: new Date().toISOString(), web: WEB, server: SERVER, identity: IDENTITY, db: DB,
  tenant, member, principal: PRINCIPAL, token,
  widgetId: widget.id, sessionId, contactId: lead.contact_id, leadId: lead.lead_id,
};
fs.writeFileSync(path.join(DIR, "r5b-leads-state.json"), JSON.stringify(state, null, 2));
console.log("R5B Leads 栈就绪：", JSON.stringify({ web: WEB, tenant, sessionId, leadId: lead.lead_id }, null, 2));

if (process.env.KEEP_STACK !== "0") {
  console.log(`接待面 ${WEB}/reception?session=${sessionId} ｜ Ctrl+C 停止`);
  await new Promise(() => {});
} else {
  await shutdown(0);
}
