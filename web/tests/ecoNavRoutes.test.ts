import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GET as getEcoNav } from "@/app/api/eco-nav/route";
import { POST as postLaunch } from "@/app/api/eco-nav/launch/route";

const fixture = JSON.parse(readFileSync(join(__dirname, "eco-nav/fixtures/ready-first-party.json"), "utf8"));

const ENV = {
  PUBLIC_AI_ECO_NAV_URL: "http://127.0.0.1:18101/internal/v1/eco-nav",
  PLATFORM_IDENTITY_BASE_URL: "http://127.0.0.1:18101",
  PLATFORM_IDENTITY_TOKEN: "identity-token",
  LEADS_SERVER_URL: "http://127.0.0.1:18230",
  LEADS_INTERNAL_TOKEN: "internal-token",
  LEADS_APP_ID: "leads-engine",
  LEADS_ECO_LAUNCH_HOSTS: "goboost.modelxing.com",
};

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function installFetch() {
  const calls: Array<{ url: string; init: RequestInit }> = [];
  const stub = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(url), init: init ?? {} });
    if (String(url).includes("/api/v1/whoami")) return json({ principal_ref: "usr_owner", tenant_id: "tnt_a" });
    if (String(url).includes("/internal/v1/identity/shell/launch")) {
      return json({ ok: true, app_id: "goboost", target_id: "ti-goboost-web", launch_url: "https://goboost.modelxing.com/launch?code=c&h=h" });
    }
    return json(fixture);
  });
  vi.stubGlobal("fetch", stub);
  return { calls };
}

beforeEach(() => {
  for (const [key, value] of Object.entries(ENV)) vi.stubEnv(key, value);
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
});

describe("GET /api/eco-nav", () => {
  it("returns the server-composed view with the session cookie and tenant", async () => {
    const { calls } = installFetch();
    const res = await getEcoNav(
      new Request("http://localhost/api/eco-nav", {
        headers: { cookie: "leads_session=tok_owner", "x-tenant-id": "tnt_a" },
      }),
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as { model: { renderable: boolean; provenance: string } };
    expect(body.model.renderable).toBe(true);
    expect(body.model.provenance).toBe("public_ai_context");
    expect(res.headers.get("cache-control")).toBe("no-store");
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.cookie).toContain("leads_session=tok_owner");
    expect(headers["x-tenant-id"]).toBe("tnt_a");
  });

  it("returns a blocked view when the directory is unconfigured (no fixture)", async () => {
    vi.stubEnv("PUBLIC_AI_ECO_NAV_URL", "");
    const { calls } = installFetch();
    const res = await getEcoNav(
      new Request("http://localhost/api/eco-nav", { headers: { cookie: "leads_session=tok_owner", "x-tenant-id": "tnt_a" } }),
    );
    const body = (await res.json()) as { model: { renderable: boolean; status: { state: string }; brand: unknown } };
    expect(body.model.renderable).toBe(false);
    expect(body.model.status.state).toBe("unavailable");
    expect(body.model.brand).toBeNull();
    expect(calls).toHaveLength(0);
  });
});

describe("POST /api/eco-nav/launch", () => {
  it("fails closed with a config gate before any network call", async () => {
    vi.stubEnv("PLATFORM_IDENTITY_TOKEN", "");
    const { calls } = installFetch();
    const res = await postLaunch(
      new Request("http://localhost/api/eco-nav/launch", {
        method: "POST",
        headers: { "content-type": "application/json", cookie: "leads_session=tok", "x-tenant-id": "tnt_a" },
        body: JSON.stringify({ app_id: "goboost", launch_target_id: "ti-goboost-web" }),
      }),
    );
    expect(res.status).toBe(503);
    expect((await res.json()) as { error: string }).toMatchObject({ error: "config_gate" });
    expect(calls).toHaveLength(0);
  });

  it("returns the validated launch URL in JSON mode", async () => {
    installFetch();
    const res = await postLaunch(
      new Request("http://localhost/api/eco-nav/launch", {
        method: "POST",
        headers: { "content-type": "application/json", accept: "application/json", cookie: "leads_session=tok", "x-tenant-id": "tnt_a" },
        body: JSON.stringify({ app_id: "goboost", launch_target_id: "ti-goboost-web" }),
      }),
    );
    expect(res.status).toBe(200);
    expect((await res.json()) as unknown).toEqual({
      ok: true,
      app_id: "goboost",
      target_id: "ti-goboost-web",
      launch_url: "https://goboost.modelxing.com/launch?code=c&h=h",
    });
  });

  it("303-redirects a non-JSON client to the launch URL", async () => {
    installFetch();
    const res = await postLaunch(
      new Request("http://localhost/api/eco-nav/launch", {
        method: "POST",
        headers: { "content-type": "application/json", cookie: "leads_session=tok", "x-tenant-id": "tnt_a" },
        body: JSON.stringify({ app_id: "goboost", launch_target_id: "ti-goboost-web" }),
      }),
    );
    expect(res.status).toBe(303);
    expect(res.headers.get("location")).toBe("https://goboost.modelxing.com/launch?code=c&h=h");
  });

  it("drops business payload: only app + target reach identity", async () => {
    const { calls } = installFetch();
    const res = await postLaunch(
      new Request("http://localhost/api/eco-nav/launch", {
        method: "POST",
        headers: { "content-type": "application/json", accept: "application/json", cookie: "leads_session=tok", "x-tenant-id": "tnt_a" },
        body: JSON.stringify({
          app_id: "goboost",
          launch_target_id: "ti-goboost-web",
          lead_id: "lead_should_not_travel",
          payer: "personal",
          tenant_id: "tnt_other",
        }),
      }),
    );
    expect(res.status).toBe(200);
    const identityCall = calls.find((call) => call.url.includes("/internal/v1/identity/shell/launch"));
    const body = JSON.parse(String(identityCall?.init.body)) as Record<string, unknown>;
    expect(body).toEqual({
      principal_id: "usr_owner",
      tenant_id: "tnt_a",
      app_id: "goboost",
      target_id: "ti-goboost-web",
    });
    expect(JSON.stringify(body)).not.toContain("lead_should_not_travel");
    expect(JSON.stringify(body)).not.toContain("payer");
  });

  it("rejects a malformed body without calling services", async () => {
    const { calls } = installFetch();
    const res = await postLaunch(
      new Request("http://localhost/api/eco-nav/launch", {
        method: "POST",
        headers: { "content-type": "application/json", cookie: "leads_session=tok", "x-tenant-id": "tnt_a" },
        body: JSON.stringify({ app_id: "goboost" }),
      }),
    );
    expect(res.status).toBe(400);
    expect(calls).toHaveLength(0);
  });

  it("does not follow an upstream redirect or a non-allowlisted URL", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string | URL | Request) => {
        if (String(url).includes("/api/v1/whoami")) return json({ principal_ref: "usr_owner", tenant_id: "tnt_a" });
        return json({ ok: true, app_id: "goboost", target_id: "ti-goboost-web", launch_url: "https://evil.example.com/x" });
      }),
    );
    const res = await postLaunch(
      new Request("http://localhost/api/eco-nav/launch", {
        method: "POST",
        headers: { "content-type": "application/json", cookie: "leads_session=tok", "x-tenant-id": "tnt_a" },
        body: JSON.stringify({ app_id: "goboost", launch_target_id: "ti-goboost-web" }),
      }),
    );
    expect(res.status).toBe(502);
    expect((await res.json()) as { error: string }).toMatchObject({ error: "invalid_launch_url" });
  });
});
