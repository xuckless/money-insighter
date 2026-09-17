import { Badge } from "@/components/ui/badge";
import { humanize } from "@/lib/format";
import { cn } from "@/lib/utils";

const tone: Record<string, string> = {
  active: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  succeeded: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  success: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  queued: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  running: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  skipped: "bg-muted text-muted-foreground",
  pending_expiration: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  login_required: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  permission_revoked: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  needs_reauth: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  retryable_error: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  error: "bg-red-500/15 text-red-700 dark:text-red-300",
  failed: "bg-red-500/15 text-red-700 dark:text-red-300",
  fatal: "bg-red-500/15 text-red-700 dark:text-red-300",
  removed: "bg-muted text-muted-foreground line-through",
};

export function StatusBadge({ status, className }: { status: string; className?: string }) {
  return (
    <Badge variant="secondary" className={cn("border-transparent", tone[status], className)}>
      {humanize(status)}
    </Badge>
  );
}
