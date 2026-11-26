import { readFileSync } from "node:fs";
import { join } from "node:path";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RecordFrame, SurfaceState, ToneBadge } from "@/components/workbench/chrome";
import { drawerSide } from "@/lib/finish";
import { memberDisplayLabel } from "@/lib/humanize";
import { formatLightCopyFacts } from "@/lib/lightCopy";
import { recallListState, rememberListState, type ListMemory } from "@/lib/listMemory";
import { factTone, rendererDensity, shellStructure } from "@/lib/productShell";
import { attentionSummary } from "@/lib/todayWorkspace";
import { TODAY_GROUPS, TODAY_LABELS, acceptToday, type DeskItem } from "@/lib/workbench";
import { RendererProvider } from "@/vendor/painuo/react/v1/src/index";

// HUI-2626 Product Finish P0 Gate 回归测试：
// 覆盖全部正式路由与业务组件成品红线、Today/Next 7 桶顺序、AI/人工/自动化视觉区分、
// 失败保留输入、抽屉与列表筛选/滚动连续性、租户 A/B 严格隔离与 390/430/1440/1920 响应契约。

const ROOT = join(__dirname, "..");

function source(rel: string): string {
  return readFileSync(join(ROOT, rel), "utf8");
}

function withScope(node: React.ReactNode) {
  return (
    <RendererProvider profile="leads-web" surface="work.light" theme="light">
      {node}
    </RendererProvider>
  );
}

const FORMAL_ROUTES = [
  "src/app/(shell)/page.tsx",
  "src/app/(shell)/leads/page.tsx",
  "src/app/(shell)/leads/[id]/page.tsx",
  "src/app/(shell)/contacts/page.tsx",
  "src/app/(shell)/contacts/[id]/page.tsx",
  "src/app/(shell)/opportunities/page.tsx",
  "src/app/(shell)/opportunities/[id]/page.tsx",
  "src/app/(shell)/sop/page.tsx",
  "src/app/(shell)/outbound/page.tsx",
  "src/app/(shell)/attribution/page.tsx",
  "src/app/(shell)/subscription/page.tsx",
  "src/app/(shell)/channel-interactions/page.tsx",
  "src/app/(shell)/enterprises/page.tsx",
  "src/app/(shell)/intent/page.tsx",
  "src/app/(shell)/isolation/page.tsx",
  "src/app/reception/page.tsx",
  "src/app/reception/session-panel.tsx",
];

const FORMAL_COMPONENTS = [
  "src/components/LightCopyPanel.tsx",
  "src/components/MatrixDraftPanel.tsx",
  "src/components/CampaignMotionPanel.tsx",
  "src/components/IntentOnOpportunity.tsx",
  "src/components/TouchFollowUpReturn.tsx",
  "src/components/workbench/conversationPanel.tsx",
  "src/components/workbench/detailDrawer.tsx",
];

