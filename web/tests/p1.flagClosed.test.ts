import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { FEATURE_CLOSED_TEXT, domainFailureText, failureText, flagClosedText } from "@/lib/productShell";

// ⑥ flag 关闭页 404 语义修正。服务端证据：
// - 登记制 flag（FEATURE_SOP_REACH / FEATURE_OUTBOUND_CALL / FEATURE_INTENT_GRADE /
//   FEATURE_RECEPTION 等）关闭时路由不注册，Go ServeMux 以 text/plain
//   「404 page not found」应答（server.go 注释「路由不注册(404 不可见)」）。
// - 记录级 404 一定是 fail() 的 JSON：{"error":"not_found","message":...}
//   （server.go fail()；全部 StatusNotFound 处理器均经 fail()，唯一例外
//   tenant_map.go 也带 error 字段）。
// 因此「404 且解析不出 error 字段」可精确判定为功能未开通，映射为开通指引；
// 记录级 404 保持「这条记录不存在」原语义。

const SRC_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "src");

describe("flagClosedText 404 分型", () => {
  it("flag 关闭（mux 纯文本 404，解析不出 JSON）→ 开通指引", () => {
    expect(flagClosedText(404, null)).toBe(FEATURE_CLOSED_TEXT);
    expect(flagClosedText(404, {})).toBe(FEATURE_CLOSED_TEXT);
    expect(flagClosedText(404, "404 page not found")).toBe(FEATURE_CLOSED_TEXT);
    expect(flagClosedText(404, undefined)).toBe(FEATURE_CLOSED_TEXT);
  });

  it("记录级 404（fail JSON 带 error 字段）→ 不算功能未开通", () => {
    expect(flagClosedText(404, { error: "not_found", message: "record not found" })).toBeNull();
    expect(flagClosedText(404, { error: "unknown_tenant", message: "target tenant is not provisioned" })).toBeNull();
  });

  it("非 404 一律不判功能未开通", () => {
    expect(flagClosedText(500, null)).toBeNull();
    expect(flagClosedText(403, {})).toBeNull();
    expect(flagClosedText(200, { error: "x" })).toBeNull();
    expect(flagClosedText(undefined, null)).toBeNull();
  });
});

describe("domainFailureText 统一映射", () => {
  it("flag 关闭 → 开通指引", () => {
    expect(domainFailureText(404, null)).toBe("该功能未开通，请联系管理员开通。");
    expect(domainFailureText(404, {})).toBe("该功能未开通，请联系管理员开通。");
  });

  it("记录级 404 → 保持记录不存在语义", () => {
    expect(domainFailureText(404, { error: "not_found", message: "record not found" })).toBe(
      "这条记录不存在，或不在当前工作范围。",
    );
  });

  it("其余状态沿用 failureText 原语义（空 body 容错）", () => {
    expect(domainFailureText(403, null)).toBe(failureText({}, 403));
    expect(domainFailureText(500, { message: "internal server error" })).toBe("服务暂时没有响应，请稍后再试。");
    expect(domainFailureText(400, { message: "活动还没有门店，不能提交。" })).toBe("活动还没有门店，不能提交。");
  });
});

describe("flag 域页面接线", () => {
  const src = (rel: string) => readFileSync(join(SRC_ROOT, rel), "utf8");

  it("sop 页初次加载对 capability/actions 走 domainFailureText，且解析失败不炸整页", () => {
    const page = src("app/(shell)/sop/page.tsx");
    expect(page).toContain("domainFailureText(listRes.status, listBody)");
    expect(page).toContain("domainFailureText(capRes.status, capBody)");
    expect(page).toContain("capRes.json().catch(() => null)");
    expect(page).toContain("listRes.json().catch(() => null)");
  });

  it("outbound 页初次加载对 capability 走 domainFailureText", () => {
    const page = src("app/(shell)/outbound/page.tsx");
    expect(page).toContain("domainFailureText(capRes.status, body)");
    expect(page).toContain("capRes.json().catch(() => null)");
  });

  it("intent 页评分/修正/样本报告走 domainFailureText 且解析失败不再未处理 rejection", () => {
    const page = src("app/(shell)/intent/page.tsx");
    expect(page.match(/domainFailureText\(/g)?.length).toBe(3);
    // 三处 fetch 全部防御性解析（含 catch），不再有裸 res.json() 直接 await。
    expect(page.match(/res\.json\(\)\.catch\(\(\) => null\)/g)?.length).toBe(3);
    expect(page).not.toMatch(/= await res\.json\(\);/);
  });

  it("reception desk-load 与全域共用同一句开通指引", () => {
    const desk = src("app/reception/desk-load.ts");
    expect(desk).toContain("FEATURE_CLOSED_TEXT");
    expect(desk).not.toContain("接待功能没有打开");
  });

  it("记录级列表页（记跟进等）不使用 flag 关闭改写，保持原语义", () => {
    const today = src("app/(shell)/page.tsx");
    expect(today).not.toContain("domainFailureText");
  });
});
