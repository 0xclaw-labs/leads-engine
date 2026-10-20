// HUI-2626 盲评 P2③/P3：时间线与记录卡的显示层人化。
// 只改「画出来」的文案：工程标识符映射/退场、微秒 ISO 变本地秒级；
// 原始值保留在接口与数据层，这里不做任何数据改写。

/** 已知授权项 → 人话标签（服务端 consent.channel 的枚举值）。 */
export const CONSENT_CHANNEL_LABELS: Record<string, string> = {
  form_authorization: "表单授权",
  channel_reply: "渠道内回复",
  marketing_phone: "电话营销",
  marketing_sms: "短信营销",
};

const KIND_LABELS: Record<string, string> = {
  source: "来源",
  assignment: "分配",
  follow_up: "跟进",
  opportunity: "商机",
  consent: "授权",
  system: "系统",
};

export function eventKindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind;
}

/** 工程引用：纯 ASCII 标识符且含数字（cmp_1、sub_walk_b1、contact_form_v2）。 */
export function isEngineeringRef(token: string): boolean {
  return /^[A-Za-z][A-Za-z0-9_-]*$/.test(token) && /\d/.test(token);
}

function mapConsentTokens(text: string): string {
  let out = text;
  for (const [raw, human] of Object.entries(CONSENT_CHANNEL_LABELS)) {
    out = out.replace(new RegExp(`\\b${raw}\\b`, "g"), human);
  }
  // 「撤销授权 电话营销」→「撤销电话营销授权」，读起来是一句话。
  const revoked = /^撤销授权\s+(.+)$/.exec(out);
  if (revoked) out = `撤销${revoked[1]}授权`;
  return out;
}

/**
 * 时间线一行文案：`类别 · 人话摘要`。
 * - 摘要以类别词开头时不再重复（盲评 P3「授权 · 授权 xxx」）；
 * - 已知授权项映射成人话；
 * - 「表单/活动/提交 <工程引用>」段从展示退场（原始值留在数据层）。
 */
export function humanSummary(summary: string): string {
  let text = (summary ?? "").trim();
  const wasRevoked = /^撤销授权\s+/.test(text);
  text = mapConsentTokens(text);
  const kept = text
    .split(" · ")
    .map((seg) => seg.trim())
    .filter(Boolean)
    .filter((seg) => {
      const m = /^(表单|活动|提交)\s+(.+)$/.exec(seg);
      return !(m && isEngineeringRef(m[2]));
    });
  text = kept.join(" · ");
  return wasRevoked ? `撤销${text.replace(/^撤销/, "")}` : text;
}

export function eventLine(kind: string, summary: string): string {
  const label = eventKindLabel(kind);
  let text = (summary ?? "").trim();
  if (label && text.startsWith(`${label} `)) text = text.slice(label.length + 1).trim();
  if (text === label) text = "";
  const wasRevoked = /^撤销授权\s+/.test(text);
  text = humanSummary(text);
  if (!text) return label;
  // 授权事件读成一句话：「渠道内回复授权」；映射标签已带「授权」尾词则不重复。
  if (kind === "consent" && !wasRevoked && !text.endsWith("授权")) text = `${text}授权`;
  return `${label} · ${text}`;
}

/**
 * 时间戳人化：ISO（含微秒/时区）→ 本地时区秒级 `YYYY-MM-DD HH:mm:ss`。
 * 解析失败或为空时原样/空返回，绝不抛错。
 */
export function humanTime(raw?: string | null): string {
  const text = (raw ?? "").trim();
  if (!text) return "";
  const looksLike = /^\d{4}-\d{2}-\d{2}[Tt ]\d{2}:\d{2}/.test(text);
  if (!looksLike) return text;
  const date = new Date(text);
  if (Number.isNaN(date.getTime())) return text;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

/** 记录卡标题：按来源人化，不再是占位的「这条线索」。 */
export function leadTitle(source?: { form?: string; activity?: string; channel?: string } | null): string {
  const channel = (source?.channel ?? "").trim();
  if (channel) return `${channel}线索`;
  if ((source?.form ?? "").trim()) return "表单线索";
  if ((source?.activity ?? "").trim()) return "碰一碰线索";
  return "线索档案";
}
