import { cn } from "@/lib/utils";

/** Page frames every screen: consistent width, spacing and heading rhythm. */
export function Page({ className, children }) {
  return (
    <div className={cn("mx-auto w-full max-w-[1240px] px-5 pb-20 pt-8 sm:px-8 sm:pt-10", className)}>
      {children}
    </div>
  );
}

export function PageHeader({ title, description, actions, className }) {
  return (
    <header className={cn("mb-8 sm:mb-10", className)}>
      <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
        <div className="min-w-0 max-w-3xl">
          <h1 className="display text-2xl font-semibold leading-tight sm:text-3xl">{title}</h1>
          {description ? <div className="mt-2.5 text-base text-muted-foreground sm:text-md">{description}</div> : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2.5">{actions}</div> : null}
      </div>
    </header>
  );
}

export function Section({ title, description, action, className, children }) {
  return (
    <section className={cn("space-y-4", className)}>
      {(title || action) && (
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div className="min-w-0">
            {title ? <h2 className="display text-lg font-semibold">{title}</h2> : null}
            {description ? <p className="mt-1 text-sm text-muted-foreground">{description}</p> : null}
          </div>
          {action}
        </div>
      )}
      {children}
    </section>
  );
}

export function Mono({ className, children, ...props }) {
  return (
    <span className={cn("font-mono text-[0.8125rem] break-all", className)} {...props}>
      {children}
    </span>
  );
}

/** Empty is the calm placeholder for a list with nothing in it yet. */
export function Empty({ icon: Icon, title, children, action, className }) {
  return (
    <div className={cn("flex flex-col items-center px-6 py-14 text-center", className)}>
      {Icon ? (
        <span className="mb-4 grid size-12 place-items-center rounded-2xl bg-secondary text-muted-foreground">
          <Icon className="size-6" />
        </span>
      ) : null}
      <p className="display text-md font-semibold">{title}</p>
      {children ? <div className="mt-2 max-w-md text-sm text-muted-foreground">{children}</div> : null}
      {action ? <div className="mt-5">{action}</div> : null}
    </div>
  );
}

/** Dot is a small status light; tone uses the reserved status colours only. */
export function Dot({ tone = "neutral", pulse, className }) {
  const color = {
    success: "bg-success",
    warning: "bg-warning",
    destructive: "bg-destructive",
    running: "bg-running",
    neutral: "bg-muted-foreground/50",
  }[tone];
  return (
    <span className={cn("relative inline-flex size-2.5 shrink-0", className)}>
      {pulse ? <span className={cn("absolute inline-flex size-full animate-ping rounded-full opacity-40", color)} /> : null}
      <span className={cn("relative inline-flex size-2.5 rounded-full", color)} />
    </span>
  );
}

/** Segmented is a small single-choice control (time ranges, filters). */
export function Segmented({ value, onChange, options, className, size = "md" }) {
  return (
    <div role="radiogroup" className={cn("inline-flex items-center gap-0.5 rounded-[11px] bg-secondary p-1", className)}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          onClick={() => onChange(o.value)}
          className={cn(
            "rounded-lg font-medium transition-colors",
            size === "sm" ? "h-7 px-2.5 text-xs" : "h-8 px-3 text-sm",
            value === o.value ? "bg-card text-foreground shadow-[0_1px_2px_rgb(0_0_0/0.08)]" : "text-muted-foreground hover:text-foreground",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** StatTile: label, big value, optional hint line. */
export function StatTile({ label, value, hint, icon: Icon, swatch, className }) {
  return (
    <div className={cn("rounded-2xl border border-border bg-card px-5 py-4", className)}>
      <div className="flex items-center justify-between gap-3 text-sm text-muted-foreground">
        <span className="flex items-center gap-2">
          {swatch ? <span className="size-2.5 rounded-[3px]" style={{ background: swatch }} /> : null}
          {label}
        </span>
        {Icon ? <Icon className="size-4 opacity-70" /> : null}
      </div>
      <div className="display mt-2 text-2xl font-semibold tabular">{value}</div>
      {hint ? <div className="mt-1 text-xs text-muted-foreground">{hint}</div> : null}
    </div>
  );
}
