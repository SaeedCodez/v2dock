import { cn } from "@/lib/utils";

function Skeleton({ className, ...props }) {
  return <div className={cn("animate-pulse rounded-xl bg-muted", className)} {...props} />;
}

export { Skeleton };
