import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// P0 侧边导航当前页高亮：aria-current="page" 是无障碍真源；"/" 只做全等匹配，
// 其余项按路径段前缀匹配（/leads 命中 /leads/:id 详情页）。样式只消费既有 token。
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

describe("shell nav active matching", () => {
  it("matches the home route exactly and never as a prefix", () => {
    expect(isNavActive("/", "/")).toBe(true);
    expect(isNavActive("/leads", "/")).toBe(false);
    expect(isNavActive("/leads/lead-9", "/")).toBe(false);
    expect(isNavActive("/leads?focus=1", "/")).toBe(false);
  });

  it("matches section routes on the page itself and its detail children", () => {
    expect(isNavActive("/leads", "/leads")).toBe(true);
    expect(isNavActive("/leads/lead-9", "/leads")).toBe(true);
    expect(isNavActive("/leads/lead-9/timeline", "/leads")).toBe(true);
    // 前缀必须按路径段切，/leadsxxx 不是线索页。
    expect(isNavActive("/leadsxxx", "/leads")).toBe(false);
    expect(isNavActive("/contacts", "/leads")).toBe(false);
    // 查询串与锚点不参与匹配。
    expect(isNavActive("/leads?focus=1#row", "/leads")).toBe(true);
  });

  it("covers every SHELL_NAV href and treats a missing pathname as nothing active", () => {
    for (const item of SHELL_NAV) {
      expect(isNavActive(item.href, item.href)).toBe(true);
    }
    expect(isNavActive(null, "/leads")).toBe(false);
    expect(isNavActive(undefined, "/")).toBe(false);
    expect(isNavActive("", "/")).toBe(false);
  });
});

describe("shell nav renders the current page marker", () => {
  it("marks 今天 on the home route and leaves the rest unmarked", () => {
    nav.pathname = "/";
    const html = chromeMarkup();
    expect(html).toMatch(/<a[^>]*aria-current="page"[^>]*href="\/"[^>]*>今天<\/a>/);
    expect(html.match(/aria-current="page"/g)).toHaveLength(1);
    expect(html).not.toMatch(/<a[^>]*href="\/leads"[^>]*aria-current/);
    expect(html).toContain('<a href="/leads">线索</a>');
  });

  it("marks 线索 on a lead detail page via the prefix rule", () => {
    nav.pathname = "/leads/lead-9";
    const html = chromeMarkup();
    expect(html).toMatch(/<a[^>]*aria-current="page"[^>]*href="\/leads"[^>]*>线索<\/a>/);
    expect(html).toContain('<a href="/">今天</a>');
    expect(html.match(/aria-current="page"/g)).toHaveLength(1);
  });

  it("marks exactly one item on a section route", () => {
    nav.pathname = "/opportunities";
    const html = chromeMarkup();
    expect(html).toMatch(/<a[^>]*aria-current="page"[^>]*href="\/opportunities"[^>]*>商机<\/a>/);
    expect(html.match(/aria-current="page"/g)).toHaveLength(1);
    nav.pathname = "/";
  });
});

describe("shell nav current styling", () => {
  it("styles the marker from existing tokens, never raw hex", () => {
    const css = readFileSync("src/app/globals.css", "utf8");
    const at = css.indexOf('.desk-nav a[aria-current="page"]');
    expect(at).toBeGreaterThan(0);
    const rule = css.slice(at, at + 300);
    expect(rule).toContain("var(--");
    expect(rule).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(rule).toContain("font-weight");
  });
});
