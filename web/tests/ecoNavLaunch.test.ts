import { describe, expect, it, vi } from "vitest";
import {
  planLaunch,
  readLaunchEnv,
  validateLaunchUrl,
  type LaunchEnv,
} from "@/lib/eco-nav/launch";

const env: Record<string, string | undefined> = {
  PLATFORM_IDENTITY_BASE_URL: "http://127.0.0.1:18101",
  PLATFORM_IDENTITY_TOKEN: "identity-token",
  LEADS_SERVER_URL: "http://127.0.0.1:18230",
  LEADS_INTERNAL_TOKEN: "internal-token",
  LEADS_APP_ID: "leads-engine",
  LEADS_ECO_LAUNCH_HOSTS: "goboost.modelxing.com,orders.example.invalid",
};

const resolved: LaunchEnv = {
  identityBaseUrl: "http://127.0.0.1:18101",
  identityToken: "identity-token",
  leadsServerUrl: "http://127.0.0.1:18230",
  leadsInternalToken: "internal-token",
  appId: "leads-engine",
  launchHosts: ["goboost.modelxing.com", "orders.example.invalid"],
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function fakeFetch(handlers: {
  whoami?: () => Response | Promise<Response>;
  launch?: () => Response | Promise<Response>;
}) {
  const calls: Array<{ url: string; init: RequestInit }> = [];
  const fetchImpl = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(url), init: init ?? {} });
    if (String(url).endsWith("/api/v1/whoami")) {
      return handlers.whoami ? handlers.whoami() : jsonResponse({ principal_ref: "usr_owner", tenant_id: "tnt_a" });
    }
    if (String(url).endsWith("/internal/v1/identity/shell/launch")) {
      return handlers.launch
        ? handlers.launch()
        : jsonResponse({ ok: true, app_id: "goboost", target_id: "ti-goboost-web", launch_url: "https://goboost.modelxing.com/launch?code=one&h=handoff" });
    }
    return jsonResponse({ error: "unexpected" }, 500);
  }) as unknown as typeof fetch;
  return { fetchImpl, calls };
}

describe("readLaunchEnv", () => {
  it("reads a complete loopback identity configuration", () => {
    const out = readLaunchEnv(env);
    expect(out.problems).toEqual([]);
    expect(out.env).toMatchObject({ appId: "leads-engine", launchHosts: ["goboost.modelxing.com", "orders.example.invalid"] });
  });

  it("reports every missing key instead of defaulting a service", () => {
    const out = readLaunchEnv({});
    expect(out.env).toBeNull();
    for (const key of ["PLATFORM_IDENTITY_BASE_URL", "PLATFORM_IDENTITY_TOKEN", "LEADS_SERVER_URL", "LEADS_INTERNAL_TOKEN", "LEADS_APP_ID", "LEADS_ECO_LAUNCH_HOSTS"]) {
      expect(out.problems.join(" ")).toContain(key);
    }
  });

  it("refuses a non-loopback identity base or an invalid host allowlist", () => {
    expect(readLaunchEnv({ ...env, PLATFORM_IDENTITY_BASE_URL: "https://identity.example.com" }).env).toBeNull();
    expect(readLaunchEnv({ ...env, LEADS_ECO_LAUNCH_HOSTS: "https://goboost.example.com" }).env).toBeNull();
    expect(readLaunchEnv({ ...env, LEADS_ECO_LAUNCH_HOSTS: "" }).env).toBeNull();
  });
});

describe("validateLaunchUrl", () => {
  it("accepts https hosts on the allowlist, with a query code", () => {
    expect(validateLaunchUrl("https://goboost.modelxing.com/launch?code=c&h=x", ["goboost.modelxing.com"])).toBe(
      "https://goboost.modelxing.com/launch?code=c&h=x",
    );
  });

  it("rejects non-https, unlisted hosts, userinfo and fragments", () => {
    const hosts = ["goboost.modelxing.com"];
    expect(validateLaunchUrl("http://goboost.modelxing.com/launch", hosts)).toBeNull();
    expect(validateLaunchUrl("https://evil.example.com/launch", hosts)).toBeNull();
    expect(validateLaunchUrl("https://user:pass@goboost.modelxing.com/launch", hosts)).toBeNull();
    expect(validateLaunchUrl("https://goboost.modelxing.com/launch#frag", hosts)).toBeNull();
    expect(validateLaunchUrl("javascript:alert(1)", hosts)).toBeNull();
    expect(validateLaunchUrl("", hosts)).toBeNull();
  });

  it("allows loopback http only for an explicitly allowlisted loopback host", () => {
    expect(validateLaunchUrl("http://127.0.0.1:18330/launch", ["127.0.0.1"])).toBe("http://127.0.0.1:18330/launch");
  });
});

