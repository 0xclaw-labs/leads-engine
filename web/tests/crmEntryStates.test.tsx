import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { CrmEntryState } from "@/components/eco-nav/CrmShell";
import { SCOPE_UNAVAILABLE } from "@/lib/productShell";
import {
  AUTH_ACCOUNT_CENTER_URL,
  buildLeadsReturnUrl,
  buildPortalLoginUrl,
  parseSsoErrorReason,
  type SsoErrorReason,
} from "@/lib/sso-entry";

// 产品门口友好准入引导：未登录给 modelxing.com 统一账号登录真实按钮，
// 无团队席位（not_member / membership_required）给切换其他账号（prompt=login）与账号中心按钮，
// 服务不可达给可重试错误态。node SSR 环境（无 jsdom），纯展示组件直接渲染校验。

function render(
  failure: "unauthenticated" | "no_scope" | "unreachable",
  extra: { ssoError?: SsoErrorReason | null; returnUrl?: string } = {},
) {
  return renderToStaticMarkup(
    createElement(CrmEntryState, { failure, onRetry: () => undefined, ...extra }),
  );
}

describe("crm entry states", () => {
  it("未登录：渲染可点击的 modelxing.com 统一账号登录按钮与重试按钮", () => {
    const html = render("unauthenticated", { returnUrl: "https://leads.modelxing.com/leads" });
    expect(html).toContain('data-surface-state="recovery"');
    expect(html).toContain("请先登录后进入数海获客");
    expect(html).toContain("通过 modelxing.com 统一账号登录");
    expect(html).toContain('data-testid="crm-sso-login"');
    const expectedHref = buildPortalLoginUrl({ next: "https://leads.modelxing.com/leads" });
    expect(expectedHref).toBe(
      "https://auth.modelxing.com/login?app=leads-engine&next=https%3A%2F%2Fleads.modelxing.com%2Fleads",
    );
    expect(html).toContain(expectedHref.replace(/&/g, "&amp;"));
    expect(html).toContain("重试");
  });

  it("未登录且携带 SSO 失败原因：展示友好中文提示且仍提供统一账号登录按钮", () => {
    const html = render("unauthenticated", { ssoError: "identity_unavailable" });
    expect(html).toContain("统一身份服务暂时不可用");
    expect(html).toContain("通过 modelxing.com 统一账号登录");
    expect(html).not.toContain("identity_unavailable");
  });

  it("无团队席位（403 not_member / 空 memberships）：明确提示并给出切换其他账号（prompt=login）与前往账号中心按钮", () => {
    const html = render("no_scope", { returnUrl: "https://leads.modelxing.com/" });
    expect(html).toContain('data-surface-state="recovery"');
    expect(html).toContain("当前账号尚未加入已开通数海获客的团队");
    expect(html).toContain(SCOPE_UNAVAILABLE);
    expect(html).toContain("主管理员确认成员关系后重试");
    expect(html).toContain("切换其他账号");
    expect(html).toContain('data-testid="crm-sso-switch-account"');
    const switchHref = buildPortalLoginUrl({
      next: "https://leads.modelxing.com/",
      switchAccount: true,
    });
    expect(switchHref).toBe(
      "https://auth.modelxing.com/login?app=leads-engine&prompt=login&next=https%3A%2F%2Fleads.modelxing.com%2F",
    );
    expect(html).toContain(switchHref.replace(/&/g, "&amp;"));
    expect(html).toContain("前往账号中心");
    expect(html).toContain('data-testid="crm-sso-account-center"');
    expect(html).toContain(`href="${AUTH_ACCOUNT_CENTER_URL}"`);
    expect(html).toContain("重试");
  });

  it("exchange 403 落回 ?sso_error=membership_required：即便尚未持有本地会话也展示团队席位引导与切换账号按钮", () => {
    const html = render("unauthenticated", { ssoError: "membership_required" });
    expect(html).toContain("当前账号尚未加入已开通数海获客的团队");
    expect(html).toContain("切换其他账号");
    expect(html).toContain("prompt=login");
    expect(html).toContain("前往账号中心");
  });

  it("服务不可达：可重试的错误态，不暴露状态码与机器串", () => {
    const html = render("unreachable");
    expect(html).toContain('data-surface-state="error"');
    expect(html).toContain("服务暂时没有响应");
    expect(html).toContain("请检查网络后重试");
    expect(html).toContain("重试");
    expect(html).not.toMatch(/HTTP|\b401\b|\b403\b|\b503\b|unauthenticated|unreachable/i);
  });

  it("三态都带可执行动作，不再是死胡同", () => {
    for (const failure of ["unauthenticated", "no_scope", "unreachable"] as const) {
      expect(render(failure)).toContain('data-state-action="true"');
    }
  });

  it("壳层按分型接线：未登录时范围条显示未登录，旧死胡同文案下线，读取 sso_error", () => {
    const shell = readFileSync("src/components/eco-nav/CrmShell.tsx", "utf8");
    expect(shell).toContain("CrmEntryState");
    expect(shell).toContain("未登录");
    expect(shell).toContain("sso_error");
    expect(shell).not.toContain("工作范围不可用");
    expect(shell).toContain("result.failure");
  });
});

describe("sso-entry URL 与原因码辅助", () => {
  it("只放行白名单 sso_error 原因码", () => {
    expect(parseSsoErrorReason("membership_required")).toBe("membership_required");
    expect(parseSsoErrorReason("failed")).toBe("failed");
    expect(parseSsoErrorReason("identity_unavailable")).toBe("identity_unavailable");
    expect(parseSsoErrorReason("unknown_reason")).toBeNull();
    expect(parseSsoErrorReason("<script>")).toBeNull();
    expect(parseSsoErrorReason(null)).toBeNull();
  });

  it("构造回跳地址时剥离 sso_error/code 并拒绝外域", () => {
    expect(
      buildLeadsReturnUrl("https://leads.modelxing.com/leads?sso_error=failed&code=abc&tab=mine"),
    ).toBe("https://leads.modelxing.com/leads?tab=mine");
    expect(buildLeadsReturnUrl("https://evil.example/phish")).toBe("https://leads.modelxing.com/");
    expect(buildLeadsReturnUrl("http://localhost:18330/")).toBe("https://leads.modelxing.com/");
    expect(buildLeadsReturnUrl(undefined)).toBe("https://leads.modelxing.com/");
  });
});
