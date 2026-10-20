// HUI-2626 finish-r1：列表 + 抽屉共用的同义层。口径统一、不发明新语义。
import { attributionText } from "@/lib/workbench";
// money 已由 lib/workbench centsText 统一；这里补 consent/status/source 词汇与抽屉朝向。

export const CONSENT_TEXT: Record<string, string> = {
  pending: "待确认",
  granted: "已同意",
  denied: "已拒绝",
};

// 档案级粗粒度授权的同义词。未知值原样透出（保留服务端真实含义），空值明确写「未记录」。
export function consentText(status: string | null | undefined): string {
  const raw = (status ?? "").trim();
  if (!raw) return "未记录";
  return CONSENT_TEXT[raw] ?? raw;
}

export const LEAD_STATUS_TEXT: Record<string, string> = {
  new: "新线索",
  in_progress: "跟进中",
  converted: "已转化",
  closed: "已关闭",
  filtered: "已过滤",
};

export const LEAD_SOURCE_TEXT: Record<string, string> = {
  manual: "手工录入",
  form: "自有表单",
  touch_campaign: "碰一碰",
};

export function leadStatusText(status: string | null | undefined): string {
  const raw = (status ?? "").trim();
  if (!raw) return "未记录";
  return LEAD_STATUS_TEXT[raw] ?? raw;
}

export function leadSourceText(source: string | null | undefined): string {
  const raw = (source ?? "").trim();
  if (!raw) return "未知来源";
  return LEAD_SOURCE_TEXT[raw] ?? raw;
}

// 抽屉朝向：桌面（≥760px）右侧抽屉，窄屏底部详情层（票面 A 组：移动端适合窄屏的详情层）。
export function drawerSide(width: number): "right" | "bottom" {
  return width >= 760 ? "right" : "bottom";
}

export const CONTACT_CATEGORY_TEXT: Record<string, string> = {
  merchant_customer: "商家经营销售",
  creative_service: "创意服务",
};

export const CONTACT_SOURCE_TEXT: Record<string, string> = {
  manual: "手工录入",
  form: "自有表单",
  touch_campaign: "碰一碰",
};

export interface ContactListRow {
  id: string;
  name: string;
  business_category: string;
  source_type: string;
  consent_status: string;
  tags: string;
  assigned_member_id?: string;
}

// 客户档案行 facts（列表与抽屉共用）：类别/来源/授权/标签/负责人，同义层口径。
export function contactRowFacts(row: ContactListRow): { label: string; value: string }[] {
  const tags = parseLocalTagList(row.tags);
  return [
    { label: "类别", value: CONTACT_CATEGORY_TEXT[row.business_category] ?? row.business_category },
    { label: "来源", value: CONTACT_SOURCE_TEXT[row.source_type] ?? row.source_type },
    { label: "授权", value: consentText(row.consent_status) },
    { label: "标签", value: tags.length > 0 ? tags.join("/") : "无标签" },
    {
      label: "负责人",
      value: row.assigned_member_id ? row.assigned_member_id : "待分配（还没有指定负责人）",
    },
  ];
}

function parseLocalTagList(tags: string | null | undefined): string[] {
  return (tags ?? "")
    .split(",")
    .map((t) => t.trim())
    .filter(Boolean);
}

export interface LeadSourceFacts {
  form?: string;
  activity?: string;
  channel?: string;
  at?: string;
  source_app?: string;
}

export interface LeadListRow {
  id: string;
  status: string;
  /** 原始来源枚举（manual/form/touch_campaign），优先使用。 */
  source_type?: string;
  /** HUI-2598：服务端列表投影的来源；仍走 sourceText 的人话层。 */
  source?: LeadSourceFacts | string;
  assigned_member_id?: string;
  /** 服务端投影的可读负责人；永远不是 mem_* 工程 id。 */
  owner_label?: string;
  /** 服务端投影的分配说明：待分配 / 已分配给 X / 负责人已停用。 */
  assignment_reason?: string;
  contact_name?: string;
}

export function leadOwnerFacts(row: LeadListRow): string {
  const reason = (row.assignment_reason ?? "").trim();
  const label = (row.owner_label ?? "").trim();
  if (reason === "待分配" || (!reason && !label)) {
    return "待分配（还没有负责人，先分配后才能持续跟进）";
  }
  if (label && reason === `已分配给 ${label}`) return label;
  if (label && reason && reason !== "待分配") return `${label} · ${reason}`;
  return label || reason || "负责人信息待同步";
}

// 线索行 facts（列表与抽屉共用）：来源/状态/负责人，同义层口径。
// 未分配的线索明确写「待分配」，不把空负责人伪装成已分派；
// 负责人只显示成员名，不回退到 mem_* 工程 id。
export function leadRowFacts(row: LeadListRow): { label: string; value: string }[] {
  const source = typeof row.source === "string"
    ? leadSourceText(row.source)
    // 列表是扫描面：渠道在前、活动编号如实标注，不把每行都变成时间戳。
    : attributionText(row.source ? { form: row.source.form, activity: row.source.activity, channel: row.source.channel } : undefined) || leadSourceText(row.source_type);
  return [
    { label: "来源", value: source },
    { label: "状态", value: leadStatusText(row.status) },
    { label: "负责人", value: leadOwnerFacts(row) },
  ];
}
