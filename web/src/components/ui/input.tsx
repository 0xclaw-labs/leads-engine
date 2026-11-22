import * as React from "react"

import { cn } from "@/lib/utils"
import { Label } from "@/components/ui/label"

export interface InputProps
  extends Omit<React.ComponentProps<"input">, "label"> {
  /** 可见字段名；传入后原语渲染关联 `<Label htmlFor>`，控件 id 自动生成或沿用调用方传入值。 */
  label?: string
}

const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, type, label, id, ...props }, ref) => {
    const autoId = React.useId()
    const controlId = id ?? (label ? autoId : undefined)
    const control = (
      <input
        type={type}
        id={controlId}
        aria-invalid={props["aria-invalid"]}
        className={cn(
          "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-base shadow-sm transition-colors file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
          className
        )}
        ref={ref}
        {...props}
      />
    )
    if (!label) return control
    return (
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={controlId}>{label}</Label>
        {control}
      </div>
    )
  }
)
Input.displayName = "Input"

export { Input }
