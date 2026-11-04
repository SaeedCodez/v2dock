import * as React from "react";
import { cva } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium whitespace-nowrap",
  {
    variants: {
      variant: {
        default: "bg-secondary text-foreground",
        secondary: "bg-secondary text-muted-foreground",
        outline: "border border-border text-muted-foreground",
        success: "bg-success/12 text-success",
        warning: "bg-warning/14 text-warning",
        destructive: "bg-destructive/12 text-destructive",
        running: "bg-running/12 text-running",
      },
    },
    defaultVariants: { variant: "default" },
  },
);

function Badge({ className, variant, ...props }) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { Badge, badgeVariants };
