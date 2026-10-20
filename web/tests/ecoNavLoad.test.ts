import { describe, expect, it, vi } from "vitest";
import {
  blockedEcoNavModel,
  ecoNavFromResponse,
  fetchEcoNavModel,
  parseEcoNavView,
} from "@/lib/eco-nav/load";
import { provisionalEcoNav } from "@/lib/eco-nav/fixture";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const fixture = JSON.parse(readFileSync(join(__dirname, "eco-nav/fixtures/ready-first-party.json"), "utf8"));

describe("blockedEcoNavModel", () => {
  it("never carries brand, apps, tenants or a fake current context", () => {
    const model = blockedEcoNavModel("unavailable", "authority_unavailable", "导航目录暂不可用");
    expect(model.renderable).toBe(false);
    expect(model.brand).toBeNull();
    expect(model.apps).toEqual([]);
    expect(model.scopes).toEqual([]);
    expect(model.active_tenant_id).toBeNull();
    expect(model.status).toEqual({ state: "unavailable", reasons: ["authority_unavailable"], summary: "导航目录暂不可用" });
    expect(model.capabilities).toEqual({
      can_switch_app: false,
      can_switch_tenant: false,
      can_manage_members: false,
      can_open_billing: false,
    });
  });
});

describe("ecoNavFromResponse", () => {
  it("is unavailable when no directory is configured, not a preview fixture", () => {
    const model = ecoNavFromResponse({ configured: false, cookiePresent: true, tenant: "tnt_a", status: 0, body: null });
    expect(model.renderable).toBe(false);
    expect(model.status.state).toBe("unavailable");
    expect(model.brand).toBeNull();
    expect(model.status.summary).toContain("还没有接上");
  });

  it("is denied without a session cookie or on 401/403", () => {
    expect(ecoNavFromResponse({ configured: true, cookiePresent: false, tenant: "tnt_a", status: 200, body: fixture }).status.state).toBe("denied");
    expect(ecoNavFromResponse({ configured: true, cookiePresent: true, tenant: "tnt_a", status: 401, body: null }).status).toMatchObject({
      state: "denied",
      reasons: ["unauthenticated"],
    });
    expect(ecoNavFromResponse({ configured: true, cookiePresent: true, tenant: "tnt_a", status: 403, body: null }).status).toMatchObject({
      state: "denied",
      reasons: ["membership_denied"],
    });
  });

  it("maps service and contract failures to unavailable (never to a preview)", () => {
    for (const status of [500, 502, 503, 0]) {
      const model = ecoNavFromResponse({ configured: true, cookiePresent: true, tenant: "tnt_a", status, body: null });
      expect(model.renderable).toBe(false);
      expect(model.status.state).toBe("unavailable");
      expect(model.brand).toBeNull();
    }
    const bad = ecoNavFromResponse({ configured: true, cookiePresent: true, tenant: "tnt_a", status: 200, body: { schema_version: "eco-nav/v2" } });
    expect(bad.status.state).toBe("unavailable");
    expect(bad.status.summary).toContain("兼容");
  });

  it("maps a valid document with server provenance and the tenant", () => {
    const model = ecoNavFromResponse({ configured: true, cookiePresent: true, tenant: "tnt_a", status: 200, body: fixture });
    expect(model.renderable).toBe(true);
    expect(model.provenance).toBe("public_ai_context");
    expect(model.current_app.app_id).toBe("leads-engine");
    expect(model.active_tenant_id).toBeTruthy();
    expect(model.apps.length).toBeGreaterThan(0);
    expect(model.status.summary).toBe("");
  });

  it("keeps a blocked contract state blocked, not previewed", () => {
    const hidden = provisionallyHidden();
    const model = ecoNavFromResponse({ configured: true, cookiePresent: true, tenant: "tnt_a", status: 200, body: hidden });
    expect(model.renderable).toBe(false);
    expect(model.status.state).toBe("hidden");
  });
});

function provisionallyHidden(): unknown {
  return {
    schema_version: "eco-nav/v1",
    status: { state: "hidden", reasons: ["public_surface"] },
    brand: null,
    app: null,
    visible_apps: [],
    work_context: null,
    role: null,
    billing: null,
    return_context: null,
    capabilities: { can_switch_app: false, can_switch_tenant: false, can_manage_members: false, can_open_billing: false },
  };
}

describe("fetchEcoNavModel", () => {
  it("sends the session cookie and workspace tenant to the directory", async () => {
    const calls: Array<{ url: string; init: RequestInit }> = [];
    const fetchImpl = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      calls.push({ url: String(url), init: init ?? {} });
      return new Response(JSON.stringify(fixture), { status: 200, headers: { "content-type": "application/json" } });
    }) as unknown as typeof fetch;
    const model = await fetchEcoNavModel({
      endpoint: "http://127.0.0.1:18101/internal/v1/eco-nav",
      cookie: "leads_session=tok_owner",
      tenant: "tnt_a",
      fetchImpl,
    });
    expect(model.renderable).toBe(true);
    expect(calls).toHaveLength(1);
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.cookie).toContain("leads_session=tok_owner");
    expect(headers["x-tenant-id"]).toBe("tnt_a");
    expect(calls[0].init.method).toBe("GET");
    expect(calls[0].init.cache).toBe("no-store");
  });

  it("maps a transport failure to unavailable without throwing", async () => {
    const fetchImpl = vi.fn(async () => {
      throw new Error("ECONNREFUSED");
    }) as unknown as typeof fetch;
    const model = await fetchEcoNavModel({
      endpoint: "http://127.0.0.1:18101/internal/v1/eco-nav",
      cookie: "leads_session=tok_owner",
      tenant: "tnt_a",
      fetchImpl,
    });
    expect(model.status.state).toBe("unavailable");
    expect(model.renderable).toBe(false);
  });

  it("refuses a non-loopback directory URL (no SSRF, fail-closed)", async () => {
    const fetchImpl = vi.fn() as unknown as typeof fetch;
    const model = await fetchEcoNavModel({
      endpoint: "https://platform.example.com/internal/v1/eco-nav",
      cookie: "leads_session=tok_owner",
      tenant: "tnt_a",
      fetchImpl,
    });
    expect(model.status.state).toBe("unavailable");
    expect(fetchImpl).not.toHaveBeenCalled();
  });
});

describe("parseEcoNavView", () => {
  it("accepts a server-composed view and rejects a non-model body", () => {
    const model = ecoNavFromResponse({ configured: true, cookiePresent: true, tenant: "tnt_a", status: 200, body: fixture });
    expect(parseEcoNavView(model)).not.toBeNull();
    expect(parseEcoNavView({ ...model, schema_version: "eco-nav/v2" })).toBeNull();
    expect(parseEcoNavView(null)).toBeNull();
    expect(parseEcoNavView({ ...model, apps: "nope" })).toBeNull();
  });

  it("rejects a renderable model without a brand or current app", () => {
    const model = provisionalEcoNav();
    expect(parseEcoNavView({ ...model, brand: null })).toBeNull();
    expect(parseEcoNavView({ ...model, renderable: true, current_app: null })).toBeNull();
  });
});
