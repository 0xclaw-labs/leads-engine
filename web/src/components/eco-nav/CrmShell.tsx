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
import { commitCrmTenant, hydrateCrmTenantFromStorage, releaseCrmTenant, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { failureText, guardTenantCommit, rendererDensity, runShellWhoami, scopeChoices, type WorkScope } from "@/lib/productShell";
import { RendererProvider } from "@/vendor/painuo/react/v1/src/index";

function DeskFrame({
  scopes,
  children,
}: {
  scopes: WorkScope[];
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
        onSwitch={(tenant) => {
          const chosen = guardTenantCommit(scopes, tenant);
          if (chosen) commitCrmTenant(chosen);
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
  const [scopeFault, setScopeFault] = useState("");
  const [switcher, setSwitcher] = useState<WorkScope[]>([]);
  const [navModel, setNavModel] = useState<EcoNavModel | null>(model ?? null);
  const [navFault, setNavFault] = useState("");
  const [launchError, setLaunchError] = useState("");
  const [launchBusy, setLaunchBusy] = useState(false);
  const [navReload, setNavReload] = useState(0);
  const navTicket = useRef({ seq: 0, tenant: "" });

  useEffect(() => {
    hydrateCrmTenantFromStorage();
    setSwitcher([]);
    let cancelled = false;
    const stored = (window.localStorage.getItem(CRM_TENANT_STORAGE_KEY) ?? "").trim() || null;
    void runShellWhoami({
      storedId: stored,
      membershipScopes: scopeChoices(model?.scopes ?? []),
    }).then((result) => {
      if (cancelled) return;
      setAuthenticated(result.ok);
      setNickname(result.email || "账户");
      setSwitcher(result.switcher);
      if (!result.memoryTenantId) {
        setScopeFault(result.fault);
        releaseCrmTenant();
        return;
      }
      setScopeFault("");
      if (result.persist && result.storage) commitCrmTenant(result.storage);
    });
    return () => {
      cancelled = true;
    };
    // 只在挂载时对账一次；租户切换由 scope.tenant 变化驱动导航重载。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadNav = useCallback(async (tenant: string) => {
    const ticket = { seq: navTicket.current.seq + 1, tenant };
    navTicket.current = ticket;
    const headers: Record<string, string> = { accept: "application/json", "x-tenant-id": tenant };
    try {
      const res = await fetch("/api/eco-nav", { headers, cache: "no-store" });
      const body = (await res.json().catch(() => ({}))) as { model?: unknown; message?: string };
      if (navTicket.current !== ticket) return;
      const parsed = parseEcoNavView(body.model);
      if (!parsed) {
        setNavModel(blockedEcoNavModel("unavailable", "authority_unavailable", "生态导航契约不兼容，已停止渲染。"));
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
      if (navTicket.current !== ticket) return;
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
        if (!res.ok || typeof body.launch_url !== "string") {
          setLaunchError(failureText(body, res.status));
          return;
        }
        window.location.assign(body.launch_url);
      } catch {
        setLaunchError("切换请求没有完成；目标应用没有打开，可以重试。");
      } finally {
        setLaunchBusy(false);
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
              if (chosen) commitCrmTenant(chosen);
            }}
          />
        ) : null}
        {show && navFault && navModelFiltered?.status.state === "unavailable" ? (
          <div className="eco-nav-fault" role="status" data-eco-nav-fault="unavailable">
            <span>生态导航暂不可用</span>
            <span className="muted">{navFault}</span>
            <button type="button" className="btn" onClick={() => setNavReload((n) => n + 1)}>
              重试导航
            </button>
          </div>
        ) : null}
        <DeskFrame scopes={switcher}>
          {scopeFault ? <SurfaceState kind="error" title="工作范围不可用" detail={scopeFault} /> : children}
        </DeskFrame>
      </div>
    </ShellWidthProvider>
  );
}
