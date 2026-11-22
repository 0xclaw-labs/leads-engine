"use client";

import * as React from "react";
import { cn } from "@/lib/utils";
import { Label } from "@/components/ui/label";

// 轻量 Checkbox 原语：封装原生 checkbox（零额外依赖），
// 对外暴露与 Radix Checkbox 一致的 checked / onCheckedChange 语义。
// 样式走 ui token（border-border / accent-primary），不引入裸 hex。

export interface CheckboxProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "onChange" | "checked"> {
  checked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
  /** 可见字段名；传入后原语渲染关联 `<Label htmlFor>`，控件 id 自动生成或沿用调用方传入值。 */
  label?: string;
}

const Checkbox = React.forwardRef<HTMLInputElement, CheckboxProps>(
  ({ className, checked, onCheckedChange, disabled, label, id, ...props }, ref) => {
    const autoId = React.useId();
    const controlId = id ?? (label ? autoId : undefined);
    const control = (
      <input
        ref={ref}
        type="checkbox"
        id={controlId}
        checked={checked}
        disabled={disabled}
        onChange={(event) => onCheckedChange?.(event.target.checked)}
        className={cn(
          "h-4 w-4 shrink-0 rounded border border-border accent-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50",
          className,
        )}
        {...props}
      />
    );
    if (!label) return control;
    return (
      <Label htmlFor={controlId} className="flex items-center gap-2 font-normal">
        {control}
        <span>{label}</span>
      </Label>
    );
  },
);
Checkbox.displayName = "Checkbox";

export { Checkbox };
