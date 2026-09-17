import { Link } from "react-router";

import { cn } from "@/lib/utils";

// Panel is the design's card: a sheet on the paper with a hairline border.
export function Panel({ className, ...props }: React.ComponentProps<"section">) {
  return (
    <section
      className={cn("flex min-w-0 flex-col gap-4 rounded-[14px] border border-line bg-sheet px-6 py-5.5", className)}
      {...props}
    />
  );
}

export function PanelTitle({ className, ...props }: React.ComponentProps<"h2">) {
  return (
    <h2 className={cn("m-0 font-serif text-[21px] leading-tight font-medium tracking-[-0.01em]", className)} {...props} />
  );
}

// PanelHeader is a title with an optional description on the left and
// whatever the panel needs (a legend, a link, a total) on the right.
export function PanelHeader({
  title,
  description,
  children,
  className,
}: {
  title: React.ReactNode;
  description?: React.ReactNode;
  children?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex items-start justify-between gap-4", className)}>
      <div className="min-w-0">
        <PanelTitle>{title}</PanelTitle>
        {description && <p className="mt-1 text-[12.5px] leading-snug text-ink-3">{description}</p>}
      </div>
      {children}
    </div>
  );
}

export function PanelLink({ to, children }: { to: string; children: React.ReactNode }) {
  return (
    <Link to={to} className="shrink-0 text-[13px] font-semibold text-clay-ink no-underline hover:text-clay">
      {children}
    </Link>
  );
}

export function Eyebrow({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("eyebrow", className)} {...props} />;
}

// Chip is the small ochre label for things that want attention.
export function Chip({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      className={cn(
        "inline-flex w-fit shrink-0 items-center rounded-[5px] bg-ochre-bg px-1.5 py-px text-[11.5px] font-semibold text-ochre-ink",
        className,
      )}
      {...props}
    />
  );
}

export function Swatch({ color, className, round = false }: { color: string; className?: string; round?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn("inline-block size-2.5 shrink-0", round ? "rounded-full" : "rounded-[3px]", className)}
      style={{ background: color }}
    />
  );
}

// Legend lists series keys: a line, a dashed line, a band or a dot.
export function Legend({ items }: { items: { label: string; kind: "line" | "dash" | "band" | "dot" | "square"; color: string }[] }) {
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1 pt-1 text-xs text-ink-3">
      {items.map((i) => (
        <span key={i.label} className="flex items-center gap-1.5 whitespace-nowrap">
          {i.kind === "line" && <span className="h-0.5 w-4.5" style={{ background: i.color }} />}
          {i.kind === "dash" && <span className="h-0 w-4.5 border-t-2 border-dashed" style={{ borderColor: i.color }} />}
          {i.kind === "band" && <span className="h-2.5 w-4.5 rounded-[2px]" style={{ background: i.color }} />}
          {i.kind === "dot" && <span className="size-2.5 rounded-full" style={{ background: i.color }} />}
          {i.kind === "square" && <span className="size-2.5 rounded-[2px]" style={{ background: i.color }} />}
          {i.label}
        </span>
      ))}
    </div>
  );
}

// Empty is the quiet message a panel shows when it has nothing yet.
export function Empty({ title, children, action }: { title: string; children?: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="flex flex-1 flex-col items-start justify-center gap-2 py-4">
      <span className="font-serif text-[22px] leading-tight">{title}</span>
      {children && <p className="m-0 max-w-md text-[13px] leading-relaxed text-ink-3">{children}</p>}
      {action}
    </div>
  );
}
