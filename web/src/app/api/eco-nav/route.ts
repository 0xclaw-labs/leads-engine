import { fetchEcoNavModel } from "@/lib/eco-nav/load";

// HUI-2227：服务端组合生态导航视图。
//
// 浏览器只打本站 BFF；会话 cookie 与工作范围头原样带到目录服务，由目录服务
// 返回 eco-nav/v1 文档。这里校验并映射成视图（品牌/应用/租户都来自服务端），
// 失败显式 blocked，不回退预览 fixture。
export const dynamic = "force-dynamic";

export async function GET(req: Request): Promise<Response> {
  const cookie = req.headers.get("cookie") ?? "";
  const tenant = req.headers.get("x-tenant-id") ?? "";
  const model = await fetchEcoNavModel({
    endpoint: (process.env.PUBLIC_AI_ECO_NAV_URL ?? "").trim() || null,
    cookie,
    tenant,
  });
  return Response.json(
    { model },
    { status: 200, headers: { "cache-control": "no-store" } },
  );
}
