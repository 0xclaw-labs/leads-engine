import { expect, it, vi } from "vitest";
import { createHandoffRequest, continuationDestination } from "../src/lib/follow-up-continuation";
it("one-time h is reused only for the same h across StrictMode observations", async () => { const issue = vi.fn(async (h: string) => ({ source: h })); const acquire = createHandoffRequest(issue); const first = acquire("first-source"); expect(acquire("first-source")).toBe(first); const second = acquire("second-source"); expect(second).not.toBe(first); expect(await second).toEqual({ source: "second-source" }); expect(issue).toHaveBeenCalledTimes(2); });
it("ordinary shell SSO without business registration keeps the original homepage", () => { expect(continuationDestination({ known: false })).toBe("/"); });
