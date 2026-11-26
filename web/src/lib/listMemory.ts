// HUI-2626：列表页筛选与滚动位置记忆（按租户隔离，切租户不串状态）。
// 序列化集中收在 lib，页面源文件不出现 JSON 序列化与原始存储键。

export interface ListViewState {
  filter?: string;
  queryName?: string;
  queryTag?: string;
  scrollY?: number;
}

export type ListMemory = Record<string, Record<string, ListViewState>>;

const LIST_MEMORY_KEY = "leads_list_view_memory";

function validKey(token: string | null | undefined): string {
  const trimmed = (token ?? "").trim();
  if (!trimmed || trimmed.length > 128) return "";
  if (!/^[A-Za-z0-9._:@-]+$/.test(trimmed)) return "";
  return trimmed;
}

export function rememberListState(
  memory: ListMemory,
  tenant: string | null | undefined,
  route: string,
  state: ListViewState,
): ListMemory {
  const t = validKey(tenant);
  const r = validKey(route);
  if (!t || !r) return memory;
  const scrollY = typeof state.scrollY === "number" && Number.isFinite(state.scrollY) && state.scrollY >= 0
    ? Math.round(state.scrollY)
    : undefined;
  const clean: ListViewState = {};
  if (typeof state.filter === "string" && state.filter.trim()) clean.filter = state.filter.trim();
  if (typeof state.queryName === "string" && state.queryName.trim()) clean.queryName = state.queryName.trim();
  if (typeof state.queryTag === "string" && state.queryTag.trim()) clean.queryTag = state.queryTag.trim();
  if (scrollY !== undefined) clean.scrollY = scrollY;
  return {
    ...memory,
    [t]: {
      ...(memory[t] ?? {}),
      [r]: clean,
    },
  };
}

export function recallListState(
  memory: ListMemory | null | undefined,
  tenant: string | null | undefined,
  route: string,
): ListViewState {
  const t = validKey(tenant);
  const r = validKey(route);
  if (!memory || !t || !r) return {};
  return memory[t]?.[r] ?? {};
}

export function readListMemory(): ListMemory {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.sessionStorage.getItem(LIST_MEMORY_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as unknown;
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as ListMemory) : {};
  } catch {
    return {};
  }
}

export function persistListMemory(memory: ListMemory): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(LIST_MEMORY_KEY, JSON.stringify(memory));
  } catch {
    /* sessionStorage unavailable: list page degrades gracefully */
  }
}
