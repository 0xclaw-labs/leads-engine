import { runShellWhoami, scopeChoices, type ShellEntryFailure, type ShellWhoamiResult, type WorkScope } from "@/lib/productShell";

export type CrmSessionEntry =
  | { kind: "ready"; scopes: WorkScope[]; session: ShellWhoamiResult }
  | { kind: "choose"; scopes: WorkScope[] }
  | { kind: "failed"; failure: ShellEntryFailure };

// Discover only the current identity's authorized memberships before choosing
// a workspace. The ordinary scoped whoami still confirms the chosen tenant.
export async function resolveCrmSessionEntry(
  preferredTenant: string | null,
  fetchImpl: typeof fetch = fetch,
): Promise<CrmSessionEntry> {
  try {
    const response = await fetchImpl("/api/session/memberships", { cache: "no-store" });
    if (!response.ok) {
      return { kind: "failed", failure: response.status === 401 ? "unauthenticated" : response.status >= 500 ? "unreachable" : "no_scope" };
    }
    const body = await response.json() as { items?: unknown };
    if (!Array.isArray(body.items) || !body.items.every((item) => item && typeof item.tenant_id === "string" && item.tenant_id.trim() && typeof item.display_name === "string")) {
      return { kind: "failed", failure: "unreachable" };
    }
    const scopes = scopeChoices(body.items);
    if (scopes.length === 0) return { kind: "failed", failure: "no_scope" };
    const preferred = scopes.find((item) => item.tenant_id === preferredTenant?.trim());
    const selected = preferred ?? (scopes.length === 1 ? scopes[0] : null);
    if (!selected) return { kind: "choose", scopes };

    const session = await runShellWhoami({
      storedId: selected.tenant_id,
      membershipScopes: scopes,
      fetchImpl: async (path, init) => {
        const result = await fetchImpl(path, { ...init, cache: "no-store" });
        return { ok: result.ok, status: result.status, body: await result.json().catch(() => ({})) };
      },
    });
    if (!session.ok || session.failure || session.sessionTenant !== selected.tenant_id) {
      return { kind: "failed", failure: session.failure ?? "no_scope" };
    }
    return { kind: "ready", scopes, session };
  } catch {
    return { kind: "failed", failure: "unreachable" };
  }
}
