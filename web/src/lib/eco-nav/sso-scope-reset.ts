import { CRM_TENANT_STORAGE_KEY } from "@/lib/eco-nav/crm-scope";
import { resetCrmScope } from "@/lib/eco-nav/use-crm-scope";

export const CRM_SSO_ACCOUNT_EPOCH_KEY = "leads_sso_account_epoch";

export function consumeCrmSsoReset(
  cookie: string,
  memory: Pick<Storage, "removeItem" | "setItem">,
  expire: () => void,
): boolean {
  if (!cookie.split(";").some((part) => part.trim() === "leads_sso_reset=1")) return false;
  memory.removeItem(CRM_TENANT_STORAGE_KEY);
  resetCrmScope();
  // A separate, non-secret generation changes even when no tenant was saved.
  memory.setItem(CRM_SSO_ACCOUNT_EPOCH_KEY, crypto.randomUUID());
  expire();
  return true;
}

export function resetCrmScopeAfterSso() {
  if (typeof window === "undefined") return;
  consumeCrmSsoReset(document.cookie, window.localStorage, () => {
    document.cookie = "leads_sso_reset=; Path=/; Max-Age=0; SameSite=Lax; Secure";
  });
}

export function observeCrmSsoAccountChanges(onChange: () => void): () => void {
  let observed = window.localStorage.getItem(CRM_SSO_ACCOUNT_EPOCH_KEY);
  const check = () => {
    const next = window.localStorage.getItem(CRM_SSO_ACCOUNT_EPOCH_KEY);
    if (next === observed) return;
    observed = next;
    resetCrmScope();
    onChange();
  };
  const storage = (event: StorageEvent) => {
    if (event.storageArea === window.localStorage && event.key === CRM_SSO_ACCOUNT_EPOCH_KEY) check();
  };
  window.addEventListener("storage", storage);
  window.addEventListener("focus", check);
  return () => {
    window.removeEventListener("storage", storage);
    window.removeEventListener("focus", check);
  };
}
