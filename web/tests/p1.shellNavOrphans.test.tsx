import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// ⑦ 孤儿页入导航：授权互动（/channel-interactions）与企业资料（/enterprises）
// 此前全站无入口。两页功能完整（真实列表 + 登记/确认操作走真 API），加入
// SHELL_NAV；nav 高亮（isNavActive）对新增项自动生效。

const nav = vi.hoisted(() => ({ pathname: "/" as string | null }));
vi.mock("next/navigation", () => ({ usePathname: () => nav.pathname }));

import { WorkbenchChrome } from "@/components/workbench/chrome";
import { SHELL_NAV, isNavActive } from "@/lib/productShell";

function chromeMarkup(): string {
  return renderToStaticMarkup(
    createElement(WorkbenchChrome, {
      width: 1440,
      scopes: [{ tenant_id: "tenant-a", display_name: "客户甲" }],
      activeId: "tenant-a",
      onSwitch: () => undefined,
      children: null,
    }),
  );
}

describe("SHELL_NAV 新增授权互动与企业资料", () => {
  it("包含两项新入口，标签与 href 对应真实路由", () => {
    const channel = SHELL_NAV.find((item) => item.href === "/channel-interactions");
    const enterprises = SHELL_NAV.find((item) => item.href === "/enterprises");
    expect(channel?.label).toBe("授权互动");
    expect(enterprises?.label).toBe("企业资料");
    // 路由真实存在（页面文件在 app 目录下）。
    for (const rel of ["src/app/(shell)/channel-interactions/page.tsx", "src/app/(shell)/enterprises/page.tsx"]) {
      expect(() => readFileSync(rel, "utf8")).not.toThrow();
    }
  });

  it("插入在接待之后、商机之前（集客来源区）", () => {
    const hrefs = SHELL_NAV.map((item) => item.href);
    expect(hrefs.indexOf("/reception")).toBeLessThan(hrefs.indexOf("/channel-interactions"));
    expect(hrefs.indexOf("/channel-interactions")).toBeLessThan(hrefs.indexOf("/enterprises"));
    expect(hrefs.indexOf("/enterprises")).toBeLessThan(hrefs.indexOf("/opportunities"));
  });

  it("href 全站唯一，isNavActive 对新项生效", () => {
    const hrefs = SHELL_NAV.map((item) => item.href);
    expect(new Set(hrefs).size).toBe(hrefs.length);
    expect(isNavActive("/channel-interactions", "/channel-interactions")).toBe(true);
    expect(isNavActive("/enterprises", "/enterprises")).toBe(true);
    // 前缀按路径段切：别的页不误亮。
    expect(isNavActive("/channel-interactionsx", "/channel-interactions")).toBe(false);
    expect(isNavActive("/leads", "/enterprises")).toBe(false);
  });

  it("壳层渲染新项并按当前页高亮（aria-current 唯一）", () => {
    nav.pathname = "/channel-interactions";
    const html = chromeMarkup();
    expect(html).toMatch(/<a[^>]*aria-current="page"[^>]*href="\/channel-interactions"[^>]*>授权互动<\/a>/);
    expect(html).toContain('<a href="/enterprises">企业资料</a>');
    expect(html.match(/aria-current="page"/g)).toHaveLength(1);
    nav.pathname = "/enterprises";
    const html2 = chromeMarkup();
    expect(html2).toMatch(/<a[^>]*aria-current="page"[^>]*href="\/enterprises"[^>]*>企业资料<\/a>/);
    expect(html2.match(/aria-current="page"/g)).toHaveLength(1);
    nav.pathname = "/";
  });
});
