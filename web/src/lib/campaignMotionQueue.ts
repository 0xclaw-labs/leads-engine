// HUI-2747 MX-OPS 增量：Campaign→专业工具的等待回流与费用归因展示。
//
// 纪律：
//   - 只展示权威 job_queue_view；没有投影 = UNKNOWN，不给伪进度；
//   - 区分「等待云端产物」与「等待销售跟进/人工审核」；GPU 忙不阻塞 CRM；
//   - 费用归因到 campaign/creative/variant，参数化 Variant 不按渠道数线性生成；
//   - 队列完成不自动触达/发布/记录转化；
//   - CRM 私密字段（聊天/联系人/电话/线索）默认不出域。

import { presentQueue, readJobQueueView, type MerchantQueue } from "@/lib/mx-ops/queue";
import { FEE_DISCLOSURE, type PayerKind } from "@/lib/mx-ops/execution";

export interface CampaignProductionInput {
  campaign_id?: string;
  creative_id?: string;
  variant?: string;
  job_queue_view?: unknown;
  human_followup_pending?: boolean;
  model_calls?: number;
  channel_count?: number;
  model_cost_minor?: number | null;
  currency?: "CNY";
  payer_kind?: string;
  campaign_deadline_unix?: number | null;
}

export type CampaignWaitKind = "cloud" | "window" | "human" | "none" | "unknown";

export interface CampaignProductionView {
  queue: MerchantQueue | null;
  waitKind: CampaignWaitKind;
  waitLabel: string;
  etaText: string;
  nextWindowText: string | null;
  policyVersion: number | null;
  windowAfterDeadline: boolean;
  deadlineWarning: string | null;
  costs: {
    campaignId: string | null;
    creativeId: string | null;
    variant: string | null;
    modelCalls: number;
    channelCount: number;
    modelCostText: string;
    payerText: string;
    note: string;
  };
  autoOutreach: false;
  autoConversion: false;
  crmNotice: string;
  feeDisclosure: string;
  notice: string;
}

const PAYERS: PayerKind[] = ["user", "platform_subsidy", "internal", "campaign"];

function payerLabel(value: string | undefined): string {
  const payer = PAYERS.find((item) => item === value);
  if (!payer) return "费用承担未提供";
  switch (payer) {
    case "user":
      return "由你承担（按确认报价）";
    case "platform_subsidy":
      return "平台补贴承担（不等于内部研发卡）";
    case "campaign":
      return "活动预算承担";
    case "internal":
      return "内部研发承担，不对商家开放";
  }
}

function formatMinor(minor: number | null | undefined): string {
  if (minor === null || minor === undefined || !Number.isFinite(minor)) return "未提供";
  return `¥${Math.floor(Math.abs(Math.round(minor)) / 100)}.${String(Math.abs(Math.round(minor)) % 100).padStart(2, "0")}`;
}

export function presentCampaignProduction(input: CampaignProductionInput = {}): CampaignProductionView {
  const queue = presentQueue(readJobQueueView(input.job_queue_view));
  const channelCount = Math.max(0, Math.trunc(input.channel_count ?? 0));
  const modelCalls = Math.max(0, Math.trunc(input.model_calls ?? 0));
  const deadline = input.campaign_deadline_unix ?? null;
  const nextEligible = queue?.nextEligibleAtUnix ?? null;
  const windowAfterDeadline = nextEligible !== null && deadline !== null && nextEligible > deadline;

  let waitKind: CampaignWaitKind;
  if (queue) {
    switch (queue.waitKind) {
      case "none":
        waitKind = "none";
        break;
      case "window":
        waitKind = "window";
        break;
      case "unknown":
        waitKind = "unknown";
        break;
      default:
        waitKind = "cloud";
    }
  } else {
    waitKind = input.human_followup_pending ? "human" : "unknown";
  }

  const waitLabel =
    waitKind === "cloud"
      ? "等待云端产物（不影响销售跟进）"
      : waitKind === "window"
        ? "等待可执行时间窗"
        : waitKind === "human"
          ? "等待销售跟进或人工审核（GPU 忙不影响）"
          : waitKind === "none"
            ? "云端进行中"
            : "等待状态未知";

  return {
    queue,
    waitKind,
    waitLabel,
    etaText: queue?.etaText ?? "暂不可估",
    nextWindowText: queue?.nextEligibleText ?? null,
    policyVersion: queue?.policyVersion ?? null,
    windowAfterDeadline,
    deadlineWarning: windowAfterDeadline
      ? "下一云端窗口晚于活动截止时间，预计赶不上；不会自动升级付费或改活动日期。"
      : null,
    costs: {
      campaignId: (input.campaign_id ?? "").trim() || null,
      creativeId: (input.creative_id ?? "").trim() || null,
      variant: (input.variant ?? "").trim() || null,
      modelCalls,
      channelCount,
      modelCostText: formatMinor(input.model_cost_minor),
      payerText: payerLabel(input.payer_kind),
      note:
        channelCount > 1
          ? `参数化 Variant：${channelCount} 个渠道不产生 ${channelCount} 次高价生成；模型调用只记 ${modelCalls} 次。`
          : `模型调用只记 ${modelCalls} 次；费用可归因到活动。`,
    },
    autoOutreach: false,
    autoConversion: false,
    crmNotice: "CRM 私密聊天、联系人、电话与未授权线索默认不出域；只交出已确认的活动事实。",
    feeDisclosure: FEE_DISCLOSURE,
    notice: "队列完成只记回活动卡；不会自动触达客户、发布内容或记录转化。",
  };
}

// CRM 私密键：客户端在提交前先拦截，服务端另有白名单兜底。
const PRIVATE_KEY = /(phone|mobile|tel|email|chat|message|transcript|conversation|lead|contact|customer|name|address|wechat|id_card|privacy|crm|note)/i;

const ALLOWED_FACT_KEYS = new Set([
  "campaign_goal",
  "target_audience",
  "approved_facts",
  "authorized_asset_ids",
  "cta",
  "channels",
  "budget_attribution_id",
  "payer",
  "execution_preference",
  "motion",
]);

export type FactsResult =
  | { ok: true; facts: Record<string, unknown> }
  | { ok: false; code: "crm_private_field" | "unknown_fact_field"; field: string; message: string };

export function validateHandoffFacts(input: Record<string, unknown>): FactsResult {
  for (const key of Object.keys(input)) {
    if (PRIVATE_KEY.test(key)) {
      return {
        ok: false,
        code: "crm_private_field",
        field: key,
        message: `「${key}」属于 CRM 私密字段，默认不出域；只交出已确认的活动事实。`,
      };
    }
    if (!ALLOWED_FACT_KEYS.has(key)) {
      return {
        ok: false,
        code: "unknown_fact_field",
        field: key,
        message: `「${key}」不在最小合法事实清单里，不交给专业工具。`,
      };
    }
  }
  return { ok: true, facts: input };
}
