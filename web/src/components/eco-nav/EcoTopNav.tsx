"use client";

// 默认导入供 vitest/esbuild 的 classic JSX 转换使用（Next SWC automatic runtime 不受影响）。
import Link from "next/link";
import React, { useEffect, useState } from "react";
import { navbarCrmAffordances } from "@/lib/eco-nav/affordances";
import {
  billingBadgeText,
  partitionApps,
  pinLeadsApp,
  selectTenant,
  switchableApps,
  type EcoNavModel,
  type VisibleApp,
} from "@/lib/eco-nav/model";
import styles from "./eco-top-nav.module.css";

function appBlockedLabel(app: VisibleApp): string {
  if (app.state === "entitlement_required") return "需开通权益";
  if (app.state === "temporarily_unavailable") return "权益暂不可用";
  return "";
}

// 应用切换只传 (app_id, launch_target_id)；BFF 负责会话与 launch 回执。
// current 不作为按钮；blocked 显示原因；launchable 才可点。
function AppControl({
  app,
  canSwitch,
  busy,
  onLaunch,
}: {
  app: VisibleApp;
  canSwitch: boolean;
  busy: boolean;
  onLaunch?: (appId: string, targetId: string) => void;
}) {
  const blocked = appBlockedLabel(app);
  const launchable = app.state === "launchable" && canSwitch && Boolean(app.launch_target_id) && Boolean(onLaunch);
  const dataset = {
    "data-testid": `eco-app-${app.app_id}`,
    "data-nav-intent": "manual_switch",
    "data-creates-handoff": "false",
    "data-eco-app-state": app.state,
    "data-launch-target-id": app.launch_target_id ?? "",
  };
  if (launchable) {
    return (
      <button
        type="button"
        className={styles.button}
        {...dataset}
        disabled={busy}
        title={`切换到 ${app.display_name}（只带应用与登记目标，不携带客户数据）`}
        onClick={() => onLaunch?.(app.app_id, app.launch_target_id)}
      >
        {app.display_name}
      </button>
    );
  }
  return (
    <span
      className={styles.quiet}
      {...dataset}
      aria-disabled={app.state !== "current" ? true : undefined}
      title={
        blocked ||
        (app.state === "launchable" && !canSwitch
          ? "本次会话没有应用切换能力"
          : app.state === "launchable"
            ? "目标应用还没有可用的启动地址"
            : "")
      }
    >
      {app.display_name}
      {blocked ? <em className={styles.reason}>{blocked}</em> : null}
    </span>
  );
}

export function EcoTopNav({
  model,
  nickname,
  onTenantSwitch,
  onLaunchApp,
  onReturn,
  launchError,
  launchBusy,
}: {
  model: EcoNavModel;
  nickname: string;
  onTenantSwitch: (tenant: string) => void;
  onLaunchApp?: (appId: string, targetId: string) => void;
  onReturn?: (sourceAppId: string, targetId: string) => void;
  launchError?: string;
  launchBusy?: boolean;
}) {
  const [nav, setNav] = useState(() => pinLeadsApp(model));
  const [menuOpen, setMenuOpen] = useState(false);

  useEffect(() => {
    setNav(pinLeadsApp(model));
    setMenuOpen(false);
  }, [model]);

  if (!nav.renderable || !nav.brand) return null;

  const affordances = navbarCrmAffordances(nav);
  const apps = switchableApps(nav.apps, nav.current_app.app_id);
  const parts = partitionApps(apps, "wide");
  const canSwitch = nav.capabilities.can_switch_app === true;
  const roleText = nav.provenance === "public_ai_context" ? nav.role_label || "角色需确认" : "角色需确认";
  const back = nav.return_context;

  return (
    <header
      className={styles.bar}
      aria-label="生态导航"
      data-testid="eco-top-nav"
      data-app-id={nav.current_app.app_id}
      data-eco-status={nav.status.state}
      data-pn-surface="work.light"
      data-pn-theme="light"
    >
      <div className={styles.brand}>
        <span className={styles.brandName} title={nav.brand.display_name}>
          {nav.brand.display_name}
        </span>
        <span className={styles.currentApp} data-testid="eco-current-app" data-app-id={nav.current_app.app_id} aria-current="page">
          {nav.current_app.display_name}
        </span>
      </div>
      <div className={styles.apps}>
        <div className={styles.pinned}>
          {parts.pinned.map((app) => (
            <AppControl key={app.app_id} app={app} canSwitch={canSwitch} busy={launchBusy === true} onLaunch={onLaunchApp} />
          ))}
        </div>
        {parts.overflow.length > 0 ? (
          <div className={styles.popover}>
            <button type="button" className={styles.button} aria-expanded={menuOpen} onClick={() => setMenuOpen((open) => !open)}>
              应用
            </button>
            {menuOpen ? (
              <div className={`${styles.menu} ${styles.menuLeft}`} role="menu">
                {parts.overflow.map((app) => (
                  <AppControl key={app.app_id} app={app} canSwitch={canSwitch} busy={launchBusy === true} onLaunch={onLaunchApp} />
                ))}
              </div>
            ) : null}
          </div>
        ) : null}
      </div>
      <div className={styles.spacer} />
      {back ? (
        <button
          type="button"
          className={styles.returnChip}
          data-testid="eco-return-source"
          data-source-app={back.source_app_id}
          data-return-target={back.return_target_id}
          data-has-source-ref={back.source_ref ? "true" : "false"}
          disabled={launchBusy === true || !onReturn}
          title={
            back.source_ref
              ? `回到 ${back.label}（来源对象已记录）`
              : `回到 ${back.label}；具体条目由来源应用恢复`
          }
          onClick={() => onReturn?.(back.source_app_id, back.return_target_id)}
        >
          ← {back.label}
        </button>
      ) : null}
      <div className={styles.context}>
        {affordances.tenantSwitcher
          ? nav.scopes.map((item) => {
              const name = item.display_name ?? item.tenant_id;
              return (
                <button
                  key={item.tenant_id}
                  type="button"
                  className={item.tenant_id === nav.active_tenant_id ? `${styles.button} ${styles.pressed}` : styles.button}
                  data-testid={`eco-tenant-${item.tenant_id}`}
                  aria-pressed={item.tenant_id === nav.active_tenant_id}
                  onClick={() => {
                    const next = selectTenant(nav, item.tenant_id);
                    if (next.active_tenant_id === nav.active_tenant_id) return;
                    setNav(next);
                    if (next.active_tenant_id) onTenantSwitch(next.active_tenant_id);
                  }}
                >
                  <span className={styles.ellipsis}>{name}</span>
                </button>
              );
            })
          : null}
        {/* HUI-2626 盲评 P1：额度徽标走 badge 样式，390 不再逐字断行成碎片。 */}
        <span className={styles.badge} data-testid="eco-billing">{billingBadgeText(nav)}</span>
        <span className={styles.badge} data-testid="eco-role">
          {roleText}
        </span>
        <Link className={styles.quiet} href="/subscription" data-testid="eco-account-usage">
          账户与用量
        </Link>
      </div>
      <div className={styles.right}>
        <span className={styles.ellipsis}>{nickname}</span>
      </div>
      {launchError ? (
        <span className={styles.launchError} role="alert" data-testid="eco-launch-error">
          {launchError}
        </span>
      ) : null}
      {affordances.implicitCrmHrefs.length === 0 ? <span hidden data-testid="eco-implicit-crm-entry" data-count="0" /> : null}
    </header>
  );
}
