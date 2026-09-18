import { cn } from "@/lib/utils";

// PageHeader opens every screen: a compact title, an optional line under
// it, and the page's controls on the right. Figures that matter go in the
// stat strip below it, not in the title.
export function PageHeader({
  title,
  subtitle,
  actions,
  className,
}: {
  title: React.ReactNode;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex min-h-10 flex-wrap items-center justify-between gap-x-6 gap-y-3", className)}>
      <div className="flex min-w-0 flex-col gap-0.5">
        <h1 className="m-0 text-[22px] leading-tight font-semibold tracking-[-0.015em] text-ink">{title}</h1>
        {subtitle && <p className="m-0 text-[13px] leading-snug text-ink-3">{subtitle}</p>}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

// Page is the vertical rhythm of a screen.
export function Page({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex flex-col gap-5", className)} {...props} />;
}

// Grid is the page's 12-column grid. Children set their span with
// col-span-12 and an xl: span; rows stretch so panels line up.
export function Grid({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("grid grid-cols-12 gap-4", className)} {...props} />;
}
