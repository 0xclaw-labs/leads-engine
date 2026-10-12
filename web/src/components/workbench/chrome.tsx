"use client";

import React, { createContext, useContext, useSyncExternalStore, type ReactNode } from "react";
import Link from "next/link";
import { Button as PainuoButton } from "@/vendor/painuo/react/v1/src/index";
import {
  SHELL_NAV,
  chooseScope,
  factTone,
  shellStructure,
  toneLabel,
  type FactTone,
  type WorkScope,
} from "@/lib/productShell";

const ShellWidthContext = createContext(1440);

function subscribe(onStoreChange: () => void) {
  window.addEventListener("resize", onStoreChange);
  return () => window.removeEventListener("resize", onStoreChange);
}

export function ShellWidthProvider({ children }: { children: ReactNode }) {
  const width = useSyncExternalStore(subscribe, () => window.innerWidth, () => 1440);
  return <ShellWidthContext.Provider value={width}>{children}</ShellWidthContext.Provider>;
}

export function useShellWidth(): number {
  return useContext(ShellWidthContext);
}

export function ToneBadge({ tone }: { tone: FactTone }) {
  return (
    <span className="tone-badge" data-tone={tone}>
      {toneLabel(tone)}
    </span>
  );
}

// HUI-2626 盲评 P2①：空/错/恢复态的标记不再是无图形的灰块——内联 SVG 图标
// （零网络请求，不存在 404 路径）；loading 保留渐变骨架原样。
function StateMarkGlyph({ kind }: { kind: "loading" | "empty" | "error" | "recovery" }) {
  if (kind === "loading") return null;
  const stroke = { fill: "none", stroke: "currentColor", strokeWidth: 1.7, strokeLinecap: "round" as const, strokeLinejoin: "round" as const };
  return (
    <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true" focusable="false">
      {kind === "empty" ? (
        <>
          <path d="M4 13h4l2 3h4l2-3h4" {...stroke} />
          <path d="M6.2 6.2 4 13v5a1.5 1.5 0 0 0 1.5 1.5h13A1.5 1.5 0 0 0 20 18v-5l-2.2-6.8A1.5 1.5 0 0 0 16.4 5H7.6a1.5 1.5 0 0 0-1.4 1.2Z" {...stroke} />
        </>
      ) : null}
      {kind === "error" ? (
        <>
          <path d="M12 4.6 21 19.4H3L12 4.6Z" {...stroke} />
          <path d="M12 10v4.2" {...stroke} />
          <circle cx="12" cy="16.8" r="0.4" fill="currentColor" />
        </>
      ) : null}
      {kind === "recovery" ? (
        <>
          <circle cx="12" cy="12" r="8.2" {...stroke} />
          <path d="M12 11v5.4" {...stroke} />
          <circle cx="12" cy="7.8" r="0.4" fill="currentColor" />
        </>
      ) : null}
    </svg>
  );
}

export function SurfaceState({
  kind,
  title,
  detail,
  action,
}: {
  kind: "loading" | "empty" | "error" | "recovery";
  title?: string;
  detail?: string;
  action?: ReactNode;
}) {
  const copy = {
    loading: { title: title ?? "正在读取", detail: detail ?? "页面还在整理，请稍候。" },
    empty: { title: title ?? "这里还没有内容", detail: detail ?? "换个条件，或等新的客户工作进来。" },
    error: { title: title ?? "这一步没有完成", detail: detail ?? "请重试。如果仍不行，换一个工作范围再看。" },
    recovery: { title: title ?? "需要先选择工作范围", detail: detail ?? "先在顶部选择工作范围。" },
  }[kind];
  return (
    <section className="surface-state" data-surface-state={kind} role={kind === "error" ? "alert" : "status"} aria-busy={kind === "loading" ? true : undefined}>
      <span className={`state-mark state-mark-${kind}`} data-state-mark={kind} aria-hidden="true">
        <StateMarkGlyph kind={kind} />
      </span>
      {kind === "loading" ? (
        <div data-skeleton="block" aria-hidden="true">
          <span data-skeleton="title" />
          <span data-skeleton="line" />
          <span data-skeleton="line" />
        </div>
      ) : null}
      <h2>{copy.title}</h2>
      <p>{copy.detail}</p>
      {action ? <div data-state-action="true">{action}</div> : null}
    </section>
  );
}

