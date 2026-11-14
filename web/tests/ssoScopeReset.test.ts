import { afterEach, describe, expect, it, vi } from "vitest";
import { CRM_TENANT_STORAGE_KEY } from "@/lib/eco-nav/crm-scope";
import { consumeCrmSsoReset, CRM_SSO_ACCOUNT_EPOCH_KEY } from "@/lib/eco-nav/sso-scope-reset";
import { commitCrmTenant, currentCrmScopeEpoch, resetCrmScope } from "@/lib/eco-nav/use-crm-scope";

afterEach(() => {
  vi.unstubAllGlobals();
  resetCrmScope();
});

function memory() {
  const saved = new Map<string, string>();
  return {
    getItem: (key: string) => saved.get(key) ?? null,
    setItem: (key: string, value: string) => { saved.set(key, value); },
    removeItem: (key: string) => { saved.delete(key); },
  };
}

describe("SSO account scope reset", () => {
  it("drops the previous account's tenant and invalidates its pending reconciliation", () => {
    const storage = memory();
    vi.stubGlobal("window", { localStorage: storage });
    commitCrmTenant("old-account-tenant");
    const pendingEpoch = currentCrmScopeEpoch();
    const expire = vi.fn();
    expect(consumeCrmSsoReset("another=1; leads_sso_reset=1", storage, expire)).toBe(true);
    expect(storage.getItem(CRM_TENANT_STORAGE_KEY)).toBeNull();
    expect(currentCrmScopeEpoch()).toBeGreaterThan(pendingEpoch);
    expect(storage.getItem(CRM_SSO_ACCOUNT_EPOCH_KEY)).toBeTruthy();
    expect(expire).toHaveBeenCalledOnce();
  });

  it("broadcasts a new account generation even when there was no saved tenant", () => {
    const storage = memory();
    consumeCrmSsoReset("leads_sso_reset=1", storage, () => undefined);
    const first = storage.getItem(CRM_SSO_ACCOUNT_EPOCH_KEY);
    consumeCrmSsoReset("leads_sso_reset=1", storage, () => undefined);
    expect(storage.getItem(CRM_SSO_ACCOUNT_EPOCH_KEY)).not.toBe(first);
    expect(storage.getItem(CRM_TENANT_STORAGE_KEY)).toBeNull();
  });

  it("preserves the server-verified continuation tenant after consuming the reset once", () => {
    const storage = memory();
    vi.stubGlobal("window", { localStorage: storage });
    let cookie = "leads_sso_reset=1";
    consumeCrmSsoReset(cookie, storage, () => { cookie = ""; });
    commitCrmTenant("verified-crm-tenant");
    const verifiedEpoch = currentCrmScopeEpoch();
    expect(consumeCrmSsoReset(cookie, storage, () => { cookie = ""; })).toBe(false);
    expect(storage.getItem(CRM_TENANT_STORAGE_KEY)).toBe("verified-crm-tenant");
    expect(currentCrmScopeEpoch()).toBe(verifiedEpoch);
  });

  it("keeps an ordinary saved choice when no successful SSO reset is present", () => {
    const storage = memory();
    storage.setItem(CRM_TENANT_STORAGE_KEY, "chosen-tenant");
    const epoch = currentCrmScopeEpoch();
    const expire = vi.fn();
    expect(consumeCrmSsoReset("not_leads_sso_reset=1; leads_sso_reset=0", storage, expire)).toBe(false);
    expect(storage.getItem(CRM_TENANT_STORAGE_KEY)).toBe("chosen-tenant");
    expect(currentCrmScopeEpoch()).toBe(epoch);
    expect(expire).not.toHaveBeenCalled();
  });
});
