"use client";

// HUI-2227：CRM 壳的生态导航。
//
// 会话/租户先由 whoami 对账；导航模型随后经本站 BFF（/api/eco-nav）由服务端
// 目录产出。缺配置、未登录、服务不可达都不回退预览 fixture：宁可不渲染导航，
// 也不把预览租户画成真实上下文。
import React, { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { EcoTopNav } from "@/components/eco-nav/EcoTopNav";
import { ShellWidthProvider, SurfaceState, WorkbenchChrome, useShellWidth } from "@/components/workbench/chrome";
import { OfflineReady } from "@/components/workbench/offlineReady";
import { CRM_TENANT_STORAGE_KEY } from "@/lib/eco-nav/crm-scope";
import { blockedEcoNavModel, parseEcoNavView } from "@/lib/eco-nav/load";
import type { EcoNavModel } from "@/lib/eco-nav/model";
import { shouldMountEcoTopNav } from "@/lib/eco-nav/mount";
import { commitCrmTenant, currentCrmScopeEpoch, releaseCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { observeCrmSsoAccountChanges, resetCrmScopeAfterSso } from "@/lib/eco-nav/sso-scope-reset";
import { resolveCrmSessionEntry } from "@/lib/eco-nav/session-entry";
import {
  SCOPE_UNAVAILABLE,
  failureText,
  guardTenantCommit,
  rendererDensity,
  scopeChoices,
  type ShellEntryFailure,
  type WorkScope,
} from "@/lib/productShell";
import {
  AUTH_ACCOUNT_CENTER_URL,
  buildLeadsReturnUrl,
  buildPortalLoginUrl,
  parseSsoErrorReason,
  type SsoErrorReason,
} from "@/lib/sso-entry";
import { Button } from "@/components/ui/button";
import { RendererProvider } from "@/vendor/painuo/react/v1/src/index";

const SSO_LOGIN_ERROR_DETAIL: Record<Exclude<SsoErrorReason, "membership_required">, string> = {
  identity_unavailable: "统一身份服务暂时不可用，未能完成登录，请稍后重新通过统一账号登录。",
  audience_mismatch: "登录请求来源校验未通过，请重新通过 modelxing.com 统一账号登录。",
  invalid_request: "一次性登录凭证无效或已过期，请重新通过 modelxing.com 统一账号登录。",
  failed: "统一账号登录未能完成，请重新通过 modelxing.com 统一账号登录。",
};

// 产品门口友好准入引导：未登录给一键跳统一登录门户、无团队席位给切换账号与账号中心、
// 服务不可达给可重试错误态；三种状态都提供真实可执行动作，不再停在死文字。
function entryStateCopy(
  failure: ShellEntryFailure,
  ssoError?: SsoErrorReason | null,
): { kind: "recovery" | "error"; title: string; detail: string } {
  if (failure === "no_scope" || ssoError === "membership_required") {
    return {
      kind: "recovery",
      title: "当前账号尚未开通数海获客席位",
      detail: `当前账号尚未加入已开通数海获客的团队。${SCOPE_UNAVAILABLE}请联系主管理员确认成员关系后重试，或切换其他已开通账号。`,
    };
  }
  if (failure === "unauthenticated") {
    return {
      kind: "recovery",
      title: "请先登录后进入数海获客",
      detail: ssoError
        ? SSO_LOGIN_ERROR_DETAIL[ssoError]
        : "请使用 modelxing.com 统一账号登录后进入工作台。",
    };
  }
  return {
    kind: "error",
    title: "服务暂时没有响应",
    detail: "请检查网络后重试。",
  };
}

export function CrmEntryState({
  failure,
  ssoError,
  returnUrl,
  onRetry,
}: {
  failure: ShellEntryFailure;
  ssoError?: SsoErrorReason | null;
  returnUrl?: string;
  onRetry: () => void;
}) {
  const effectiveFailure: ShellEntryFailure = ssoError === "membership_required" ? "no_scope" : failure;
  const copy = entryStateCopy(effectiveFailure, ssoError);
  const loginUrl = buildPortalLoginUrl({ next: returnUrl });
  const switchAccountUrl = buildPortalLoginUrl({ next: returnUrl, switchAccount: true });

  const action =
    effectiveFailure === "unauthenticated" ? (
      <div className="flex flex-wrap items-center gap-2">
        <Button asChild size="sm">
          <a href={loginUrl} data-testid="crm-sso-login">
            通过 modelxing.com 统一账号登录
          </a>
        </Button>
        <Button type="button" variant="outline" size="sm" onClick={onRetry}>
          重试
        </Button>
      </div>
    ) : effectiveFailure === "no_scope" ? (
      <div className="flex flex-wrap items-center gap-2">
        <Button asChild size="sm">
          <a href={switchAccountUrl} data-testid="crm-sso-switch-account">
            切换其他账号
          </a>
        </Button>
        <Button asChild variant="outline" size="sm">
          <a href={AUTH_ACCOUNT_CENTER_URL} data-testid="crm-sso-account-center">
            前往账号中心
          </a>
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onRetry}>
          重试
        </Button>
      </div>
    ) : (
      <Button type="button" size="sm" onClick={onRetry}>
        重试
      </Button>
    );

  return (
    <SurfaceState
      kind={copy.kind}
      title={copy.title}
      detail={copy.detail}
      action={action}
    />
  );
}

function DeskFrame({
  scopes,
  scopeEmptyLabel,
  onScopeSwitch,
  children,
}: {
  scopes: WorkScope[];
  /** 未确认任何范围时范围条的文案；未登录时由壳传入“未登录”。 */
  scopeEmptyLabel?: string;
  onScopeSwitch: (tenant: string) => void;
  children: ReactNode;
}) {
  const width = useShellWidth();
  const scope = useCrmScope();
  return (
    // HUI-2626（DECISIONS.md D2-2）：壳级 renderer scope——工作台子树统一消费
    // painuo token（profile-scopes.json leads-web/work.light/light 合法组合），
    // 并为 Drawer/Dialog 等原语提供 portal root。attribution 页级试点 provider 保留不动。
    <RendererProvider
      profile="leads-web"
      surface="work.light"
      theme="light"
      // HUI-2626：≤430 走 renderer 原生 touch 密度（控件高度 48px），桌面 default。
      density={rendererDensity(width)}
    >
      <WorkbenchChrome
        width={width}
        scopes={scopes}
        activeId={scope.tenant}
        scopeEmptyLabel={scopeEmptyLabel}
        onSwitch={(tenant) => {
          const chosen = guardTenantCommit(scopes, tenant);
          if (chosen) onScopeSwitch(chosen);
        }}
      >
        {children}
      </WorkbenchChrome>
    </RendererProvider>
  );
}

export function CrmShell({
  model,
  children,
}: {
  /** 仅组件测试/SSR 预览注入；运行时由 /api/eco-nav 服务端产出。 */
  model?: EcoNavModel;
  children: ReactNode;
}) {
  const scope = useCrmScope();
  const [authenticated, setAuthenticated] = useState(false);
  const [nickname, setNickname] = useState("账户");
  const [entryFailure, setEntryFailure] = useState<ShellEntryFailure | null>(null);
  const [ssoError, setSsoError] = useState<SsoErrorReason | null>(null);
  const [returnUrl, setReturnUrl] = useState<string | undefined>(undefined);
  const [entryPending, setEntryPending] = useState(true);
  const [entryChoosing, setEntryChoosing] = useState(false);
  const [switcher, setSwitcher] = useState<WorkScope[]>([]);
  const [navModel, setNavModel] = useState<EcoNavModel | null>(model ?? null);
  const [navFault, setNavFault] = useState("");
  const [launchError, setLaunchError] = useState("");
  const [launchBusy, setLaunchBusy] = useState(false);
  const [navReload, setNavReload] = useState(0);
  const navTicket = useRef({ seq: 0, tenant: "" });
  // 会话对账票据：重试与在途请求竞态时，只采纳最新一次的结果。
  const sessionTicket = useRef(0);

  const clearSsoErrorQuery = useCallback(() => {
    if (typeof window === "undefined") return;
    const url = new URL(window.location.href);
    if (!url.searchParams.has("sso_error")) return;
    url.searchParams.delete("sso_error");
    const next = `${url.pathname}${url.search}${url.hash}`;
    window.history.replaceState(window.history.state, "", next);
  }, []);

  // 挂载与「重试」共用同一套对账流程。用票据替代 effect 里的 cancelled 标记，
  // 这样按钮触发的重跑能作废仍在途的上一次结果，不出现旧答案覆盖新答案。
  const reconcileSession = useCallback((selectedTenant?: string, options?: { clearSsoError?: boolean }) => {
    const ticket = sessionTicket.current + 1;
    sessionTicket.current = ticket;
    resetCrmScopeAfterSso();
    if (options?.clearSsoError) clearSsoErrorQuery();
    const urlSsoError = options?.clearSsoError
      ? null
      : parseSsoErrorReason(new URLSearchParams(window.location.search).get("sso_error"));
    setSsoError(urlSsoError);
    setReturnUrl(buildLeadsReturnUrl(window.location.href));
    const scopeEpoch = currentCrmScopeEpoch();
    navTicket.current = { seq: navTicket.current.seq + 1, tenant: "" };
    setAuthenticated(false);
    setEntryPending(true);
    setEntryChoosing(false);
    setEntryFailure(null);
    setSwitcher([]);
    setNavModel(model ?? null);
    setNavFault("");
    setLaunchBusy(false);
    setLaunchError("");
    const stored = selectedTenant ?? ((window.localStorage.getItem(CRM_TENANT_STORAGE_KEY) ?? "").trim() || null);
    if (urlSsoError === "membership_required") {
      setEntryPending(false);
      setEntryFailure("no_scope");
      releaseCrmTenant();
      return;
    }
    void resolveCrmSessionEntry(stored).then((result) => {
      if (sessionTicket.current !== ticket || currentCrmScopeEpoch() !== scopeEpoch) return;
      setEntryPending(false);
      if (result.kind === "failed") {
        setEntryFailure(result.failure);
        releaseCrmTenant();
        return;
      }
      if (urlSsoError) {
        setSsoError(null);
        clearSsoErrorQuery();
      }
      setAuthenticated(true);
      setSwitcher(result.scopes);
      if (result.kind === "choose") {
        releaseCrmTenant();
        setEntryChoosing(true);
        return;
      }
      setNickname(result.session.email || "账户");
      commitCrmTenant(result.session.memoryTenantId!);
    });
  }, [clearSsoErrorQuery, model]);

  useEffect(() => {
    reconcileSession();
    const stop = observeCrmSsoAccountChanges(() => reconcileSession());
    return () => {
      stop();
      sessionTicket.current += 1;
      navTicket.current = { seq: navTicket.current.seq + 1, tenant: "" };
    };
    // 只在挂载时对账一次；租户切换由 scope.tenant 变化驱动导航重载。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadNav = useCallback(async (tenant: string) => {
    const ticket = { seq: navTicket.current.seq + 1, tenant };
    navTicket.current = ticket;
    const scopeEpoch = currentCrmScopeEpoch();
    const headers: Record<string, string> = { accept: "application/json", "x-tenant-id": tenant };
    try {
      const res = await fetch("/api/eco-nav", { headers, cache: "no-store" });
      const body = (await res.json().catch(() => ({}))) as { model?: unknown; message?: string };
      if (navTicket.current !== ticket || currentCrmScopeEpoch() !== scopeEpoch) return;
      const parsed = parseEcoNavView(body.model);
      if (!parsed) {
        setNavModel(blockedEcoNavModel("unavailable", "authority_unavailable", "导航数据不兼容，已停止渲染。"));
        setNavFault("生态导航没有载入。");
        return;
      }
      setNavModel(parsed);
      // 导航目录的 membership scopes 是服务端事实；工作范围切换列表以它为准。
      if (parsed.renderable && parsed.scopes.length > 0) {
        const scopes = scopeChoices(parsed.scopes);
        if (scopes.length > 0) setSwitcher(scopes);
      }
      setNavFault(parsed.renderable || parsed.status.state === "hidden" || parsed.status.state === "denied" ? "" : parsed.status.summary || "生态导航暂不可用");
    } catch {
      if (navTicket.current !== ticket || currentCrmScopeEpoch() !== scopeEpoch) return;
      setNavModel(blockedEcoNavModel("unavailable", "authority_unavailable", "生态导航服务暂不可达。"));
      setNavFault("生态导航服务暂不可达");
    }
  }, []);

  useEffect(() => {
    if (!authenticated || !scope.tenant) return;
    void loadNav(scope.tenant);
  }, [authenticated, scope.tenant, navReload, loadNav]);

  const show = shouldMountEcoTopNav({ surface: "crm", authenticated });
  const allowed = new Set(switcher.map((item) => item.tenant_id));
  const navModelFiltered: EcoNavModel | null = navModel
    ? { ...navModel, scopes: navModel.scopes.filter((item) => allowed.has(item.tenant_id)) }
    : null;

  const launch = useCallback(
    async (appId: string, targetId: string) => {
      const tenant = scope.tenant ?? "";
      const scopeEpoch = currentCrmScopeEpoch();
      if (!tenant) {
        setLaunchError("先在顶部选择工作范围。");
        return;
      }
      setLaunchBusy(true);
      setLaunchError("");
      try {
        const res = await fetch("/api/eco-nav/launch", {
          method: "POST",
          headers: {
            "content-type": "application/json",
            accept: "application/json",
            "x-tenant-id": tenant,
          },
          body: JSON.stringify({ app_id: appId, launch_target_id: targetId }),
        });
        const body = (await res.json().catch(() => ({}))) as { launch_url?: unknown; message?: unknown };
        if (currentCrmScopeEpoch() !== scopeEpoch) return;
        if (!res.ok || typeof body.launch_url !== "string") {
          setLaunchError(failureText(body, res.status));
          return;
        }
        window.location.assign(body.launch_url);
      } catch {
        if (currentCrmScopeEpoch() === scopeEpoch) setLaunchError("切换请求没有完成；目标应用没有打开，可以重试。");
      } finally {
        if (currentCrmScopeEpoch() === scopeEpoch) setLaunchBusy(false);
      }
    },
    [scope.tenant],
  );

  return (
    <ShellWidthProvider>
      {/* HUI-2626 fix2（gate-r2 #8）：断网导航兜底为应用内离线页。 */}
      <OfflineReady />
      <div className="desk-app" data-shell="sales">
        {show && navModelFiltered && navModelFiltered.renderable ? (
          <EcoTopNav
            model={navModelFiltered}
            nickname={nickname}
            launchError={launchError}
            launchBusy={launchBusy}
            onLaunchApp={(appId, targetId) => void launch(appId, targetId)}
            onReturn={(sourceAppId, targetId) => void launch(sourceAppId, targetId)}
            onTenantSwitch={(tenant) => {
              const chosen = guardTenantCommit(switcher, tenant);
              if (chosen) reconcileSession(chosen);
            }}
          />
        ) : null}
        {show && navFault && navModelFiltered?.status.state === "unavailable" ? (
          <div className="eco-nav-fault" role="status" data-eco-nav-fault="unavailable">
            <span>生态导航暂不可用</span>
            <span className="muted">{navFault}</span>
            <Button type="button" variant="secondary" size="sm" onClick={() => setNavReload((n) => n + 1)}>
              重试导航
            </Button>
          </div>
        ) : null}
        <DeskFrame scopes={switcher} scopeEmptyLabel={entryFailure === "unauthenticated" ? "未登录" : undefined} onScopeSwitch={(tenant) => reconcileSession(tenant)}>
          {entryPending ? <SurfaceState kind="loading" title="正在核验工作范围" />
            : entryFailure ? (
              <CrmEntryState
                failure={entryFailure}
                ssoError={ssoError}
                returnUrl={returnUrl}
                onRetry={() => reconcileSession(undefined, { clearSsoError: true })}
              />
            )
            : entryChoosing ? <SurfaceState kind="recovery" title="请选择工作范围" detail="你可以进入多个工作范围，请在顶部选择本次要处理的客户。" />
            : children}
        </DeskFrame>
      </div>
    </ShellWidthProvider>
  );
}
