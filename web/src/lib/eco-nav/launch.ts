// HUI-2227：应用切换只经自家 BFF。
//
// 浏览器只发送 (app_id, launch_target_id)。BFF 用会话 cookie 向 leads 服务取
// principal/tenant，再调 public-ai identity 的 shell/launch，拿回一次性 launch_url。
// 这里不拼生态域名、不复制业务载荷、不跟随重定向、失败即拒绝。
//
// 配置缺失（identity 地址/令牌、leads 服务、app id、host allowlist）一律
// fail-closed；launch_url 必须 https（loopback 显式 allowlist 例外）且 host 在
// 部署注入的 allowlist 里，否则不跳转。

const SLUG = /^[a-z0-9][a-z0-9-]{0,63}$/;
const HOST = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/;

const REQUIRED_KEYS = [
  "PLATFORM_IDENTITY_BASE_URL",
  "PLATFORM_IDENTITY_TOKEN",
  "LEADS_SERVER_URL",
  "LEADS_INTERNAL_TOKEN",
  "LEADS_APP_ID",
  "LEADS_ECO_LAUNCH_HOSTS",
] as const;

const LOOPBACK_HOSTS = new Set(["127.0.0.1", "::1", "localhost"]);

export interface LaunchEnv {
  identityBaseUrl: string;
  identityToken: string;
  leadsServerUrl: string;
  leadsInternalToken: string;
  appId: string;
  launchHosts: string[];
}

export interface LaunchConfig {
  env: LaunchEnv | null;
  problems: string[];
}

export interface LaunchInput {
  appId: string;
  targetId: string;
  cookie: string;
  tenant: string;
}

export type LaunchErrorCode =
  | "config_gate"
  | "invalid_request"
  | "unauthenticated"
  | "denied"
  | "rate_limited"
  | "unavailable"
  | "invalid_launch_url";

export type LaunchResult =
  | { ok: true; appId: string; targetId: string; launchUrl: string }
  | { ok: false; code: LaunchErrorCode; status: number; message: string };

function loopbackBase(raw: string): string | null {
  try {
    const url = new URL(raw);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    if (!LOOPBACK_HOSTS.has(url.hostname)) return null;
    if (url.username || url.password) return null;
    return url.origin;
  } catch {
    return null;
  }
}

export function readLaunchEnv(raw: Record<string, string | undefined>): LaunchConfig {
  const problems: string[] = [];
  for (const key of REQUIRED_KEYS) {
    if (!(raw[key] ?? "").trim()) problems.push(`${key} is required`);
  }
  const identityBaseUrl = loopbackBase((raw.PLATFORM_IDENTITY_BASE_URL ?? "").trim());
  if ((raw.PLATFORM_IDENTITY_BASE_URL ?? "").trim() && !identityBaseUrl) {
    problems.push("PLATFORM_IDENTITY_BASE_URL must be a loopback http(s) origin");
  }
  const leadsServerUrl = loopbackBase((raw.LEADS_SERVER_URL ?? "").trim());
  if ((raw.LEADS_SERVER_URL ?? "").trim() && !leadsServerUrl) {
    problems.push("LEADS_SERVER_URL must be a loopback http(s) origin");
  }
  const appId = (raw.LEADS_APP_ID ?? "").trim();
  if (appId && !SLUG.test(appId)) problems.push("LEADS_APP_ID must be a lowercase slug");
  const launchHosts = (raw.LEADS_ECO_LAUNCH_HOSTS ?? "")
    .split(",")
    .map((host) => host.trim().toLowerCase())
    .filter(Boolean);
  if (launchHosts.length === 0) {
    if ((raw.LEADS_ECO_LAUNCH_HOSTS ?? "").trim() === "") {
      // 已计入 REQUIRED_KEYS；这里不重复报错。
    }
  }
  for (const host of launchHosts) {
    if (!HOST.test(host) || host.includes("/") || host.includes(":")) {
      problems.push("LEADS_ECO_LAUNCH_HOSTS entries must be bare hostnames");
      break;
    }
  }
  if (problems.length > 0 || !identityBaseUrl || !leadsServerUrl) return { env: null, problems };
  return {
    env: {
      identityBaseUrl,
      identityToken: (raw.PLATFORM_IDENTITY_TOKEN ?? "").trim(),
      leadsServerUrl,
      leadsInternalToken: (raw.LEADS_INTERNAL_TOKEN ?? "").trim(),
      appId,
      launchHosts,
    },
    problems: [],
  };
}

