import { cn } from "@/lib/utils";

// Segmented is the design's pill group for switching a view: a period, a
// projection horizon.
export function Segmented<T extends string | number>({
  label,
  value,
  options,
  onChange,
  className,
}: {
  label: string;
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
  className?: string;
}) {
  return (
    <div role="group" aria-label={label} className={cn("flex shrink-0 gap-0.5 rounded-xl bg-segment p-[3px]", className)}>
      {options.map((o) => {
        const on = o.value === value;
        return (
          <button
            key={String(o.value)}
            type="button"
            aria-pressed={on}
            onClick={() => onChange(o.value)}
            className={cn(
              "h-9.5 cursor-pointer rounded-[9px] border-0 px-4 text-[13px] transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-clay",
              on ? "bg-sheet font-semibold text-ink shadow-[0_1px_2px_rgba(31,27,22,0.1)]" : "bg-transparent font-medium text-ink-2 hover:text-ink",
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

// Pills is the rounded tab row used to filter a list.
export function Pills<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: { value: T; label: string; count?: number }[];
  onChange: (v: T) => void;
}) {
  return (
    <div role="tablist" aria-label={label} className="flex flex-wrap gap-1.5">
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
              "flex h-9.5 cursor-pointer items-center gap-2 rounded-full border px-3.5 text-[13px] font-semibold transition-colors",
              on ? "border-ink bg-ink text-sheet" : "border-line-strong bg-sheet text-ink hover:bg-row-hover",
            )}
          >
            {o.label}
            {o.count !== undefined && <span className="num text-xs font-medium opacity-80">{o.count}</span>}
          </button>
        );
      })}
    </div>
  );
}
