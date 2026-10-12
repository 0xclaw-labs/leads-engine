import { readFileSync } from "node:fs";
import { join } from "node:path";
import React from "react";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { EcoTopNav } from "@/components/eco-nav/EcoTopNav";
import { SurfaceState } from "@/components/workbench/chrome";
import { provisionalEcoNav } from "@/lib/eco-nav/fixture";
import {
  eventKindLabel,
  eventLine,
  humanTime,
  isEngineeringRef,
  leadTitle,
} from "@/lib/humanize";
import { factLabel } from "@/lib/lightCopy";
import { sourceText } from "@/lib/workbench";

const ROOT = join(__dirname, "..");

function source(path: string): string {
  return readFileSync(join(ROOT, path), "utf8");
}

function mediaBlock(css: string, query: string): string {
  const at = css.indexOf(query);
  expect(at, `缺少媒体查询 ${query}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf("{", at);
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === "{") depth++;
    if (css[i] === "}") {
      depth--;
      if (depth === 0) return css.slice(open + 1, i);
    }
  }
  return "";
}

// ---------------------------------------------------------------------------
// 盲评 P1：390 顶部导航逐字竖排（碰/一/碰、接/单）、租户/状态 chips 断行碎片、
// 邮箱截断。修法=≤430 顶栏换行收纳 + 全部导航控件 nowrap；1440 布局不动。
// ---------------------------------------------------------------------------

describe("盲评 P1：390 顶栏收纳（eco-top-nav.module.css）", () => {
  const css = () => source("src/components/eco-nav/eco-top-nav.module.css");

  it("基础规则：导航控件文字不再逐字折行（.quiet / .badge nowrap）", () => {
    const text = css();
    const quietBlock = /(^|\n)\.quiet\s*\{/.exec(text) ?? /(^|\n)\.link,\n\.button,\n\.quiet\s*\{/.exec(text);
    expect(quietBlock, "缺少 .quiet 规则块").toBeTruthy();
    expect(text).toMatch(/\.badge,\s*\n\.roleLine\s*\{[^}]*white-space:\s*nowrap/);
  });

  it("≤430 媒体查询：顶栏换行收纳，不逐字竖排、不碎片化", () => {
    const block = mediaBlock(css(), "@media (max-width: 430px)");
    expect(block).toMatch(/\.bar\s*\{[^}]*flex-wrap:\s*wrap/);
    expect(block).toMatch(/\.bar\s*\{[^}]*min-height:/);
    expect(block).toMatch(/\.spacer\s*\{[^}]*display:\s*none/);
    expect(block).toMatch(/\.right\s*\{[^}]*margin-left:\s*auto/);
  });

  it("≤430 触控目标 ≥44px 的既有规则不被移除（不碰 keyboard/touch PASS）", () => {
    const block = mediaBlock(css(), "@media (max-width: 430px)");
    expect(block).toMatch(/height:\s*44px/);
  });

  it("额度徽标走 badge 样式（不再裸 span 逐字断行）", () => {
    const nav = source("src/components/eco-nav/EcoTopNav.tsx");
    expect(nav).toMatch(/<span className=\{styles\.badge\}\s*data-testid="eco-billing">/);
  });

  it("SSR：EcoTopNav 仍渲染导航/租户/账号区（landmark 与 testid 不动）", () => {
    const html = renderToStaticMarkup(
      createElement(EcoTopNav, {
        model: provisionalEcoNav(),
        nickname: "owner-walk@leads.local",
        onTenantSwitch: () => undefined,
      }),
    );
    expect(html).toContain('data-testid="eco-top-nav"');
    expect(html).toContain("碰一碰");
    expect(html).toContain("接单");
    expect(html).toContain('data-testid="eco-billing"');
    expect(html).toContain('data-testid="eco-role"');
  });
});

// ---------------------------------------------------------------------------
// 盲评 P2①：空态图标 404/空白灰块 + 详情页每次都打 5 条 console 404。
// 修法=空态/错误/恢复态内联 SVG 图标（零外链）+ 应用图标补齐（favicon 不再
// 404）+ capability 发现 200+enabled（fail-closed 不变，探测不再 404）+
// 轻文案只在有活动出处时挂载（不再对普通线索打必 404 的探测）。
// ---------------------------------------------------------------------------

describe("盲评 P2①：空态真图标与 404 噪音清零", () => {
  it("SurfaceState empty/error/recovery 渲染内联 SVG 图标（非空白灰块，无外链）", () => {
    for (const kind of ["empty", "error", "recovery"] as const) {
      const html = renderToStaticMarkup(createElement(SurfaceState, { kind, title: "标题", detail: "说明" }));
      expect(html, `${kind} 应有内联 svg`).toContain("<svg");
      expect(html).toContain(`data-state-mark="${kind}"`);
    }
  });

  it("空态图标不依赖任何网络资源（state-mark 规则无 url()）", () => {
    const css = source("src/app/globals.css");
    const stateRules = css.slice(css.indexOf(".state-mark {"), css.indexOf("[data-skeleton"));
    expect(stateRules).not.toMatch(/url\(/);
  });

  it("应用自带图标（浏览器不再请求 /favicon.ico 吃 404）", () => {
    expect(source("src/app/icon.svg")).toMatch(/<svg/i);
  });

  it("campaign-motion 面板读到 enabled=false 即隐藏（404 兜底保留）", () => {
    const panel = source("src/components/CampaignMotionPanel.tsx");
    expect(panel).toMatch(/enabled === false/);
    expect(panel).toMatch(/capRes\.status === 404/);
  });

  it("轻文案面板只在线索带活动出处时挂载（普通线索不再必打 404 探测）", () => {
    const page = source("src/app/(shell)/leads/[id]/page.tsx");
    expect(page).toMatch(/visible\?\.source\?\.activity\s*\?\s*<LightCopyPanel/);
  });
});

// ---------------------------------------------------------------------------
// 盲评 P2②：左下浮动「N」头像压叠正文——是 Next dev 指示器，走查链全程
// next dev，关掉 devIndicators（生产构建本就不渲染）。
// ---------------------------------------------------------------------------

describe("盲评 P2②：dev 指示器浮动头像关闭", () => {
  it("next.config 关闭 devIndicators", () => {
    const config = source("next.config.mjs");
    expect(config).toMatch(/devIndicators:\s*false/);
  });
});

// ---------------------------------------------------------------------------
// 盲评 P2③+P3：时间线裸露工程标识符与微秒 ISO；『授权 · 授权 xxx』同词重复；
// 『这条线索』占位标题。显示层人化：标签映射 + 秒级时间；原始值留在数据层。
// ---------------------------------------------------------------------------

describe("盲评 P2③/P3：时间线人化", () => {
  it("isEngineeringRef 识别工程引用（cmp_1、sub_walk_b1），不误伤中文与渠道词", () => {
    expect(isEngineeringRef("cmp_1")).toBe(true);
    expect(isEngineeringRef("sub_walk_b1")).toBe(true);
    expect(isEngineeringRef("contact_form_v2")).toBe(true);
    expect(isEngineeringRef("碰一碰")).toBe(false);
    expect(isEngineeringRef("手工录入")).toBe(false);
    expect(isEngineeringRef("wecom")).toBe(false);
  });

  it("授权事件：同词去重 + 已知授权项映射成人话", () => {
    expect(eventLine("consent", "授权 form_authorization")).toBe("授权 · 表单授权");
    expect(eventLine("consent", "授权 channel_reply")).toBe("授权 · 渠道内回复授权");
    expect(eventLine("consent", "授权 marketing_phone")).toBe("授权 · 电话营销授权");
    expect(eventLine("consent", "授权 marketing_sms")).toBe("授权 · 短信营销授权");
    expect(eventLine("consent", "撤销授权 marketing_sms")).toBe("授权 · 撤销短信营销授权");
  });

  it("来源事件：工程引用段退场，留人话渠道词", () => {
    expect(eventLine("source", "活动 cmp_1 · 提交 sub_walk_a2 · 碰一碰")).toBe("来源 · 碰一碰");
    expect(eventLine("source", "表单 contact_form_v2 · 手工录入")).toBe("来源 · 手工录入");
    expect(eventLine("assignment", "已分配")).toBe("分配 · 已分配");
    expect(eventLine("follow_up", "真实跟进：问了到店时间")).toBe("跟进 · 真实跟进：问了到店时间");
    expect(eventKindLabel("mystery")).toBe("mystery");
  });

  it("秒级时间：微秒 ISO 变本地秒级，非时间串原样返回", () => {
    const out = humanTime("2026-10-11T05:14:32.586554Z");
    expect(out).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
    expect(out).not.toContain("T");
    expect(out).not.toContain(".");
    expect(humanTime("2026-10-12T10:00")).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
    expect(humanTime("还没有时间")).toBe("还没有时间");
    expect(humanTime("")).toBe("");
  });

  it("记录卡标题不再是占位『这条线索』，按来源人化", () => {
    expect(leadTitle({ channel: "碰一碰" })).toBe("碰一碰线索");
    expect(leadTitle({ form: "contact_form_v2" })).toBe("表单线索");
    expect(leadTitle({})).toBe("线索档案");
    expect(leadTitle(undefined)).toBe("线索档案");
  });

  it("来源事实行：工程引用退场 + 秒级时间（数据层字段不动）", () => {
    const line = sourceText({ activity: "cmp_1", channel: "碰一碰", at: "2026-10-11T05:14:32.586554Z" });
    expect(line).not.toContain("cmp_1");
    expect(line).toContain("碰一碰");
    expect(line).toMatch(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
    expect(sourceText(undefined)).toBe("来源未记录");
  });

  it("时间线渲染接线：eventLine/humanTime/leadTitle 进详情页", () => {
    const page = source("src/app/(shell)/leads/[id]/page.tsx");
    expect(page).toMatch(/eventLine\(event\.kind,\s*event\.summary\)/);
    expect(page).toMatch(/humanTime\(event\.at\)/);
    expect(page).toMatch(/leadTitle\(visible\.source\)/);
    expect(page).not.toMatch(/title="这条线索"/);
  });

  it("轻文案事实复选框用中文标签（activity_ref 不再裸奔）", () => {
    expect(factLabel("activity_ref")).toBe("活动引用");
    expect(factLabel("title")).toBe("标题");
    expect(factLabel("price_cents")).toContain("价格");
    expect(factLabel("custom_key")).toBe("custom_key");
  });
});

// ---------------------------------------------------------------------------
// 盲评 P3：390 页脚链接从词中间折行（订/阅与用量）。每个链接整体换行。
// ---------------------------------------------------------------------------

describe("盲评 P3：页脚链接整体换行", () => {
  it("今日页页脚带 foot-nav 类", () => {
    const page = source("src/app/(shell)/page.tsx");
    expect(page).toMatch(/className="muted foot-nav"/);
  });

  it("globals.css：foot-nav 链接 inline-block + nowrap", () => {
    const css = source("src/app/globals.css");
    expect(css).toMatch(/\.foot-nav a\s*\{[^}]*white-space:\s*nowrap/);
    expect(css).toMatch(/\.foot-nav a\s*\{[^}]*display:\s*inline-block/);
  });
});