describe("HUI-2626 Product Finish Red-Line Gate（全正式路由与业务组件）", () => {
  for (const file of [...FORMAL_ROUTES, ...FORMAL_COMPONENTS]) {
    it(`${file} 无裸 button/table、无手填租户 ID、无假演示数据、无 JSON UI 倾倒`, () => {
      const src = source(file);
      expect(src, `${file} 不得出现裸 <button`).not.toMatch(/<button[\s>]/);
      expect(src, `${file} 不得出现裸 <table`).not.toMatch(/<table[\s>]/);
      expect(src, `${file} 不得出现手填租户 ID 提示`).not.toMatch(/请输入租户|租户\s*ID|内部\s*ID/);
      expect(src, `${file} 不得出现假演示数据`).not.toMatch(/\bCOMPANIES\b|\bCURRENT_USER\b|tenant-a|tenant-b/);
      expect(src, `${file} 不得出现裸 statusText 或 HTTP 状态拼接`).not.toMatch(/\bstatusText\b|HTTP\s*\$\{/);
      // 禁止把 JSON.stringify 塞进 UI 展示 state（仅允许 fetch body: JSON.stringify(...)）。
      const uiJsonDump = /set[A-Z]\w*\(\s*JSON\.stringify/.exec(src);
      expect(uiJsonDump, `${file} 命中 JSON.stringify UI 倾倒：${uiJsonDump?.[0] ?? ""}`).toBeNull();
      // 禁止把裸 (e as Error).message / err.message 不经 productError 直接写入提示状态。
      const rawErrorLeak = /set(?:Error|Err|Msg|Message|DraftMsg|Note)\(\s*(?:\(e as Error\)|e|err)\.message\s*\)/.exec(src);
      expect(rawErrorLeak, `${file} 命中未脱敏的原始异常消息：${rawErrorLeak?.[0] ?? ""}`).toBeNull();
      const rawTernaryLeak = /set(?:Error|Err|Msg|Message|DraftMsg|Note)\(\s*(?:e|err) instanceof Error \? (?:e|err)\.message/.exec(src);
      expect(rawTernaryLeak, `${file} 命中未脱敏的异常三元表达式：${rawTernaryLeak?.[0] ?? ""}`).toBeNull();
      // 禁止双层嵌套 <label><Input label=...>（破坏无障碍与表单布局）。
      expect(src, `${file} 不得把已带 label 的 Input 再包一层原生 <label>`).not.toMatch(/<label>\s*(?:[^<]*<br\s*\/>\s*)?<Input\s+label=/);
    });
  }

  it("每个正式页面至多声明一个主行动（data-page-primary=\"true\"）", () => {
    for (const file of FORMAL_ROUTES) {
      const count = source(file).split('data-page-primary="true"').length - 1;
      expect(count, `${file} 主行动数应 ≤ 1，实际 ${count}`).toBeLessThanOrEqual(1);
    }
  });
});

describe("HUI-2626 Today/Next 7 桶顺序、AI/人工/自动化视觉区分与租户隔离", () => {
  it("严格对齐 HUI-2598 的 7 桶工作队列顺序与中文标题", () => {
    expect(TODAY_GROUPS).toEqual([
      "new_inquiry",
      "due_today",
      "waiting_customer",
      "overdue",
      "ai_draft",
      "human_takeover",
      "info_or_permission",
    ]);
    expect(TODAY_GROUPS.map((g) => TODAY_LABELS[g])).toEqual([
      "新咨询待处理",
      "今天应跟进",
      "等待客户",
      "逾期",
      "AI 草稿待确认",
      "需要人工接管的会话",
      "信息缺失或权限异常",
    ]);
  });

  it("attentionSummary 按固定 7 桶顺序投影非空组，不乱序也不渲染空桶", () => {
    const mk = (id: string): DeskItem => ({ id, tenant_id: "t1", kind: "lead" });
    const summary = attentionSummary({
      overdue: [mk("d1")],
      new_inquiry: [mk("n1"), mk("n2")],
      ai_draft: [mk("a1")],
      info_or_permission: [mk("p1")],
    });
    expect(summary.total).toBe(5);
    expect(summary.rows.map((r) => [r.group, r.count])).toEqual([
      ["new_inquiry", 2],
      ["overdue", 1],
      ["ai_draft", 1],
      ["info_or_permission", 1],
    ]);
  });

  it("AI 建议、人工事实、自动化状态在渲染层有明确 data-tone 与中文语义区分", () => {
    expect(factTone({ kind: "ai_draft" })).toBe("ai");
    expect(factTone({ kind: "lead" })).toBe("human");
    expect(factTone({ kind: "lead", auto: true })).toBe("automation");

    const aiHtml = renderToStaticMarkup(withScope(<ToneBadge tone="ai" />));
    const humanHtml = renderToStaticMarkup(withScope(<ToneBadge tone="human" />));
    const autoHtml = renderToStaticMarkup(withScope(<ToneBadge tone="automation" />));

    expect(aiHtml).toContain('data-tone="ai"');
    expect(aiHtml).toContain("AI 建议");
    expect(humanHtml).toContain('data-tone="human"');
    expect(humanHtml).toContain("人工事实");
    expect(autoHtml).toContain('data-tone="automation"');
    expect(autoHtml).toContain("自动化状态");

    const frameHtml = renderToStaticMarkup(
      withScope(
        <RecordFrame
          width={1440}
          tone="ai"
          title="待确认的回复草稿"
          facts={[{ label: "为什么在今天", value: "AI 草稿待确认 · 待确认，不会自动发送" }]}
        />,
      ),
    );
    expect(frameHtml).toContain('data-tone="ai"');
    expect(frameHtml).toContain("AI 建议");
    expect(frameHtml).toContain('data-density="dense"');
  });

  it("切租户 A/B 时 acceptToday 严格丢弃其他租户记录", () => {
    const mixed = {
      new_inquiry: [
        { id: "a1", tenant_id: "tenant_alpha", kind: "lead" },
        { id: "b1", tenant_id: "tenant_beta", kind: "lead" },
      ],
      ai_draft: [{ id: "b2", tenant_id: "tenant_beta", kind: "ai_draft" }],
    };
    const alpha = acceptToday("tenant_alpha", mixed);
    expect(alpha.new_inquiry.map((i) => i.id)).toEqual(["a1"]);
    expect(alpha.ai_draft).toEqual([]);
    const beta = acceptToday("tenant_beta", mixed);
    expect(beta.new_inquiry.map((i) => i.id)).toEqual(["b1"]);
    expect(beta.ai_draft.map((i) => i.id)).toEqual(["b2"]);
  });

  it("所有状态面（loading/empty/error/recovery）均有非纯文字图形或骨架标识", () => {
    for (const kind of ["loading", "empty", "error", "recovery"] as const) {
      const html = renderToStaticMarkup(withScope(<SurfaceState kind={kind} />));
      expect(html).toContain(`data-surface-state="${kind}"`);
      expect(html).toContain(`data-state-mark="${kind}"`);
      if (kind === "loading") {
        expect(html).toContain('data-skeleton="block"');
      } else {
        expect(html).toContain("<svg");
      }
    }
  });
});

describe("HUI-2626 失败保留输入、抽屉/列表筛选与滚动连续性、租户状态重置", () => {
  it("listMemory 按租户隔离保存与恢复筛选条件和滚动位置", () => {
    let mem: ListMemory = {};
    mem = rememberListState(mem, "t_alpha", "leads", { filter: "in_progress", scrollY: 420.6 });
    mem = rememberListState(mem, "t_beta", "leads", { filter: "new", scrollY: 80 });

    expect(recallListState(mem, "t_alpha", "leads")).toEqual({ filter: "in_progress", scrollY: 421 });
    expect(recallListState(mem, "t_beta", "leads")).toEqual({ filter: "new", scrollY: 80 });
    // 未记录的租户或非法租户键返回空对象，绝不串出其他租户的筛选条件。
    expect(recallListState(mem, "t_gamma", "leads")).toEqual({});
    expect(recallListState(mem, "", "leads")).toEqual({});
    expect(recallListState(mem, "../bad", "leads")).toEqual({});
  });

  it("客户详情页追加跟进仅在服务端返回成功后清空输入（失败保留用户输入）", () => {
    const src = source("src/app/(shell)/contacts/[id]/page.tsx");
    expect(src).toMatch(/const post = async \(path: string, body: unknown\): Promise<boolean>/);
    expect(src).toContain('const ok = await post("/followups", { note: followupNote });');
    expect(src).toContain('if (ok) setFollowupNote("");');
    expect(src).not.toContain('await post("/followups", { note: followupNote }); setFollowupNote("");');
  });

  it("线索列表抽屉支持快速跟进、失败保留输入、时间线人化且不重置筛选与滚动", () => {
    const src = source("src/app/(shell)/leads/page.tsx");
    expect(src).toContain('data-drawer-followup="true"');
    expect(src).toContain("eventLine(event.kind, event.summary)");
    expect(src).toContain("humanTime(event.at)");
    expect(src).toContain("recallListState(readListMemory()");
    expect(src).toContain("rememberListState(readListMemory()");
    // 失败分支 return 前不得清空 drawerNote。
    const submitIdx = src.indexOf("const submitDrawerFollow");
    const submitBlock = src.slice(submitIdx, submitIdx + 1200);
    const failBranch = submitBlock.slice(submitBlock.indexOf("if (!out.ok)"), submitBlock.indexOf('setDrawerNote("")'));
    expect(failBranch).toContain("return;");
    expect(failBranch).not.toContain("setDrawerNote");
  });

  it("今日工作台草稿修订与忽略包裹 try/catch/finally 并带 saving 并发保护", () => {
    const src = source("src/app/(shell)/page.tsx");
    const reviseBlock = src.slice(src.indexOf("async function reviseDraft"), src.indexOf("async function ignoreDraft"));
    expect(reviseBlock).toContain("if (!headers || saving) return;");
    expect(reviseBlock).toContain("catch (e)");
    expect(reviseBlock).toContain("setDraftMsg(productError((e as Error).message))");
    expect(reviseBlock).toContain("finally");
    const ignoreBlock = src.slice(src.indexOf("async function ignoreDraft"), src.indexOf("if (!scope.tenant)"));
    expect(ignoreBlock).toContain("if (!headers || saving) return;");
    expect(ignoreBlock).toContain("catch (e)");
    expect(ignoreBlock).toContain("finally");
  });

  it("接待会话面板仅在跟进提交成功后清空输入，并区分 AI 草稿与人工回复", () => {
    const src = source("src/app/reception/session-panel.tsx");
    expect(src).toMatch(/async function run\(call: DeskCall \| null, refused: string\): Promise<boolean>/);
    expect(src).toContain("const ok = await run(followUpCall(");
    expect(src).toContain("if (ok) {");
    expect(src).toContain("<ToneBadge tone={turn.tone} />");
  });

  it("归因、订阅与附属面板在切租户时清空旧租户视图", () => {
    expect(source("src/app/(shell)/attribution/page.tsx")).toContain("setReport(null)");
    expect(source("src/app/(shell)/subscription/page.tsx")).toContain("setLoadedFor(null)");
    expect(source("src/components/MatrixDraftPanel.tsx")).toContain("setView(null)");
    expect(source("src/components/CampaignMotionPanel.tsx")).toContain("setRecorded(false)");
    expect(source("src/components/LightCopyPanel.tsx")).toContain("setDrafts([])");
    expect(source("src/components/IntentOnOpportunity.tsx")).toContain("setRows({})");
  });
});

describe("HUI-2626 显示层人化与 390/430/1440/1920 响应密度", () => {
  it("formatLightCopyFacts 把事实对象转成可读中文标签（无原始 JSON）", () => {
    const formatted = formatLightCopyFacts({
      activity_ref: "暑期到店礼",
      title: "短视频代运营",
      price_cents: 19900,
      empty_field: "",
    });
    expect(formatted).toEqual([
      { key: "activity_ref", label: "活动引用", value: "暑期到店礼" },
      { key: "title", label: "标题", value: "短视频代运营" },
      { key: "price_cents", label: "价格（分）", value: "¥199.00" },
      { key: "empty_field", label: "empty_field", value: "未记录" },
    ]);
    expect(formatLightCopyFacts(null)).toEqual([]);
  });

  it("memberDisplayLabel 隐藏 mem_* 与工程编号，只展示可读成员名", () => {
    expect(memberDisplayLabel("张销售", "mem_123")).toBe("张销售");
    expect(memberDisplayLabel("", "mem_123")).toBe("团队成员");
    expect(memberDisplayLabel("mem_456", "mem_456")).toBe("团队成员");
    expect(memberDisplayLabel("", "")).toBe("未记录");
  });

  it("桌面 1440/1920 与移动 390/430 响应式布局与抽屉朝向一致", () => {
    expect(shellStructure(390)).toMatchObject({ density: "compact", navigation: "bar", records: "cards", columns: 1, primaryCount: 1 });
    expect(shellStructure(430)).toMatchObject({ density: "compact", navigation: "bar", records: "cards", columns: 2, primaryCount: 1 });
    expect(shellStructure(1440)).toMatchObject({ density: "dense", navigation: "rail", records: "grid", columns: 4, primaryCount: 1 });
    expect(shellStructure(1920)).toMatchObject({ density: "dense", navigation: "rail", records: "grid", columns: 4, primaryCount: 1 });
    expect(rendererDensity(390)).toBe("touch");
    expect(rendererDensity(430)).toBe("touch");
    expect(rendererDensity(1440)).toBe("default");
    expect(drawerSide(390)).toBe("bottom");
    expect(drawerSide(430)).toBe("bottom");
    expect(drawerSide(1440)).toBe("right");
  });
});
