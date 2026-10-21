// 接待功能关闭时，未注册的 desk 路由返回纯文本 404。解析失败不能把解析器原文交给页面。
// ⑥ 与其他 flag 域页面共用同一句开通指引（FEATURE_CLOSED_TEXT），不再各页自拟。
import { FEATURE_CLOSED_TEXT, failureText } from "@/lib/productShell";

const UNAVAILABLE = "服务暂时没有响应，请稍后再试。";

export function receptionDeskLoad(status: number, raw: string):
  | { ok: true; body: unknown }
  | { ok: false; message: string } {
  let body: unknown;
  try {
    body = JSON.parse(raw);
  } catch {
    return { ok: false, message: status === 404 ? FEATURE_CLOSED_TEXT : UNAVAILABLE };
  }
  if (status < 200 || status >= 300) {
    return { ok: false, message: failureText(body, status) };
  }
  return { ok: true, body };
}
