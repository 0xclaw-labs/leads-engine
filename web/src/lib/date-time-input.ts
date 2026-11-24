// 日期时间复合件纯函数口径（本地墙钟，不做 UTC 偏移）。
// datetime-local 值为 `YYYY-MM-DDTHH:mm`；date 值为 `YYYY-MM-DD`；空串表示未选。

const DATE_PART_RE = /^(\d{4})-(\d{2})-(\d{2})$/;
const TIME_PART_RE = /^(\d{2}):(\d{2})$/;
const DATETIME_RE = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/;

export function pad2(value: number): string {
  return String(value).padStart(2, "0");
}

export function parseDatePart(value: string): { year: number; month: number; day: number } | null {
  const match = DATE_PART_RE.exec(typeof value === "string" ? value : "");
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const date = new Date(year, month - 1, day);
  if (Number.isNaN(date.getTime())) return null;
  if (date.getFullYear() !== year || date.getMonth() !== month - 1 || date.getDate() !== day) return null;
  return { year, month, day };
}

export function isValidTimePart(value: string): boolean {
  const match = TIME_PART_RE.exec(typeof value === "string" ? value : "");
  if (!match) return false;
  const hour = Number(match[1]);
  const minute = Number(match[2]);
  return hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59;
}

export function isValidLocalDateTime(value: string): boolean {
  const match = DATETIME_RE.exec(typeof value === "string" ? value : "");
  if (!match) return false;
  const date = parseDatePart(`${match[1]}-${match[2]}-${match[3]}`);
  if (!date) return false;
  const hour = Number(match[4]);
  const minute = Number(match[5]);
  return hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59;
}

export function splitLocalDateTime(value: string): { datePart: string; timePart: string } | null {
  const match = DATETIME_RE.exec(typeof value === "string" ? value : "");
  if (!match) return null;
  const datePart = `${match[1]}-${match[2]}-${match[3]}`;
  const timePart = `${match[4]}:${match[5]}`;
  if (!parseDatePart(datePart) || !isValidTimePart(timePart)) return null;
  return { datePart, timePart };
}

export function combineLocalDateTime(datePart: string, timePart: string): string | null {
  if (!parseDatePart(datePart)) return null;
  if (!isValidTimePart(timePart)) return null;
  return `${datePart}T${timePart}`;
}

export function formatDateLabel(value: string): string {
  if (!parseDatePart(value)) return "选择日期";
  return value;
}

export function formatDateTimeLabel(value: string): string {
  if (!isValidLocalDateTime(value)) return "选择日期时间";
  const match = DATETIME_RE.exec(value)!;
  return `${match[1]}-${match[2]}-${match[3]} ${match[4]}:${match[5]}`;
}
