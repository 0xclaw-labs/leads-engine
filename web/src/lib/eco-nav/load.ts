// eco-nav/v1 消费端加载：服务端取目录，前端只渲染已校验的视图。
//
// HUI-2227 / 2026-10-21：旧的兜底是「没有配置就回预览 fixture」。那会把
// 预览租户/应用画成真实上下文，正常路径失败被藏起来。现在失败一律显式
// blocked（hidden/denied/unavailable），fixture 只存在于组件单测。
import {
  ECO_NAV_SCHEMA_VERSION,
  parseEcoNavDocument,
  pinLeadsApp,
  toViewModel,
  type BlockedModel,
  type EcoNavDocument,
  type EcoNavModel,
  type EcoNavProvenance,
  type NavState,
  type StatusReason,
} from "@/lib/eco-nav/model";

export interface LoadEcoNavInput {
  endpoint: string | null;
  cookie: string | null;
  tenant: string | null;
  fetchImpl?: typeof fetch;
}

export interface EcoNavResponseInput {
  configured: boolean;
  cookiePresent: boolean;
  tenant: string | null;
  status: number;
  body: unknown;
}

const LOOPBACK_HOSTS = new Set(["127.0.0.1", "::1", "localhost"]);

export function isLoopbackUrl(raw: string): boolean {
  try {
    const url = new URL(raw);
    return (url.protocol === "http:" || url.protocol === "https:") && LOOPBACK_HOSTS.has(url.hostname);
  } catch {
    return false;
  }
}

// 服务端产出的视图只做形状守卫；契约级的字段/枚举校验已在加载时完成。
export function parseEcoNavView(value: unknown): EcoNavModel | null {
  if (typeof value !== "object" || value === null) return null;
  const model = value as Partial<EcoNavModel>;
  if (model.schema_version !== ECO_NAV_SCHEMA_VERSION) return null;
  if (typeof model.renderable !== "boolean") return null;
  if (!Array.isArray(model.apps) || !Array.isArray(model.scopes)) return null;
  if (!model.status || typeof model.status.state !== "string" || !Array.isArray(model.status.reasons)) return null;
  if (!model.capabilities || typeof model.capabilities.can_switch_app !== "boolean") return null;
  if (model.renderable) {
    if (!model.brand || !model.current_app || !model.current_app.app_id) return null;
  }
  return model as EcoNavModel;
}

export function blockedEcoNavModel(state: NavState, reason: StatusReason, summary: string): EcoNavModel {
  const blockedState: "hidden" | "denied" | "unavailable" =
    state === "hidden" || state === "denied" || state === "unavailable" ? state : "unavailable";
  const document: BlockedModel = {
    schema_version: ECO_NAV_SCHEMA_VERSION,
    status: { state: blockedState, reasons: [reason] },
    brand: null,
    app: null,
    visible_apps: [],
    work_context: null,
    role: null,
    billing: null,
    return_context: null,
    capabilities: {
      can_switch_app: false,
      can_switch_tenant: false,
      can_manage_members: false,
      can_open_billing: false,
    },
  };
  return pinLeadsApp(
    toViewModel(document, { provenance: "server_blocked", statusSummary: summary }),
  );
}

export function ecoNavFromResponse(input: EcoNavResponseInput): EcoNavModel {
  if (!input.configured) {
    return blockedEcoNavModel(
      "unavailable",
      "authority_unavailable",
      "还没有接上生态导航，应用切换暂未启用。",
    );
  }
  if (!input.cookiePresent) {
    return blockedEcoNavModel("denied", "unauthenticated", "未登录，生态导航不渲染。");
  }
  if (input.status === 401) {
    return blockedEcoNavModel("denied", "unauthenticated", "登录已过期，生态导航暂不渲染。");
  }
  if (input.status === 403) {
    return blockedEcoNavModel("denied", "membership_denied", "当前身份在这个应用下没有可用成员关系。");
  }
  if (input.status < 200 || input.status >= 300) {
    return blockedEcoNavModel("unavailable", "authority_unavailable", "生态导航服务暂不可用，应用切换暂未启用。");
  }
  const parsed = parseEcoNavDocument(input.body);
  if (!parsed.ok) {
    return blockedEcoNavModel("unavailable", "authority_unavailable", "生态导航暂时无法兼容，已停止渲染。");
  }
  if (!input.tenant) {
    // 目录返回了模型但没有工作范围：仍是不可渲染，不猜一个租户。
    return blockedEcoNavModel("unavailable", "authority_unavailable", "先选择工作范围，生态导航才会出现。");
  }
  const summary =
    parsed.document.status.state === "degraded" ? "部分能力未知，按服务端状态展示；不会补默认值。" : "";
  return pinLeadsApp(
    toViewModel(parsed.document, { provenance: "public_ai_context" as EcoNavProvenance, statusSummary: summary }),
  );
}

export async function fetchEcoNavModel(input: LoadEcoNavInput): Promise<EcoNavModel> {
  const endpoint = (input.endpoint ?? "").trim();
  const cookie = (input.cookie ?? "").trim();
  const tenant = (input.tenant ?? "").trim();
  const configured = endpoint.length > 0;
  if (!configured) {
    return ecoNavFromResponse({ configured, cookiePresent: cookie.length > 0, tenant, status: 0, body: null });
  }
  if (!cookie) {
    return ecoNavFromResponse({ configured, cookiePresent: false, tenant, status: 0, body: null });
  }
  if (!isLoopbackUrl(endpoint)) {
    return blockedEcoNavModel(
      "unavailable",
      "authority_unavailable",
      "生态导航目录地址不符合本机服务纪律，已停止请求。",
    );
  }
  const fetchImpl = input.fetchImpl ?? fetch;
  let response: Response;
  try {
    const headers: Record<string, string> = { accept: "application/json", cookie };
    if (tenant) headers["x-tenant-id"] = tenant;
    response = await fetchImpl(endpoint, {
      method: "GET",
      headers,
      cache: "no-store",
      redirect: "manual",
    });
  } catch {
    return ecoNavFromResponse({ configured, cookiePresent: true, tenant, status: 0, body: null });
  }
  let body: unknown = null;
  try {
    body = await response.json();
  } catch {
    body = null;
  }
  return ecoNavFromResponse({ configured, cookiePresent: true, tenant, status: response.status, body });
}

export function provenanceLabel(model: EcoNavModel): string {
  return model.provenance === "public_ai_context" ? "服务端上下文" : "";
}
