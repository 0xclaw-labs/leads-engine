import { afterEach, expect, it } from "vitest";
import { NextRequest } from "next/server";
import { middleware } from "../src/middleware";

afterEach(() => { delete process.env.SSO_APP_HOST; });

it("keeps the canonical HTTPS origin when Next sees the reverse proxy's internal URL", () => {
  const h = "hnd_" + "a".repeat(32);
  const response = middleware(new NextRequest(`http://localhost:19963/?code=original-code&h=${h}`, {
    headers: { host: "leads.modelxing.com", "x-forwarded-host": "leads.modelxing.com", "x-forwarded-proto": "https" },
  }));
  expect(response.headers.get("location")).toBe(`https://leads.modelxing.com/api/auth/sso/landing?code=original-code&h=${h}`);
  expect(response.headers.get("cache-control")).toBe("no-store");
});

it("does not redirect a code when the forwarded host disagrees with the canonical host", () => {
  const response = middleware(new NextRequest("http://localhost:19963/?code=original-code", {
    headers: { host: "leads.modelxing.com", "x-forwarded-host": "evil.example" },
  }));
  expect(response.status).toBe(400);
  expect(response.headers.get("location")).toBeNull();
});

it("rejects a malformed configured host instead of constructing an outside redirect", () => {
  process.env.SSO_APP_HOST = "leads.modelxing.com/elsewhere";
  const response = middleware(new NextRequest("http://localhost:19963/?code=original-code", {
    headers: { host: "leads.modelxing.com" },
  }));
  expect(response.status).toBe(400);
});