export function RecordFrame({
  width,
  tone,
  title,
  facts,
  primary,
  secondary,
  children,
}: {
  width: number;
  tone: FactTone;
  title: string;
  facts: { label: string; value: string }[];
  primary?: ReactNode;
  secondary?: ReactNode;
  children?: ReactNode;
}) {
  const structure = shellStructure(width);
  return (
    <article className="record-frame" data-record={structure.records} data-density={structure.density} data-columns={structure.columns} data-tone={tone}>
      <header className="record-head">
        <ToneBadge tone={tone} />
        {/* HUI-2626 fix2（gate-r2 #4 heading-order）：记录标题用 h2，页面 h1 之后不再跳级。 */}
        <h2>{title}</h2>
        {primary ? <div data-page-primary="true">{primary}</div> : null}
        {secondary ? <div data-page-primary="false">{secondary}</div> : null}
      </header>
      {structure.factLayout === "chips" ? (
        <ul data-facts="chips">
          {facts.map((fact) => (
            <li key={fact.label}>
              <span>{fact.label}</span>
              <strong>{fact.value}</strong>
            </li>
          ))}
        </ul>
      ) : (
        <dl data-facts="columns">
          {facts.map((fact) => (
            <div key={fact.label}>
              <dt>{fact.label}</dt>
              <dd>{fact.value}</dd>
            </div>
          ))}
        </dl>
      )}
      {children}
    </article>
  );
}

export function RecordList({
  width,
  rows,
}: {
  width: number;
  rows: {
    id: string;
    title: string;
    tone?: FactTone;
    facts: { label: string; value: string }[];
    href?: string;
    action?: string;
    primary?: boolean;
    /** HUI-2626：行内打开详情抽屉（不路由跳转，保留列表筛选/滚动）。 */
    onOpen?: () => void;
  }[];
}) {
  const structure = shellStructure(width);
  return (
    <div className="record-list" data-record={structure.records} data-density={structure.density}>
      {rows.map((row) => {
        // onOpen 优先：行动作开抽屉；href 交给抽屉内的「打开完整页」链接，避免一排同权链接。
        const action = row.onOpen ? (
          <PainuoRowButton label={row.action ?? "查看详情"} primary={row.primary === true} onOpen={row.onOpen} />
        ) : row.href ? (
          <Link className={row.primary ? "btn primary" : "btn"} href={row.href}>
            {row.action ?? "打开"}
          </Link>
        ) : null;
        return (
          <RecordFrame
            key={row.id}
            width={width}
            tone={row.tone ?? factTone({ kind: "human" })}
            title={row.title}
            facts={row.facts}
            primary={row.primary ? action : undefined}
            secondary={row.primary ? undefined : action}
          />
        );
      })}
    </div>
  );
}

// 行级抽屉触发按钮。renderer Button 需要 provider scope（壳级已提供）。
function PainuoRowButton({ label, primary, onOpen }: { label: string; primary: boolean; onOpen: () => void }) {
  return (
    <PainuoButton
      variant={primary ? "primary" : "secondary"}
      size="sm"
      type="button"
      onClick={() => onOpen()}
    >
      {label}
    </PainuoButton>
  );
}

export function WorkbenchChrome({
  width,
  scopes,
  activeId,
  onSwitch,
  children,
}: {
  width: number;
  scopes: WorkScope[];
  activeId: string | null;
  onSwitch: (tenant: string) => void;
  children: ReactNode;
}) {
  const structure = shellStructure(width);
  const active = chooseScope(scopes, activeId ?? "");
  return (
    <div className="desk-frame" data-shell-density={structure.density} data-shell-nav={structure.navigation}>
      {/* HUI-2626 fix2（gate-r2 #4 region）：工作范围条补地标，页面内容全部落入 landmark。 */}
      <div className="desk-context" id="work-context" data-work-context="tenant" role="region" aria-label="工作范围">
        <span>工作范围</span>
        <strong>{active?.display_name ?? "未选择"}</strong>
        <select
          aria-label="切换工作范围"
          value={active?.tenant_id ?? ""}
          onChange={(event) => {
            const chosen = chooseScope(scopes, event.target.value);
            if (chosen) onSwitch(chosen.tenant_id);
          }}
        >
          {active ? null : <option value="">未选择</option>}
          {scopes.map((scope) => (
            <option key={scope.tenant_id} value={scope.tenant_id}>
              {scope.display_name}
            </option>
          ))}
        </select>
      </div>
      <nav className="desk-nav" aria-label="销售工作台" data-shell-nav={structure.navigation}>
        {SHELL_NAV.map((item) => (
          <Link key={item.id} href={item.href}>
            {item.label}
          </Link>
        ))}
      </nav>
      <div data-shell-main="true">{children}</div>
    </div>
  );
}
