"use client";

import React, { createContext, useContext, useSyncExternalStore, type ReactNode } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Button as PainuoButton } from "@/vendor/painuo/react/v1/src/index";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  SHELL_NAV,
  chooseScope,
  isNavActive,
  factTone,
  shellStructure,
  toneLabel,
  type FactTone,
  type WorkScope,
} from "@/lib/productShell";
import { Skeleton } from "@/components/ui/skeleton";

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

// LD-07：事实项。numeric 仅标记金额/概率/日期/计数等数字语境，
// 渲染层据此挂等宽数字；数据层格式化函数与文案语义不动。
export interface FactItem {
  label: string;
  value: string;
  numeric?: boolean;
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
  facts: FactItem[];
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
              <strong data-numeric={fact.numeric || undefined}>{fact.value}</strong>
            </li>
          ))}
        </ul>
      ) : (
        <dl data-facts="columns">
          {facts.map((fact) => (
            <div key={fact.label}>
              <dt>{fact.label}</dt>
              <dd data-numeric={fact.numeric || undefined}>{fact.value}</dd>
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
    facts: FactItem[];
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

// LD-07：列表加载态的逐行/逐块骨架。外层容器承担 role=status + aria-busy 语义，
// 内层骨架条全部 aria-hidden；骨架行数贴近真实卡片结构（标题 + 若干事实条），
// 可见的加载文案仍由 SurfaceState 承担，不用纯装饰 div 传达加载。
export function RecordListSkeleton({ rows = 3, facts = 4 }: { rows?: number; facts?: number }) {
  return (
    <div className="record-list" data-skeleton-list="true" role="status" aria-busy="true" aria-label="内容加载中">
      {Array.from({ length: rows }, (_, rowIndex) => (
        <article className="record-frame" data-skeleton-row="true" aria-hidden="true" key={rowIndex}>
          <header className="record-head">
            <Skeleton className="h-5 w-20" />
            <Skeleton className="h-5 w-2/5" />
          </header>
          <ul data-facts="chips" aria-hidden="true">
            {Array.from({ length: facts }, (_, factIndex) => (
              <li key={factIndex}>
                <Skeleton className="h-4 w-16" />
                <Skeleton className="h-4 w-20" />
              </li>
            ))}
          </ul>
        </article>
      ))}
    </div>
  );
}

// LD-07：纯文本记录列表（无卡片结构）的逐行骨架，节奏对齐真实条目行。
export function LineListSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div data-skeleton-list="true" role="status" aria-busy="true" aria-label="内容加载中">
      {Array.from({ length: rows }, (_, rowIndex) => (
        <div className="record-frame" data-skeleton-row="true" aria-hidden="true" key={rowIndex}>
          <Skeleton className="h-4 w-full" />
        </div>
      ))}
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
  scopeEmptyLabel = "未选择",
  children,
}: {
  width: number;
  scopes: WorkScope[];
  activeId: string | null;
  onSwitch: (tenant: string) => void;
  /** 没有已确认范围时范围条与空选项的文案；默认“未选择”，未登录由壳传入“未登录”。 */
  scopeEmptyLabel?: string;
  children: ReactNode;
}) {
  const structure = shellStructure(width);
  const active = chooseScope(scopes, activeId ?? "");
  const pathname = usePathname();
  return (
    <div className="desk-frame" data-shell-density={structure.density} data-shell-nav={structure.navigation}>
      {/* HUI-2626 fix2（gate-r2 #4 region）：工作范围条补地标，页面内容全部落入 landmark。 */}
      <div className="desk-context" id="work-context" data-work-context="tenant" role="region" aria-label="工作范围">
        <span>工作范围</span>
        <strong>{active?.display_name ?? scopeEmptyLabel}</strong>
        <Select
          value={active?.tenant_id ?? ""}
          onValueChange={(value) => {
            const chosen = chooseScope(scopes, value);
            if (chosen) onSwitch(chosen.tenant_id);
          }}
        >
          <SelectTrigger aria-label="切换工作范围" className="w-auto">
            <SelectValue placeholder={scopeEmptyLabel} />
          </SelectTrigger>
          <SelectContent>
            {scopes.map((scope) => (
              <SelectItem key={scope.tenant_id} value={scope.tenant_id}>
                {scope.display_name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <nav className="desk-nav" aria-label="销售工作台" data-shell-nav={structure.navigation}>
        {/* P0：当前页导航项高亮。aria-current="page" 是无障碍真源，样式跟随选择器。 */}
        {SHELL_NAV.map((item) => {
          const current = isNavActive(pathname, item.href);
          return (
            <Link
              key={item.id}
              href={item.href}
              aria-current={current ? "page" : undefined}
              className={current ? "nav-current" : undefined}
            >
              {item.label}
            </Link>
          );
        })}
      </nav>
      <div data-shell-main="true">{children}</div>
    </div>
  );
}
