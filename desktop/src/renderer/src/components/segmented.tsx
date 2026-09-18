import { cn } from "@/lib/utils";

// Segmented is the control for switching a view: a period, a projection
// horizon, a source.
export function Segmented<T extends string | number>({
  label,
  value,
  options,
  onChange,
  size = "default",
  className,
}: {
  label: string;
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
  size?: "default" | "sm";
  className?: string;
}) {
  return (
    <div role="group" aria-label={label} className={cn("flex shrink-0 gap-px rounded-[4px] border border-line-strong bg-segment p-[2px]", className)}>
      {options.map((o) => {
        const on = o.value === value;
        return (
          <button
            key={String(o.value)}
            type="button"
            aria-pressed={on}
            onClick={() => onChange(o.value)}
            className={cn(
              "cursor-pointer rounded-[3px] border-0 transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-clay",
              size === "sm" ? "h-7 px-2.5 text-[12px]" : "h-8 px-3.5 text-[13px]",
              on ? "bg-sheet font-semibold text-ink shadow-[0_1px_1px_rgba(31,27,22,0.08)]" : "bg-transparent font-medium text-ink-2 hover:text-ink",
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

// Tabs is the underlined tab row that filters a list.
export function Tabs<T extends string>({
  label,
  value,
  options,
  onChange,
  className,
}: {
  label: string;
  value: T;
  options: { value: T; label: string; count?: number }[];
  onChange: (v: T) => void;
  className?: string;
}) {
  return (
    <div role="tablist" aria-label={label} className={cn("flex gap-5 border-b border-line", className)}>
      {options.map((o) => {
        const on = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            role="tab"
            aria-selected={on}
            onClick={() => onChange(o.value)}
            className={cn(
              "-mb-px flex h-9 cursor-pointer items-center gap-1.5 border-0 border-b-2 bg-transparent px-0.5 text-[13px] transition-colors",
              on ? "border-ink font-semibold text-ink" : "border-transparent font-medium text-ink-3 hover:text-ink",
            )}
          >
            {o.label}
            {o.count !== undefined && (
              <span className={cn("num rounded-[3px] px-1.5 py-px text-[11px] font-semibold", on ? "bg-ink text-sheet" : "bg-track text-ink-3")}>{o.count}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}
