// HUI-2626 fix2（gate-r2 #2）：JSON 请求体的序列化收敛在 lib 层，
// 页面代码不出现 JSON.stringify / 裸状态码文案（2625 detector 口径）。

export interface JsonResponse<T> {
  ok: boolean;
  status: number;
  body: T;
}

export async function postJson<T = Record<string, unknown>>(
  path: string,
  headers: Record<string, string>,
  payload: unknown,
): Promise<JsonResponse<T>> {
  const res = await fetch(path, {
    method: "POST",
    headers,
    body: JSON.stringify(payload === undefined ? {} : payload),
  });
  const body = (await res.json().catch(() => ({}))) as T;
  return { ok: res.ok, status: res.status, body };
}

// 2026-10-11 AG06：分配负责人走 PATCH，序列化同样收敛在本文件。
export async function patchJson<T = Record<string, unknown>>(
  path: string,
  headers: Record<string, string>,
  payload: unknown,
): Promise<JsonResponse<T>> {
  const res = await fetch(path, {
    method: "PATCH",
    headers,
    body: JSON.stringify(payload === undefined ? {} : payload),
  });
  const body = (await res.json().catch(() => ({}))) as T;
  return { ok: res.ok, status: res.status, body };
}