export function validateLaunchUrl(raw: string, hosts: string[]): string | null {
  const text = (raw ?? "").trim();
  if (!text || text.length > 2048) return null;
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    return null;
  }
  if (url.username || url.password || url.hash) return null;
  const host = url.hostname.toLowerCase();
  if (!hosts.includes(host)) return null;
  if (url.protocol === "https:") return url.toString();
  if (url.protocol === "http:" && LOOPBACK_HOSTS.has(host)) return url.toString();
  return null;
}

function fail(code: LaunchErrorCode, status: number, message: string): LaunchResult {
  return { ok: false, code, status, message };
}

interface WhoamiBody {
  principal_ref?: unknown;
  tenant_id?: unknown;
}

export async function planLaunch(
  input: LaunchInput,
  config: LaunchConfig,
  fetchImpl: typeof fetch = fetch,
): Promise<LaunchResult> {
  if (!config.env || config.problems.length > 0) {
    return fail("config_gate", 503, "应用切换还没有配置完整（身份服务或目标允许列表缺失）。");
  }
  const env = config.env;
  const appId = (input.appId ?? "").trim();
  const targetId = (input.targetId ?? "").trim();
  const cookie = (input.cookie ?? "").trim();
  const tenant = (input.tenant ?? "").trim();
  if (!SLUG.test(appId) || !SLUG.test(targetId) || appId === env.appId) {
    return fail("invalid_request", 400, "只接受其它应用的 app_id 与已登记的 launch target。");
  }
  if (!cookie) return fail("unauthenticated", 401, "登录已过期，请重新登录后再切换。");
  if (!tenant) return fail("invalid_request", 400, "先在顶部选择工作范围。");

  let whoami: Response;
  try {
    whoami = await fetchImpl(`${env.leadsServerUrl}/api/v1/whoami`, {
      method: "GET",
      headers: {
        "x-internal-token": env.leadsInternalToken,
        cookie,
        "x-tenant-id": tenant,
      },
      cache: "no-store",
      redirect: "manual",
    });
  } catch {
    return fail("unavailable", 503, "工作范围服务暂不可达，没有发起切换。");
  }
  if (whoami.status === 401) return fail("unauthenticated", 401, "登录已过期，请重新登录后再切换。");
  if (whoami.status === 403) return fail("denied", 403, "当前身份在这个工作范围里没有成员关系。");
  if (!whoami.ok) return fail("unavailable", 503, "工作范围服务暂不可用，没有发起切换。");
  let who: WhoamiBody;
  try {
    who = (await whoami.json()) as WhoamiBody;
  } catch {
    return fail("unavailable", 503, "工作范围响应无法读取，没有发起切换。");
  }
  const principal = typeof who.principal_ref === "string" ? who.principal_ref.trim() : "";
  const serverTenant = typeof who.tenant_id === "string" ? who.tenant_id.trim() : "";
  if (!principal || !serverTenant) return fail("unavailable", 503, "服务端没有给出可用的身份或工作范围。");
  if (serverTenant !== tenant) return fail("denied", 403, "工作范围与登录身份不一致，已拒绝切换。");

  const body = JSON.stringify({ principal_id: principal, tenant_id: serverTenant, app_id: appId, target_id: targetId });
  let launched: Response;
  try {
    launched = await fetchImpl(`${env.identityBaseUrl}/internal/v1/identity/shell/launch`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "X-App-ID": env.appId,
        "X-PilotSeaView-Internal-Token": env.identityToken,
      },
      body,
      cache: "no-store",
      redirect: "manual",
    });
  } catch {
    return fail("unavailable", 503, "身份服务暂不可达，目标应用没有打开。");
  }
  if (launched.status === 403) return fail("denied", 403, "目标应用这次不能切换（权限或权益未通过）。");
  if (launched.status === 429) return fail("rate_limited", 429, "切换太频繁，稍后再试。");
  if (launched.status === 400) return fail("invalid_request", 400, "目标应用或登记目标不在允许范围。");
  if (!launched.ok) return fail("unavailable", 503, "身份服务暂不可用，目标应用没有打开。");
  let payload: Record<string, unknown>;
  try {
    payload = (await launched.json()) as Record<string, unknown>;
  } catch {
    return fail("invalid_launch_url", 502, "身份服务返回了无法读取的切换结果。");
  }
  if (payload.ok !== true || payload.app_id !== appId || payload.target_id !== targetId) {
    return fail("invalid_launch_url", 502, "切换结果与请求不一致，已拒绝跳转。");
  }
  const launchUrl = typeof payload.launch_url === "string" ? payload.launch_url : "";
  const validated = validateLaunchUrl(launchUrl, env.launchHosts);
  if (!validated) return fail("invalid_launch_url", 502, "目标地址不在部署允许列表内，已拒绝跳转。");
  return { ok: true, appId, targetId, launchUrl: validated };
}
