import { planLaunch, readLaunchEnv } from "@/lib/eco-nav/launch";

// HUI-2227：应用切换 BFF。
//
// POST { app_id, launch_target_id }，只带这两个字段。BFF 用会话 cookie 找
// leads 服务拿 principal/tenant，再调 identity shell/launch，校验回执后
// 303 到一次性 launch_url；导航用 JSON 模式拿 URL 后由浏览器跳转。
// 缺配置 / 未登录 / 被拒 / 目标不在 allowlist 都显式失败，不拼 URL。
export const dynamic = "force-dynamic";

const MAX_BODY = 4096;

function json(status: number, body: Record<string, unknown>): Response {
  return Response.json(body, { status, headers: { "cache-control": "no-store" } });
}

export async function POST(req: Request): Promise<Response> {
  const config = readLaunchEnv(process.env);
  if (!config.env) {
    return json(503, {
      error: "config_gate",
      message: "应用切换还没有配置完整（身份服务或目标允许列表缺失）。",
      detail: config.problems,
    });
  }
  const text = await req.text();
  if (text.length > MAX_BODY) return json(400, { error: "invalid_request", message: "请求体过大。" });
  let input: { app_id?: unknown; launch_target_id?: unknown };
  try {
    input = JSON.parse(text || "{}") as typeof input;
  } catch {
    return json(400, { error: "invalid_request", message: "请求不是合法 JSON。" });
  }
  if (typeof input.app_id !== "string" || typeof input.launch_target_id !== "string") {
    return json(400, { error: "invalid_request", message: "只接受 app_id 与 launch_target_id。" });
  }
  const result = await planLaunch(
    {
      appId: input.app_id,
      targetId: input.launch_target_id,
      cookie: req.headers.get("cookie") ?? "",
      tenant: req.headers.get("x-tenant-id") ?? "",
    },
    config,
  );
  if (!result.ok) {
    return json(result.status, { error: result.code, message: result.message });
  }
  const wantsJson = (req.headers.get("accept") ?? "").includes("application/json") || req.headers.get("x-eco-launch-mode") === "json";
  if (wantsJson) {
    return json(200, { ok: true, app_id: result.appId, target_id: result.targetId, launch_url: result.launchUrl });
  }
  return new Response(null, {
    status: 303,
    headers: { location: result.launchUrl, "cache-control": "no-store" },
  });
}
