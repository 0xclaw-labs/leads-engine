"use client";

// 日期/时间标准件（stock 层）：Popover + Calendar 选日期；datetime-local 类型
// 在浮层内保留显式时间槽（原生 type="time" 作为复合件内时间输入）。
// 值口径与原原生控件一致：datetime-local → `YYYY-MM-DDTHH:mm`；date → `YYYY-MM-DD`；
// 空串表示未选。页面提交链路读取受控值的口径不变。

import React from "react";
import { CalendarIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/workbench/calendar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/workbench/popover";
import {
  combineLocalDateTime,
  formatDateLabel,
  formatDateTimeLabel,
  isValidLocalDateTime,
  parseDatePart,
  splitLocalDateTime,
} from "@/lib/date-time-input";

export interface DateTimeFieldProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
  type?: "datetime-local" | "date";
  id?: string;
  disabled?: boolean;
}

export function DateTimeField({
  label,
  value,
  onChange,
  type = "datetime-local",
  id,
  disabled,
}: DateTimeFieldProps) {
  const [open, setOpen] = React.useState(false);
  const parts = type === "datetime-local" ? splitLocalDateTime(value) : null;
  const datePart = parts?.datePart ?? (type === "date" && parseDatePart(value) ? value : "");
  const timePart = parts?.timePart ?? "";
  const selected = datePart ? parseDatePart(datePart) : null;
  const selectedDate = selected ? new Date(selected.year, selected.month - 1, selected.day) : undefined;

  const handleSelect = (day: Date | undefined) => {
    if (!day) return;
    const dayText = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`;
    if (type === "date") {
      onChange(dayText);
      return;
    }
    const next = combineLocalDateTime(dayText, timePart || "00:00");
    if (next) onChange(next);
  };

  const handleTimeChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    const next = combineLocalDateTime(datePart, event.currentTarget.value);
    if (next) onChange(next);
  };

  const triggerLabel = type === "date" ? formatDateLabel(value) : formatDateTimeLabel(value);

  return (
    <label className="dt-field">
      <span>{label}</span>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            type="button"
            id={id}
            variant="outline"
            size="sm"
            disabled={disabled}
            aria-label={label}
            aria-expanded={open}
            aria-haspopup="dialog"
            className="w-full justify-start gap-2 text-left font-normal"
          >
            <CalendarIcon className="h-4 w-4 shrink-0" />
            <span className="truncate">{triggerLabel}</span>
          </Button>
        </PopoverTrigger>
        <PopoverContent align="start" sideOffset={4} className="w-auto p-0">
          <div className="flex max-h-[calc(100dvh-1.5rem)] w-[min(320px,calc(100vw-24px))] flex-col gap-3 overflow-y-auto p-3">
            <Calendar
              mode="single"
              showOutsideDays
              selected={selectedDate}
              onSelect={handleSelect}
              className="px-0"
            />
            {type === "datetime-local" ? (
              <div className="flex flex-col gap-2">
                <label className="flex items-center gap-2 text-sm text-muted-foreground">
                  时间
                  <input
                    type="time"
                    className="flex h-8 w-[120px] rounded-md border border-input bg-transparent px-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                    value={timePart}
                    disabled={disabled}
                    onChange={handleTimeChange}
                  />
                </label>
              </div>
            ) : null}
            <div className="flex justify-end gap-2">
              <Button type="button" variant="ghost" size="sm" onClick={() => setOpen(false)}>
                取消
              </Button>
              <Button type="button" size="sm" onClick={() => setOpen(false)}>
                应用
              </Button>
            </div>
          </div>
        </PopoverContent>
      </Popover>
    </label>
  );
}

// 保留对旧类型枚举的引用，避免调用方误传；实际控件内不再渲染裸原生日期输入。
export type DateTimeFieldType = "datetime-local" | "date";

// 供测试与源码约束引用：当前值是否为合法本地日期时间串。
export function isValidFieldValue(value: string, type: "datetime-local" | "date"): boolean {
  return type === "date" ? parseDatePart(value) !== null : isValidLocalDateTime(value);
}
