import { describe, expect, it, vi } from "vitest";
import { resolveCrmSessionEntry } from "@/lib/eco-nav/session-entry";

const scopes = [
  { tenant_id: "tenant-a", display_name: "甲门店" },
  { tenant_id: "tenant-b", display_name: "乙门店" },
];

function gateway(items = scopes, whoamiStatus = 200) {
  return vi.fn(async (path: string | URL | Request, init?: RequestInit) => {
    if (path === "/api/session/memberships") return Response.json({ items });
    const tenant = new Headers(init?.headers).get("x-tenant-id");
    return Response.json(whoamiStatus === 200 ? { tenant_id: tenant, email: "current-account" } : { error: "not_member" }, { status: whoamiStatus });
  });
}

describe("CRM first entry after SSO", () => {
  it("discovers current choices without a tenant header and asks when more than one is available", async () => {
    const request = gateway();
    expect(await resolveCrmSessionEntry(null, request as typeof fetch)).toEqual({ kind: "choose", scopes });
    expect(request).toHaveBeenCalledTimes(1);
    expect(new Headers(request.mock.calls[0][1]?.headers).get("x-tenant-id")).toBeNull();
  });

  it("confirms a unique authorized workspace through the original scoped whoami", async () => {
    const request = gateway([scopes[1]]);
    const result = await resolveCrmSessionEntry(null, request as typeof fetch);
    expect(result.kind).toBe("ready");
    expect(new Headers(request.mock.calls[1][1]?.headers).get("x-tenant-id")).toBe("tenant-b");
  });

  it("does not inherit an old or forged tenant outside the current membership list", async () => {
    const request = gateway();
    expect(await resolveCrmSessionEntry("previous-account-tenant", request as typeof fetch)).toEqual({ kind: "choose", scopes });
    expect(request).toHaveBeenCalledTimes(1);
  });

  it("confirms an explicit available choice and refuses a membership revoked before whoami", async () => {
    const request = gateway(scopes, 403);
    expect(await resolveCrmSessionEntry("tenant-b", request as typeof fetch)).toEqual({ kind: "failed", failure: "no_scope" });
    expect(new Headers(request.mock.calls[1][1]?.headers).get("x-tenant-id")).toBe("tenant-b");
  });

  it("keeps unavailability distinct from a real empty membership list", async () => {
    const unavailable = vi.fn(async () => Response.json({ error: "identity_unavailable" }, { status: 503 }));
    expect(await resolveCrmSessionEntry(null, unavailable as typeof fetch)).toEqual({ kind: "failed", failure: "unreachable" });
    expect(await resolveCrmSessionEntry(null, gateway([]) as typeof fetch)).toEqual({ kind: "failed", failure: "no_scope" });
  });
});
