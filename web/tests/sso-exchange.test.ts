/**
 * POST /api/auth/sso/exchange 换码桥测试（fix/sso-restore：按生产副本实际语义，非 guanlan 模板语义）。
 *
 * 生产契约（leads.modelxing.com）：
 *  - 所有失败都是 303 → https://leads.modelxing.com/login?sso=<粗粒度原因>（cache-control no-store）；
 *  - 原因词表只有五个：audience_mismatch / invalid_request / identity_unavailable /
 *    membership_required(仅 identity 403) / failed——identity 非 200 一律粗粒度 failed（已知债务）；
 *  - 成功只认 identity Set-Cookie 里出现 leads-engine_session=（cookie 名与 Go 侧
 *    LEADS_SESSION_COOKIE 生产显式值对齐；Go 缺省 leads_session 不被认）；
 *  - return_target 不呈递 identity，只在浏览器落点做站内白名单（safeReturnPath），缺省 /。
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { NextRequest } from "next/server";
import type { IncomingHttpHeaders } from "node:http";

const exchangeMock = vi.hoisted(() => vi.fn());
vi.mock("../src/lib/sso-bridge", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../src/lib/sso-bridge")>();
  return { ...actual, exchangeWithIdentity: exchangeMock };
});

import { POST } from "../src/app/api/auth/sso/exchange/route";
import {
  hostAllowed,
  opaqueExchangeCode,
  readSsoBridgeConfig,
  safeReturnPath,
  sameOriginPost,
} from "../src/lib/sso-bridge";

const HOST = "leads.modelxing.com";
const LOGIN = (reason: string) => `https://${HOST}/login?sso=${reason}`;

function postReq(body: string, headers: Record<string, string> = {}): NextRequest {
  return new NextRequest(`https://${HOST}/api/auth/sso/exchange`, {
    method: "POST",
    headers: {
      host: HOST,
      origin: `https://${HOST}`,
      "content-type": "application/x-www-form-urlencoded",
      ...headers,
    },
    body,
  });
}

beforeEach(() => {
  exchangeMock.mockReset();
  delete process.env.SSO_APP_HOST;
  delete process.env.IDENTITY_SSO_EXCHANGE_URL;
});

describe("路由：Host / same-origin 纪律（失败 303 login?sso=audience_mismatch）", () => {
  it("陌生 Host → 303 audience_mismatch，no-store", async () => {
    const res = await POST(postReq("code=abc", { host: "evil.example", origin: "https://evil.example" }));
    expect(res.status).toBe(303);
    expect(res.headers.get("location")).toBe(LOGIN("audience_mismatch"));
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(exchangeMock).not.toHaveBeenCalled();
  });

  it("无 Origin 且无 sec-fetch-site（非 same-origin）→ 303 audience_mismatch", async () => {
    const res = await POST(postReq("code=abc", { origin: "" }));
    expect(res.headers.get("location")).toBe(LOGIN("audience_mismatch"));
  });

  it("sec-fetch-site: cross-site（Origin 缺席）→ 303 audience_mismatch", async () => {
    const res = await POST(postReq("code=abc", { origin: "", "sec-fetch-site": "cross-site" }));
    expect(res.headers.get("location")).toBe(LOGIN("audience_mismatch"));
  });

  it("跨站 Origin → 303 audience_mismatch；www 子域 Origin 放行（无 code 落 invalid_request 证明过了门）", async () => {
    const evil = await POST(postReq("code=abc", { origin: "https://evil.example" }));
    expect(evil.headers.get("location")).toBe(LOGIN("audience_mismatch"));
    const www = await POST(postReq("", { origin: `https://www.${HOST}` }));
    // 过了 origin 门，进到码形门（body 无 code）→ invalid_request 而非 audience
    expect(www.headers.get("location")).toBe(LOGIN("invalid_request"));
  });

  it("无 Origin 但 sec-fetch-site: same-origin（同源表单直提形态）放行", async () => {
    exchangeMock.mockRejectedValue(new Error("down"));
    const res = await POST(postReq("code=abc", { origin: "", "sec-fetch-site": "same-origin" }));
    expect(res.headers.get("location")).toBe(LOGIN("identity_unavailable"));
    expect(exchangeMock).toHaveBeenCalledTimes(1);
  });
});

describe("路由：读码纪律（失败 303 login?sso=invalid_request）", () => {
  it("form 缺 code / 码形坏 → 303 invalid_request，identity 未被调", async () => {
    for (const body of ["", "return_target=/x", `code=${encodeURIComponent("a b")}`]) {
      const res = await POST(postReq(body));
      expect(res.headers.get("location")).toBe(LOGIN("invalid_request"));
    }
    expect(exchangeMock).not.toHaveBeenCalled();
  });

  it("JSON body {code} 也认；非字符串 code → invalid_request", async () => {
    exchangeMock.mockRejectedValue(new Error("down"));
    const ok = await POST(postReq(JSON.stringify({ code: "abc" }), { "content-type": "application/json" }));
    expect(ok.headers.get("location")).toBe(LOGIN("identity_unavailable"));
    const bad = await POST(postReq(JSON.stringify({ code: 42 }), { "content-type": "application/json" }));
    expect(bad.headers.get("location")).toBe(LOGIN("invalid_request"));
  });

  it("超 16KB body / 不认识的 content-type → invalid_request", async () => {
    const big = await POST(postReq(`code=${"a".repeat(17 * 1024)}`));
    expect(big.headers.get("location")).toBe(LOGIN("invalid_request"));
    const weird = await POST(postReq("code=abc", { "content-type": "text/plain" }));
    expect(weird.headers.get("location")).toBe(LOGIN("invalid_request"));
    expect(exchangeMock).not.toHaveBeenCalled();
  });
});

describe("路由：identity 交互（错误映射，粗粒度）", () => {
  it("identity 连不通（reject）→ 303 identity_unavailable", async () => {
    exchangeMock.mockRejectedValue(new Error("ECONNREFUSED"));
    const res = await POST(postReq("code=abc"));
    expect(res.headers.get("location")).toBe(LOGIN("identity_unavailable"));
  });

  it("identity 非 200 → 粗粒度 failed；仅 403 映射 membership_required", async () => {
    exchangeMock.mockResolvedValue({ status: 500, setCookie: [] });
    const res = await POST(postReq("code=abc"));
    expect(res.headers.get("location")).toBe(LOGIN("failed"));

    exchangeMock.mockResolvedValue({ status: 401, setCookie: [] });
    expect((await POST(postReq("code=abc"))).headers.get("location")).toBe(LOGIN("failed"));

    exchangeMock.mockResolvedValue({ status: 403, setCookie: [] });
    expect((await POST(postReq("code=abc"))).headers.get("location")).toBe(
      LOGIN("membership_required"),
    );
  });

  it("identity 200 但没有 leads-engine_session= cookie → 303 failed（cookie 名对齐门）", async () => {
    // Go 缺省 cookie 名 leads_session：即使 identity 回了会话 cookie，桥也不认。
    exchangeMock.mockResolvedValue({
      status: 200,
      setCookie: ["leads_session=tok; Path=/; HttpOnly", "leads_session_refresh=r; Path=/; HttpOnly"],
    });
    const res = await POST(postReq("code=abc"));
    expect(res.headers.get("location")).toBe(LOGIN("failed"));
    expect(res.headers.get("set-cookie")).toBeNull();
  });

  it("成功：303 站内落点 + 原样转发 identity Set-Cookie + 追加 leads_sso_reset=1 + no-store", async () => {
    exchangeMock.mockResolvedValue({
      status: 200,
      setCookie: [
        "leads-engine_session=tok; Path=/; HttpOnly; Secure",
        "leads-engine_session_refresh=r; Path=/; HttpOnly; Secure",
      ],
    });
    const res = await POST(postReq("code=abc"));
    expect(res.status).toBe(303);
    expect(res.headers.get("location")).toBe(`https://${HOST}/`);
    expect(res.headers.get("cache-control")).toBe("no-store");
    const cookies = res.headers.getSetCookie();
    expect(cookies).toContain("leads-engine_session=tok; Path=/; HttpOnly; Secure");
    expect(cookies).toContain("leads-engine_session_refresh=r; Path=/; HttpOnly; Secure");
    expect(cookies).toContain("leads_sso_reset=1; Path=/; Max-Age=60; SameSite=Lax; Secure");
  });

  it("客户端 IP 取 x-forwarded-for 第一跳；xff 缺席回退 x-real-ip；都无则不透传", async () => {
    exchangeMock.mockRejectedValue(new Error("down"));
    await POST(postReq("code=abc", { "x-forwarded-for": "203.0.113.9, 70.1.2.3" }));
    expect(exchangeMock).toHaveBeenCalledWith(expect.anything(), "abc", "203.0.113.9");
    // xff 缺席（非空串）才回退 x-real-ip
    await POST(postReq("code=abc", { "x-real-ip": "198.51.100.7" }));
    expect(exchangeMock).toHaveBeenLastCalledWith(expect.anything(), "abc", "198.51.100.7");
    await POST(postReq("code=abc"));
    expect(exchangeMock).toHaveBeenLastCalledWith(expect.anything(), "abc", null);
  });

  it("x-forwarded-for 为空串不触发 x-real-ip 回退（?? 语义：空串非 nullish）→ null", async () => {
    exchangeMock.mockRejectedValue(new Error("down"));
    await POST(postReq("code=abc", { "x-forwarded-for": "", "x-real-ip": "198.51.100.7" }));
    expect(exchangeMock).toHaveBeenLastCalledWith(expect.anything(), "abc", null);
  });
});

describe("路由：return_target 只做浏览器站内落点（不呈递 identity）", () => {
  beforeEach(() => {
    exchangeMock.mockResolvedValue({
      status: 200,
      setCookie: ["leads-engine_session=tok; Path=/; HttpOnly"],
    });
  });

  it("缺省 /；站内路径原样落点（含查询串）", async () => {
    expect((await POST(postReq("code=abc"))).headers.get("location")).toBe(`https://${HOST}/`);
    expect(
      (await POST(postReq("code=abc&return_target=%2Forders%2F5"))).headers.get("location"),
    ).toBe(`https://${HOST}/orders/5`);
    expect(
      (await POST(postReq("code=abc&return_target=%2Fworkbench%3Fdenied%3D1"))).headers.get("location"),
    ).toBe(`https://${HOST}/workbench?denied=1`);
  });

  it("站外落点一律回 /：协议相对、反斜杠、绝对 URL、控制字符、超长", async () => {
    for (const t of ["//evil.example", "/\\evil.example", "https://evil.example/x", "/x\nSet-Cookie: 1", `/${"a".repeat(513)}`]) {
      const res = await POST(postReq(`code=abc&return_target=${encodeURIComponent(t)}`));
      expect(res.headers.get("location")).toBe(`https://${HOST}/`);
    }
  });

  it("return_target 不会出现在对 identity 的调用里", async () => {
    exchangeMock.mockRejectedValue(new Error("down"));
    await POST(postReq(`code=abc&return_target=${encodeURIComponent("/orders/5")}`));
    expect(exchangeMock).toHaveBeenCalledWith(expect.anything(), "abc", null);
  });
});

describe("桥纯策略：opaqueExchangeCode（一次性码形状门）", () => {
  it("非字符串/空/越界/形状不符 → null；合法不透明码原样通过", () => {
    expect(opaqueExchangeCode(undefined)).toBeNull();
    expect(opaqueExchangeCode(42)).toBeNull();
    expect(opaqueExchangeCode("")).toBeNull();
    expect(opaqueExchangeCode("  ")).toBeNull();
    expect(opaqueExchangeCode("x".repeat(513))).toBeNull();
    expect(opaqueExchangeCode("a b")).toBeNull();
    expect(opaqueExchangeCode("a\u0000b")).toBeNull();
    expect(opaqueExchangeCode("sso_9f8e7d6c")).toBe("sso_9f8e7d6c");
    expect(opaqueExchangeCode("a.b_c-d~e")).toBe("a.b_c-d~e");
    expect(opaqueExchangeCode("x".repeat(512))).toHaveLength(512);
  });
});

describe("桥纯策略：safeReturnPath（只收站内落点）", () => {
  it("非字符串/空/越界/控制字符/反斜杠 → /", () => {
    expect(safeReturnPath(undefined)).toBe("/");
    expect(safeReturnPath(null)).toBe("/");
    expect(safeReturnPath("")).toBe("/");
    expect(safeReturnPath("  ")).toBe("/");
    expect(safeReturnPath(`/${"a".repeat(513)}`)).toBe("/");
    expect(safeReturnPath("/x\ny")).toBe("/");
    expect(safeReturnPath("/\\x")).toBe("/");
  });

  it("协议相对/绝对 URL → /；站内路径原样", () => {
    expect(safeReturnPath("//evil.example")).toBe("/");
    expect(safeReturnPath("https://evil.example")).toBe("/");
    expect(safeReturnPath("/orders/5")).toBe("/orders/5");
    expect(safeReturnPath("/workbench?denied=1")).toBe("/workbench?denied=1");
  });
});

describe("桥纯策略：hostAllowed / sameOriginPost", () => {
  it("hostAllowed：两个头都在场时必须全过；bare host 判定、www 放行、缺头拒收", () => {
    expect(hostAllowed(HOST, null, HOST)).toBe(true);
    expect(hostAllowed(`${HOST}:443`, `www.${HOST}`, HOST)).toBe(true);
    expect(hostAllowed("evil.example", null, HOST)).toBe(false);
    expect(hostAllowed(HOST, "evil.example", HOST)).toBe(false);
    expect(hostAllowed(null, null, HOST)).toBe(false);
  });

  it("sameOriginPost：Origin 缺席回退 sec-fetch-site===same-origin；在场则须 canonical/www", () => {
    expect(sameOriginPost({ origin: null, secFetchSite: "same-origin" }, HOST)).toBe(true);
    expect(sameOriginPost({ origin: null, secFetchSite: "cross-site" }, HOST)).toBe(false);
    expect(sameOriginPost({ origin: null, secFetchSite: null }, HOST)).toBe(false);
    expect(sameOriginPost({ origin: `https://${HOST}`, secFetchSite: null }, HOST)).toBe(true);
    expect(sameOriginPost({ origin: `https://www.${HOST}`, secFetchSite: null }, HOST)).toBe(true);
    expect(sameOriginPost({ origin: "https://evil.example", secFetchSite: null }, HOST)).toBe(false);
  });
});

describe("桥纯策略：readSsoBridgeConfig（缺省即生产形态）", () => {
  it("无 env → exchange 指向本机 identity、host 钉 leads.modelxing.com", () => {
    const cfg = readSsoBridgeConfig({});
    expect(cfg).toEqual({
      exchangeURL: "http://127.0.0.1:18101/api/auth/sso/exchange",
      appHost: "leads.modelxing.com",
    });
  });

  it("显式 env 覆盖生效", () => {
    const cfg = readSsoBridgeConfig({
      IDENTITY_SSO_EXCHANGE_URL: "http://127.0.0.1:19999/x",
      SSO_APP_HOST: "staging.modelxing.com",
    });
    expect(cfg.exchangeURL).toBe("http://127.0.0.1:19999/x");
    expect(cfg.appHost).toBe("staging.modelxing.com");
  });
});

describe("exchangeWithIdentity：真实 node:http 行为（本机临时服务，不打外部）", () => {
  it("POST JSON {code}，host/origin 用 canonical appHost，透传 clientIP，收集 set-cookie 数组", async () => {
    const { exchangeWithIdentity } =
      await vi.importActual<typeof import("../src/lib/sso-bridge")>("../src/lib/sso-bridge");
    const http = await import("node:http");
    const seen: { headers: IncomingHttpHeaders; body: string } = {} as never;
    const server = http.createServer((req, res) => {
      let body = "";
      seen.headers = req.headers;
      req.on("data", (c) => (body += c));
      req.on("end", () => {
        seen.body = body;
        res.setHeader("set-cookie", ["leads-engine_session=tok; Path=/", "second=2"]);
        res.statusCode = 200;
        res.end("{}");
      });
    });
    await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
    const addr = server.address();
    if (addr === null || typeof addr === "string") throw new Error("no port");
    try {
      const result = await exchangeWithIdentity(
        { exchangeURL: `http://127.0.0.1:${addr.port}/api/auth/sso/exchange`, appHost: HOST },
        "code-1",
        "203.0.113.9",
      );
      expect(seen.body).toBe(JSON.stringify({ code: "code-1" }));
      expect(seen.headers.host).toBe(HOST);
      expect(seen.headers.origin).toBe(`https://${HOST}`);
      expect(seen.headers["x-forwarded-for"]).toBe("203.0.113.9");
      expect(seen.headers.accept).toBe("application/json");
      expect(result.status).toBe(200);
      expect(result.setCookie).toEqual(["leads-engine_session=tok; Path=/", "second=2"]);
    } finally {
      server.close();
    }
  });

  it("上游拒连 → reject（路由层转 identity_unavailable）", async () => {
    const { exchangeWithIdentity } =
      await vi.importActual<typeof import("../src/lib/sso-bridge")>("../src/lib/sso-bridge");
    // 127.0.0.1:1（TCP 保留端口）不会有服务在听，连接立即被拒。
    await expect(
      exchangeWithIdentity(
        { exchangeURL: "http://127.0.0.1:1/api/auth/sso/exchange", appHost: HOST },
        "code-1",
      ),
    ).rejects.toThrow();
  });
});
