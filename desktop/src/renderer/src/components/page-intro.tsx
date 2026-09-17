import { cn } from "@/lib/utils";

// PageIntro opens every screen: a small terracotta eyebrow, a sentence in
// the serif face that says what matters most, and the page's controls.
export function PageIntro({
  eyebrow,
  children,
  actions,
  className,
}: {
  eyebrow: React.ReactNode;
  children: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between xl:gap-8", className)}>
      <div className="flex min-w-0 flex-col gap-3">
        <div className="text-xs font-semibold tracking-[0.1em] text-clay uppercase">{eyebrow}</div>
        <h1 className="m-0 max-w-[820px] font-serif text-[42px] leading-[1.08] font-normal tracking-[-0.015em] text-balance [&_em]:text-clay">
          {children}
        </h1>
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

// Page is the vertical rhythm of a screen.
export function Page({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex flex-col gap-6", className)} {...props} />;
}
