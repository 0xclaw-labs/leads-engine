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

  it("does not resurrect the service-draft ghost button that only refused clicks", () => {
    const src = page();
    expect(src).not.toContain("创建服务需求草稿");
    expect(src).not.toContain("serviceDraftClick");
    expect(src).not.toContain("serviceDraftControl");
  });

  it("SSR with no work scope renders only the recovery state, not competing page copy", () => {
    const html = renderToStaticMarkup(withScope(<Home />));
    expect(html).toContain('data-pattern="workspace"');
    expect(html).toContain('data-today-home="today-next"');
    // 壳级同款 recovery，默认标题；页内自拟文案不再与壳层状态打架。
    expect(html).toContain("需要先选择工作范围");
    expect(html).toContain("先在顶部选择工作范围。");
    expect(html.match(/data-surface-state="recovery"/g)).toHaveLength(1);
    expect(html).not.toContain("还没有工作范围");
    expect(html).not.toContain("正在整理今天的工作");
    expect(html).not.toContain("排序与金额口径");
    expect(html).not.toContain("今天要完成的客户工作");
    expect(html).not.toContain("<table");
  });
});

describe("today follow submit feedback", () => {
  const page = () => source("src/app/(shell)/page.tsx");

  it("marks the submit in flight and refuses double submits", () => {
    const src = page();
    expect(src).toContain("const [saving, setSaving] = useState(false)");
    expect(src).toContain("if (saving) return;");
    expect(src).toContain("提交中…");
    // finally 归位：失败也恢复可点，不留死按钮。
    expect(src).toMatch(/finally\s*{\s*setSaving\(false\);\s*}/);
  });

  it("disables every follow submit while saving and reuses the draftMsg error channel", () => {
    const src = page();
    expect(src).toContain("disabled={followDisabled || saving}");
    expect(src).toContain("disabled={saving || !draft.note.trim() || !draft.nextAt}");
    expect(src).toContain("setDraftMsg(failureText(out.body, out.status))");
    expect(src).toContain("setDraftMsg(productError((e as Error).message))");
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
