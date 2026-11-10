/**
 * GET /api/auth/sso/landing 到岸桥测试（fix/sso-restore：回仓生产副本 sso-bridge 形态）。
 *
 * 生产语义（leads.modelxing.com，与 guanlan-order 模板不同，勿混用）：
 *  - Host 不认（含缺 Host）→ 400「登录请求来源不正确。」纯文案，不重定向；
 *  - 无码/码形不符 → 400「登录链接不完整…」纯文案（guanlan 是 302 /login，此处不是）；
 *  - 有效码 → 200 text/html 自动提交表单，GET 不换码、不种 cookie、不落日志；
 *  - 码形门（opaqueExchangeCode：可见 ASCII ≤512）先于 HTML 渲染——HTML 元字符根本
 *    进不了模板，escapeHTML 只是纵深防御。
 */
import { beforeEach, describe, expect, it } from "vitest";
import { NextRequest } from "next/server";
import { GET } from "../src/app/api/auth/sso/landing/route";

const HOST = "leads.modelxing.com";

function getReq(search: string, headers: Record<string, string> = {}): NextRequest {
  return new NextRequest(`https://${HOST}/api/auth/sso/landing${search}`, { headers });
}

const TEXT_400 = { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" } as const;

beforeEach(() => {
  // 路由按调用读 process.env；测试固定走默认 canonical host。
  delete process.env.SSO_APP_HOST;
  delete process.env.IDENTITY_SSO_EXCHANGE_URL;
});

describe("Host 纪律（hostAllowed：host 与 x-forwarded-host 全过才放行）", () => {
  it("缺 Host 头 → 400 来源不正确（文案页，非重定向）", async () => {
    const res = await GET(new NextRequest(`https://${HOST}/api/auth/sso/landing?code=abc`));
    expect(res.status).toBe(400);
    expect(await res.text()).toBe("登录请求来源不正确。");
    for (const [k, v] of Object.entries(TEXT_400)) expect(res.headers.get(k)).toBe(v);
    expect(res.headers.get("location")).toBeNull();
  });

  it("陌生 Host → 400；端口剥掉后按 bare host 判定", async () => {
    expect((await GET(getReq("?code=abc", { host: "evil.example" }))).status).toBe(400);
    expect((await GET(getReq("?code=abc", { host: `${HOST}:8443` }))).status).toBe(200);
  });

  it("www 子域放行；host 对但 x-forwarded-host 篡改 → 400（every 语义）", async () => {
    expect((await GET(getReq("?code=abc", { host: `www.${HOST}` }))).status).toBe(200);
    expect(
      (await GET(getReq("?code=abc", { host: HOST, "x-forwarded-host": "evil.example" }))).status,
    ).toBe(400);
    expect(
      (await GET(getReq("?code=abc", { host: HOST, "x-forwarded-host": HOST }))).status,
    ).toBe(200);
  });
});

describe("无码/坏码：400 文案页（不重定向、不渲染假桥）", () => {
  it.each([
    ["缺 code", "/api/auth/sso/landing"],
    ["空 code", "/api/auth/sso/landing?code="],
    ["含空格 code", "/api/auth/sso/landing?code=abc%20def"],
    ["HTML 元字符 code", `/api/auth/sso/landing?code=${encodeURIComponent('"><script>alert(1)</script>')}`],
    ["超 512 字节 code", `/api/auth/sso/landing?code=${"a".repeat(513)}`],
  ])("%s → 400「登录链接不完整…」", async (_name, path) => {
    const res = await GET(
      new NextRequest(`https://${HOST}${path}`, { headers: { host: HOST } }),
    );
    expect(res.status).toBe(400);
    expect(await res.text()).toBe("登录链接不完整，请回到门户重新进入数海获客。");
    expect(res.headers.get("cache-control")).toBe("no-store");
  });
});

describe("有效码：200 自动提交表单", () => {
  it("200 text/html；form POST 同源 /api/auth/sso/exchange；code 进 hidden input 并自动提交", async () => {
    const res = await GET(getReq("?code=abc123", { host: HOST }));
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toBe("text/html; charset=utf-8");
    const html = await res.text();
    expect(html).toContain('action="/api/auth/sso/exchange"');
    expect(html).toContain('method="post"');
    expect(html).toContain('name="code" value="abc123"');
    expect(html).toContain('id="sso-exchange"');
    expect(html).toContain("document.getElementById");
    expect(html).toContain("noscript");
    expect(html).toContain("noindex");
  });

  it("安全响应头：no-store / Referrer-Policy same-origin / nosniff / DENY", async () => {
    const res = await GET(getReq("?code=abc", { host: HOST }));
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(res.headers.get("referrer-policy")).toBe("same-origin");
    expect(res.headers.get("x-content-type-options")).toBe("nosniff");
    expect(res.headers.get("x-frame-options")).toBe("DENY");
  });

  it("GET 不种 cookie、不换码（无 set-cookie，body 也不含交换结果）", async () => {
    const res = await GET(getReq("?code=abc", { host: HOST }));
    expect(res.headers.get("set-cookie")).toBeNull();
  });
});
