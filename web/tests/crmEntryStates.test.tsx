import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { CrmEntryState } from "@/components/eco-nav/CrmShell";
import { SCOPE_UNAVAILABLE } from "@/lib/productShell";

// 工作台入口死路分型：未登录 / 无范围 / 服务不可达是三种不同的死路，
// 必须各自诚实指路，不再共用一句“工作范围不可用”把人带进无处可去的死胡同。
// node SSR 环境（无 jsdom）；CrmEntryState 是纯展示组件，可直接渲染。

function render(failure: "unauthenticated" | "no_scope" | "unreachable") {
  return renderToStaticMarkup(createElement(CrmEntryState, { failure, onRetry: () => undefined }));
}

describe("crm entry states", () => {
  it("未登录：恢复态给入口指引，不伪造登录链接", () => {
    const html = render("unauthenticated");
    expect(html).toContain('data-surface-state="recovery"');
    expect(html).toContain("请先登录后进入工作台");
    expect(html).toContain("获客工作台不提供独立登录页");
    expect(html).toContain("数海观澜生态工作台");
    expect(html).toContain("重试");
    // 没有真实登录页就没有链接；不出现任何 URL。
    expect(html).not.toMatch(/https?:\/\//);
  });

  it("无范围：保留原句，补管理员路径，不暗示自动开通", () => {
    const html = render("no_scope");
    expect(html).toContain('data-surface-state="recovery"');
    expect(html).toContain("没有可用的工作范围");
    expect(html).toContain(SCOPE_UNAVAILABLE);
    expect(html).toContain("主管理员确认成员关系后重试");
    expect(html).toContain("重试");
    expect(html).not.toMatch(/https?:\/\//);
  });

  it("服务不可达：可重试的错误态，不暴露状态码与机器串", () => {
    const html = render("unreachable");
    expect(html).toContain('data-surface-state="error"');
    expect(html).toContain("服务暂时没有响应");
    expect(html).toContain("请检查网络后重试");
    expect(html).toContain("重试");
    expect(html).not.toMatch(/HTTP|\b401\b|\b403\b|\b503\b|unauthenticated|unreachable/i);
  });

  it("三态都带重试动作，不再是死胡同", () => {
    for (const failure of ["unauthenticated", "no_scope", "unreachable"] as const) {
      expect(render(failure)).toContain('data-state-action="true"');
    }
  });

  it("壳层按分型接线：未登录时范围条显示未登录，旧死胡同文案下线", () => {
    const shell = readFileSync("src/components/eco-nav/CrmShell.tsx", "utf8");
    expect(shell).toContain("CrmEntryState");
    expect(shell).toContain("未登录");
    expect(shell).not.toContain("工作范围不可用");
    // 三种故障都来自 runShellWhoami 的分型，壳层不再自己拼一句通用文案。
    expect(shell).toContain("result.failure");
  });
});
