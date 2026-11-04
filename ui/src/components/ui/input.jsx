import * as React from "react";
import { cn } from "@/lib/utils";

const Input = React.forwardRef(({ className, type, ...props }, ref) => (
  <input
    type={type}
    ref={ref}
    className={cn(
      "flex h-10 w-full rounded-[10px] border border-input bg-card px-3.5 text-base transition-[border-color,box-shadow]",
      "placeholder:text-muted-foreground/70",
      "focus-visible:border-ring focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-ring/15",
      "disabled:cursor-not-allowed disabled:opacity-50",
      "aria-invalid:border-destructive aria-invalid:ring-destructive/20",
      className,
    )}
    {...props}
  />
));
Input.displayName = "Input";

export { Input };
