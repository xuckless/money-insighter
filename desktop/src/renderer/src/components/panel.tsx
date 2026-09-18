import { Link } from "react-router";

import { cn } from "@/lib/utils";

// Panel is the app's container: a flat sheet on the paper with a hairline
// border, a 4px corner and one padding. Every panel on a page shares
// these, so a grid of them lines up.
export function Panel({ className, ...props }: React.ComponentProps<"section">) {
  return <section className={cn("flex min-w-0 flex-col gap-4 rounded-[4px] border border-line bg-sheet p-5", className)} {...props} />;
}

export function PanelTitle({ className, ...props }: React.ComponentProps<"h2">) {
  return <h2 className={cn("m-0 text-[15px] leading-tight font-semibold tracking-[-0.005em] text-ink", className)} {...props} />;
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
    <Link to={to} className="shrink-0 text-[12.5px] font-semibold text-clay-ink no-underline hover:text-clay">
      {children}
    </Link>
  );
}

export function Eyebrow({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("eyebrow", className)} {...props} />;
}

// Stat is a figure tile for the strip under a page header: a label, the
// figure in the serif face, and a line of context.
export function Stat({
  label,
  value,
  aside,
  tone,
  children,
  className,
}: {
  label: React.ReactNode;
  value: React.ReactNode;
  aside?: React.ReactNode;
  tone?: "good" | "bad";
  children?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-2 rounded-[4px] border border-line bg-sheet p-5", className)}>
      <div className="eyebrow">{label}</div>
      <div className="flex items-end justify-between gap-3">
        <div className={cn("figure truncate text-[30px]", tone === "good" && "text-moss", tone === "bad" && "text-clay")}>{value}</div>
        {aside}
      </div>
      {children && <div className="text-[12.5px] leading-snug text-ink-3">{children}</div>}
    </div>
  );
}

// Chip is the small label for things that want attention (ochre) or a
// quiet state (pass a className).
export function Chip({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      className={cn("inline-flex w-fit shrink-0 items-center rounded-[3px] bg-ochre-bg px-1.5 py-px text-[11px] font-semibold text-ochre-ink", className)}
      {...props}
    />
  );
}

export function Swatch({ color, className, round = false }: { color: string; className?: string; round?: boolean }) {
  return <span aria-hidden className={cn("inline-block size-2.5 shrink-0", round ? "rounded-full" : "rounded-[2px]", className)} style={{ background: color }} />;
}

// Legend lists series keys: a line, a dashed line, a band or a dot.
export function Legend({ items }: { items: { label: string; kind: "line" | "dash" | "band" | "dot" | "square"; color: string }[] }) {
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-ink-3">
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
      <span className="text-[15px] font-semibold text-ink">{title}</span>
      {children && <p className="m-0 max-w-md text-[13px] leading-relaxed text-ink-3">{children}</p>}
      {action && <div className="pt-1">{action}</div>}
    </div>
  );
}

// ListHeader and ListRow are the column headers and rows of a list inside
// a panel. Pass the same grid-cols to both.
export function ListHeader({ cols, className, children }: { cols: string; className?: string; children: React.ReactNode }) {
  return <div className={cn("grid items-center gap-4 border-b border-line px-2 pb-2", cols, className)}>{children}</div>;
}

export function ListRow({ cols, className, children, ...props }: React.ComponentProps<"div"> & { cols: string }) {
  return (
    <div className={cn("grid min-h-12 items-center gap-4 border-b border-hairline px-2 py-1.5 text-[13.5px] last:border-b-0", cols, className)} {...props}>
      {children}
    </div>
  );
}

// Notice is an inline message inside a page: a warning in ochre, or a
// plain note.
export function Notice({ tone = "note", className, children }: { tone?: "note" | "warn"; className?: string; children: React.ReactNode }) {
  return (
    <p
      className={cn(
        "m-0 rounded-[4px] border px-3.5 py-2.5 text-[12.5px] leading-snug",
        tone === "warn" ? "border-ochre-bg bg-ochre-bg/50 text-ochre-ink" : "border-line bg-paper text-ink-2",
        className,
      )}
    >
      {children}
    </p>
  );
}
