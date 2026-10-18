import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import type { ExecutionPolicy } from "@/lib/mx-ops/contract";
import { executionOptions, FEE_DISCLOSURE } from "@/lib/mx-ops/execution";
import { presentCampaignProduction, validateHandoffFacts } from "@/lib/campaignMotionQueue";

const fixtures = path.join(process.cwd(), "tests", "fixtures", "mx-ops");

function fixture(name: string): unknown {
  return JSON.parse(readFileSync(path.join(fixtures, name), "utf8"));
}

describe("Campaign 制作等待回流（MX-OPS）", () => {
  it("云端等待与人工等待分开，GPU 忙不阻塞 CRM", () => {
    const cloud = presentCampaignProduction({
      campaign_id: "camp_1",
      job_queue_view: fixture("job-queue-view.waiting.json"),
      channel_count: 3,
      model_calls: 1,
    });
    expect(cloud.waitKind).toBe("cloud");
    expect(cloud.waitLabel).toContain("等待云端产物");
    expect(cloud.waitLabel).toContain("不影响销售跟进");
    expect(cloud.queue?.etaText).toBe("暂不可估");

    const human = presentCampaignProduction({ campaign_id: "camp_1", human_followup_pending: true });
    expect(human.waitKind).toBe("human");
    expect(human.waitLabel).toContain("销售跟进");
    expect(human.waitLabel).toContain("GPU 忙不影响");
    expect(human.etaText).toBe("暂不可估");
  });

  it("没有队列投影时 UNKNOWN，不猜 ETA", () => {
    const view = presentCampaignProduction({ campaign_id: "camp_1" });
    expect(view.queue).toBeNull();
    expect(view.waitKind).toBe("unknown");
    expect(view.etaText).toBe("暂不可估");
    expect(view.waitLabel).not.toContain("马上");
  });

  it("夜窗晚于活动截止：提示赶不上，不自动升级付费", () => {
    const base = fixture("job-queue-view.waiting.json") as Record<string, unknown>;
    const withWindow = { ...base, next_eligible_at_unix: 1780003600 };
    const view = presentCampaignProduction({
      campaign_id: "camp_1",
      job_queue_view: withWindow,
      campaign_deadline_unix: 1779990000,
    });
    expect(view.windowAfterDeadline).toBe(true);
    expect(view.deadlineWarning).toContain("赶不上");
    expect(view.deadlineWarning).toContain("不会自动升级付费");
  });

  it("N 个渠道不产生 N 次生成：费用只归因一次", () => {
    const view = presentCampaignProduction({
      campaign_id: "camp_1",
      creative_id: "creative_9",
      variant: "douyin",
      channel_count: 3,
      model_calls: 1,
      model_cost_minor: 980,
      payer_kind: "campaign",
    });
    expect(view.costs.modelCalls).toBe(1);
    expect(view.costs.channelCount).toBe(3);
    expect(view.costs.modelCostText).toBe("¥9.80");
    expect(view.costs.note).toContain("不产生 3 次高价生成");
    expect(view.costs.payerText).toContain("活动预算");
    expect(view.feeDisclosure).toContain("唯一收费");
    expect(view.feeDisclosure).toBe(FEE_DISCLOSURE);
  });

  it("队列完成不自动外发/触达/记录转化", () => {
    const view = presentCampaignProduction({ campaign_id: "camp_1", job_queue_view: fixture("job-queue-view.measured.json") });
    expect(view.autoOutreach).toBe(false);
    expect(view.autoConversion).toBe(false);
    expect(view.notice).toContain("不会自动触达客户");
    expect(view.notice).toContain("不会自动");
  });
});

describe("Campaign→专业工具最小合法事实", () => {
  it("只允许最小事实清单", () => {
    const result = validateHandoffFacts({
      campaign_goal: "春季上新",
      target_audience: "门店店主",
      approved_facts: [{ key: "product", value: "冷萃" }],
      authorized_asset_ids: ["asset_1"],
      cta: "到店试喝",
      channels: ["抖音", "视频号"],
      budget_attribution_id: "budget_1",
      payer: "org_1",
      execution_preference: "night_batch",
      motion: { project_id: "prj", revision_id: "rev" },
    });
    expect(result.ok).toBe(true);
  });

  it("CRM 私密字段默认不出域", () => {
    for (const field of ["chat", "contact", "phone", "leads", "customer_name", "privacy"]) {
      const result = validateHandoffFacts({ campaign_goal: "x", [field]: "secret" });
      expect(result.ok, field).toBe(false);
      if (!result.ok) {
        expect(result.code).toBe("crm_private_field");
        expect(result.field).toBe(field);
      }
    }
  });

  it("清单外字段不交出", () => {
    const result = validateHandoffFacts({ campaign_goal: "x", renderer: "evil" });
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.code).toBe("unknown_fact_field");
  });
});

describe("偏好选项只从允许模板取交集（留待真实策略接入）", () => {
  const policy = JSON.parse(readFileSync(path.join(fixtures, "execution-policy.night_batch.json"), "utf8")) as ExecutionPolicy;

  it("夜间模板在允许列表且有窗口时可用；否则不可用并给原因", () => {
    const options = executionOptions({ policies: [policy], entitlements: [], quoteConfirmed: false, nowUnix: 1780000000 });
    const night = options.find((item) => item.template === "night_batch");
    expect(night?.available).toBe(true);
    const withoutWindow = executionOptions({
      policies: [{ ...policy, time_window: null }],
      entitlements: [],
      quoteConfirmed: false,
      nowUnix: 1780000000,
    });
    expect(withoutWindow.find((item) => item.template === "night_batch")?.available).toBe(false);
  });
});

describe("面板接入（源码契约）", () => {
  const panel = readFileSync(path.join(process.cwd(), "src/components/CampaignMotionPanel.tsx"), "utf8");

  it("展示等待/ETA/费用归因/CRM 边界，且不自动触达", () => {
    expect(panel).toContain("presentCampaignProduction");
    expect(panel).toContain("waitLabel");
    expect(panel).toContain("etaText");
    expect(panel).toContain("costs.note");
    expect(panel).toContain("crmNotice");
    expect(panel).toContain("autoOutreach");
  });
});
