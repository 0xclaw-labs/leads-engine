import { readFileSync } from "node:fs";
import { join } from "node:path";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import Home from "@/app/(shell)/page";
import { ConversationPanel } from "@/components/workbench/conversationPanel";
import { RendererProvider } from "@/vendor/painuo/react/v1/src/index";

// HUI-2626：今日工作台重构的静态守卫。
// 真实数据/交互在浏览器走查取证；这里锁定页面结构与“不伪造”的文案。

const ROOT = join(__dirname, "..");
function source(path: string): string {
  return readFileSync(join(ROOT, path), "utf8");
}

function withScope(node: React.ReactNode) {
  return <RendererProvider profile="leads-web" surface="work.light" theme="light">{node}</RendererProvider>;
}

describe("today workspace structure", () => {
  const page = () => source("src/app/(shell)/page.tsx");

  it("declares the workspace page pattern and its regions", () => {
    const src = page();
    expect(src).toContain('data-pattern="workspace"');
    for (const region of ["header", "status", "body", "loading", "empty", "error"]) {
      expect(src, `missing region ${region}`).toContain(`data-region="${region}"`);
    }
  });

  it("puts money/rationale behind a disclosure instead of a top KPI card", () => {
    const src = page();
    expect(src).toContain("<details");
    expect(src).toContain("排序与金额口径");
    expect(src).not.toContain("<h2>金额分开看</h2>");
    const moneyIndex = src.indexOf("money-lines");
    const detailsIndex = src.indexOf("<details");
    expect(detailsIndex).toBeGreaterThan(0);
    expect(moneyIndex).toBeGreaterThan(detailsIndex);
  });

  it("lets a row expand the conversation instead of only linking away", () => {
    const src = page();
    expect(src).toContain("ConversationPanel");
    expect(src).toContain("data-conversation-toggle");
    expect(src).toContain("展开会话与回复");
    expect(src).toContain("为什么在今天");
  });

  it("keeps the list mounted on a same-tenant reload so an open panel is not lost", () => {
    const src = page();
    expect(src).toContain("shownTenant");
    const fetchEffect = src.slice(src.indexOf("const headers = listTenantHeader(scope.tenant)"));
    expect(fetchEffect.slice(0, 300)).not.toContain("setGroups(null)");
    expect(fetchEffect.slice(0, 300)).not.toContain("setLoadedFor(null)");
  });

  it("describes what will appear when the day is empty", () => {
    expect(page()).toContain("新的咨询、到期的跟进、未回复的会话和待确认的草稿会按顺序出现在这里");
  });

  it("SSR marks the workspace and still shows loading/recovery honestly", () => {
    const html = renderToStaticMarkup(withScope(<Home />));
    expect(html).toContain('data-pattern="workspace"');
    expect(html).toContain('data-today-home="today-next"');
    expect(html).toContain("今天要完成的客户工作");
    expect(html).toContain("正在整理今天的工作");
    expect(html).toContain("排序与金额口径");
    expect(html).toContain("还没有工作范围");
    expect(html).not.toContain("<table");
  });
});

describe("conversation panel structure", () => {
  it("renders an accessible, explicitly non-sending loading state", () => {
    const html = renderToStaticMarkup(
      withScope(<ConversationPanel sessionId="rcs_test" focusId="desk_1" />),
    );
    expect(html).toContain('aria-label="会话与回复"');
    expect(html).toContain('data-conversation-session="rcs_test"');
    expect(html).toContain("正在读取会话和草稿；发送状态以服务端为准。");
    expect(html).toContain("data-conversation-state");
    expect(html).not.toContain("<form");
  });

  it("has no local success claim and relies on server states", () => {
    const src = source("src/components/workbench/conversationPanel.tsx");
    expect(src).not.toMatch(/已送达|发送成功/);
    expect(src).toContain("草稿已保存，尚未发送");
    expect(src).toContain("保存草稿只写入服务端");
    expect(src).toContain("/api/workbench/sessions/");
    expect(src).toContain("/api/reception/sessions/");
  });
});
