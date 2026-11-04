import { Monitor, Moon, Sun } from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useTheme } from "@/lib/theme";
import { cn } from "@/lib/utils";

/** BrandMark: a path that leaves and comes back — traffic taking the detour. */
export function BrandMark({ className }) {
  return (
    <span className={cn("grid size-9 shrink-0 place-items-center rounded-[11px] bg-primary text-primary-foreground", className)}>
      <svg viewBox="0 0 24 24" className="size-[58%]" fill="none" aria-hidden="true">
        <path d="M4 7h9a4.5 4.5 0 0 1 0 9H9" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" />
        <path d="M12 12.5 8.5 16l3.5 3.5" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </span>
  );
}

const THEMES = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
];

export function ThemeSwitch({ className }) {
  const { theme, setTheme } = useTheme();
  return (
    <div role="radiogroup" aria-label="Theme" className={cn("flex items-center gap-0.5 rounded-[11px] bg-secondary p-1", className)}>
      {THEMES.map(({ value, label, icon: Icon }) => (
        <Tooltip key={value}>
          <TooltipTrigger asChild>
            <button
              type="button"
              role="radio"
              aria-checked={theme === value}
              aria-label={`${label} theme`}
              onClick={() => setTheme(value)}
              className={cn(
                "grid h-8 flex-1 place-items-center rounded-lg px-2.5 transition-colors",
                theme === value ? "bg-card text-foreground shadow-[0_1px_2px_rgb(0_0_0/0.08)]" : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Icon className="size-4" />
            </button>
          </TooltipTrigger>
          <TooltipContent>{label}</TooltipContent>
        </Tooltip>
      ))}
    </div>
  );
}
