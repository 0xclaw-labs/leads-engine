import { LEADS_APP_HOST } from "@/lib/sso-host";

export const AUTH_PORTAL_ORIGIN = "https://auth.modelxing.com";
export const AUTH_ACCOUNT_CENTER_URL = `${AUTH_PORTAL_ORIGIN}/`;
export const LEADS_SSO_APP_ID = "leads-engine";

export const SSO_ERROR_REASONS = [
  "audience_mismatch",
  "invalid_request",
  "identity_unavailable",
  "membership_required",
  "failed",
] as const;

export type SsoErrorReason = (typeof SSO_ERROR_REASONS)[number];

const SSO_ERROR_SET: ReadonlySet<string> = new Set(SSO_ERROR_REASONS);

/** 只接受 exchange 桥约定的白名单原因码，忽略任意未知或恶意输入。 */
export function parseSsoErrorReason(raw: string | null | undefined): SsoErrorReason | null {
  if (!raw) return null;
  const trimmed = raw.trim();
  return SSO_ERROR_SET.has(trimmed) ? (trimmed as SsoErrorReason) : null;
}

/** 构造安全的本站回跳地址（剔除一次性 code 与历史 sso_error 参数；非本站 canonical host 回落首页）。 */
export function buildLeadsReturnUrl(currentHref?: string | null, canonicalHost = LEADS_APP_HOST): string {
  const host = canonicalHost.trim().toLowerCase() || LEADS_APP_HOST;
  const fallback = `https://${host}/`;
  if (!currentHref) return fallback;
  try {
    const parsed = new URL(currentHref);
    const bare = parsed.hostname.toLowerCase();
    if (parsed.protocol !== "https:" || (bare !== host && bare !== `www.${host}`)) {
      return fallback;
    }
    parsed.searchParams.delete("sso_error");
    parsed.searchParams.delete("sso");
    parsed.searchParams.delete("code");
    const search = parsed.searchParams.toString();
    return `https://${host}${parsed.pathname || "/"}${search ? `?${search}` : ""}`;
  } catch {
    return fallback;
  }
}

/** 构造跳往 modelxing.com 统一登录门户的链接；switchAccount=true 时附加 prompt=login。 */
export function buildPortalLoginUrl(options?: {
  next?: string | null;
  switchAccount?: boolean;
  canonicalHost?: string;
}): string {
  const url = new URL("/login", AUTH_PORTAL_ORIGIN);
  url.searchParams.set("app", LEADS_SSO_APP_ID);
  if (options?.switchAccount) {
    url.searchParams.set("prompt", "login");
  }
  url.searchParams.set("next", buildLeadsReturnUrl(options?.next, options?.canonicalHost));
  return url.toString();
}