describe("planLaunch", () => {
  it("fails closed on missing configuration before any network call", async () => {
    const { fetchImpl, calls } = fakeFetch({});
    const out = await planLaunch(
      { appId: "goboost", targetId: "ti-goboost-web", cookie: "leads_session=tok", tenant: "tnt_a" },
      { env: null, problems: ["PLATFORM_IDENTITY_TOKEN is required"] },
      fetchImpl,
    );
    expect(out).toMatchObject({ ok: false, code: "config_gate", status: 503 });
    expect(calls).toHaveLength(0);
  });

  it("rejects malformed app/target ids before whoami", async () => {
    const { fetchImpl, calls } = fakeFetch({});
    const out = await planLaunch(
      { appId: "../../etc", targetId: "ti-goboost-web", cookie: "leads_session=tok", tenant: "tnt_a" },
      { env: resolved, problems: [] },
      fetchImpl,
    );
    expect(out).toMatchObject({ ok: false, code: "invalid_request", status: 400 });
    expect(calls).toHaveLength(0);
  });

  it("launches with the session principal/tenant and echoes validation", async () => {
    const { fetchImpl, calls } = fakeFetch({});
    const out = await planLaunch(
      { appId: "goboost", targetId: "ti-goboost-web", cookie: "leads_session=tok", tenant: "tnt_a" },
      { env: resolved, problems: [] },
      fetchImpl,
    );
    expect(out).toMatchObject({ ok: true, appId: "goboost", targetId: "ti-goboost-web" });
    const who = calls[0];
    expect(who.url).toBe("http://127.0.0.1:18230/api/v1/whoami");
    const whoHeaders = who.init.headers as Record<string, string>;
    expect(whoHeaders["x-internal-token"]).toBe("internal-token");
    expect(whoHeaders.cookie).toBe("leads_session=tok");
    expect(whoHeaders["x-tenant-id"]).toBe("tnt_a");
    const launch = calls[1];
    expect(launch.url).toBe("http://127.0.0.1:18101/internal/v1/identity/shell/launch");
    const launchHeaders = launch.init.headers as Record<string, string>;
    expect(launchHeaders["X-App-ID"]).toBe("leads-engine");
    expect(launchHeaders["X-PilotSeaView-Internal-Token"]).toBe("identity-token");
    expect(JSON.parse(String(launch.init.body))).toEqual({
      principal_id: "usr_owner",
      tenant_id: "tnt_a",
      app_id: "goboost",
      target_id: "ti-goboost-web",
    });
  });

  it("does not launch when whoami is unauthenticated or unavailable", async () => {
    for (const [status, code] of [[401, "unauthenticated"], [503, "unavailable"]] as const) {
      const { fetchImpl, calls } = fakeFetch({ whoami: () => jsonResponse({ error: "x" }, status) });
      const out = await planLaunch(
        { appId: "goboost", targetId: "ti-goboost-web", cookie: "leads_session=tok", tenant: "tnt_a" },
        { env: resolved, problems: [] },
        fetchImpl,
      );
      expect(out).toMatchObject({ ok: false, code, status: status === 401 ? 401 : 503 });
      expect(calls).toHaveLength(1);
    }
  });

  it("maps identity launch denials/throttles/outages without launching", async () => {
    const cases: Array<[number, string, number]> = [
      [403, "denied", 403],
      [429, "rate_limited", 429],
      [503, "unavailable", 503],
      [400, "invalid_request", 400],
    ];
    for (const [status, code, http] of cases) {
      const { fetchImpl } = fakeFetch({ launch: () => jsonResponse({ error: "shell_launch_denied" }, status) });
      const out = await planLaunch(
        { appId: "goboost", targetId: "ti-goboost-web", cookie: "leads_session=tok", tenant: "tnt_a" },
        { env: resolved, problems: [] },
        fetchImpl,
      );
      expect(out, `${status}`).toMatchObject({ ok: false, code, status: http });
    }
  });

  it("refuses a launch_url whose host is not allowlisted, or whose echo mismatches", async () => {
    const badUrl = fakeFetch({ launch: () => jsonResponse({ ok: true, app_id: "goboost", target_id: "ti-goboost-web", launch_url: "https://evil.example.com/x" }) });
    const out = await planLaunch(
      { appId: "goboost", targetId: "ti-goboost-web", cookie: "leads_session=tok", tenant: "tnt_a" },
      { env: resolved, problems: [] },
      badUrl.fetchImpl,
    );
    expect(out).toMatchObject({ ok: false, code: "invalid_launch_url" });

    const mismatch = fakeFetch({ launch: () => jsonResponse({ ok: true, app_id: "orders", target_id: "ti-goboost-web", launch_url: "https://goboost.modelxing.com/launch" }) });
    const out2 = await planLaunch(
      { appId: "goboost", targetId: "ti-goboost-web", cookie: "leads_session=tok", tenant: "tnt_a" },
      { env: resolved, problems: [] },
      mismatch.fetchImpl,
    );
    expect(out2).toMatchObject({ ok: false, code: "invalid_launch_url" });
  });

  it("refuses to launch into the current app (manual switch only)", async () => {
    const { fetchImpl, calls } = fakeFetch({});
    const out = await planLaunch(
      { appId: "leads-engine", targetId: "ti-leads-engine-web", cookie: "leads_session=tok", tenant: "tnt_a" },
      { env: resolved, problems: [] },
      fetchImpl,
    );
    expect(out).toMatchObject({ ok: false, code: "invalid_request", status: 400 });
    expect(calls).toHaveLength(0);
  });
});
