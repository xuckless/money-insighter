import { MoreHorizontal, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { HostedLinkButton } from "@/components/hosted-link-button";
import { StatusBadge } from "@/components/status-badge";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { useJobPoller } from "@/hooks/use-job-poller";
import { removeItem, syncItem } from "@/lib/actions";
import { formatDateTime, formatRelative, humanize } from "@/lib/format";
import { NEEDS_RELINK, type Item, type Job, type Run } from "@/lib/plaidsync-types";

export function ConnectionCard({
  item,
  latestJob,
  latestRun,
  accountCount,
  onChanged,
}: {
  item: Item;
  latestJob: Job | null;
  latestRun: Run | null;
  accountCount: number | null;
  onChanged: () => void;
}) {
  const poll = useJobPoller();
  const [syncing, setSyncing] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);

  const removed = item.status === "removed";
  const needsRelink = NEEDS_RELINK.has(item.status);
  // Plaid asks for account selection when the bank added accounts.
  const wantsAccountSelection = item.last_error?.code === "NEW_ACCOUNTS_AVAILABLE";
  const name = item.institution_name ?? item.item_id;

  const sync = async () => {
    setSyncing(true);
    try {
      const res = await syncItem(item.item_id);
      if (!res.ok) {
        toast.error(`Sync of ${name} failed to start`, { description: res.error });
        return;
      }
      try {
        const job = await poll(res.data);
        if (job.state === "succeeded") {
          toast.success(`${name} synced`);
        } else if (job.state === "skipped") {
          toast.info(`${name} is already up to date`, {
            description:
              job.error_code === "debounced"
                ? "It synced recently, so Plaid was not called again."
                : humanize(job.error_code),
          });
        } else {
          toast.error(`Sync of ${name} failed`, {
            description: job.error_message ?? humanize(job.error_code),
          });
        }
      } catch (err) {
        toast.warning(`Sync of ${name} still running`, { description: (err as Error).message });
      }
      onChanged();
    } finally {
      setSyncing(false);
    }
  };

  const remove = async () => {
    setRemoving(true);
    try {
      const res = await removeItem(item.item_id);
      setConfirmOpen(false);
      if (!res.ok) {
        toast.error(`Could not remove ${name}`, { description: res.error });
        return;
      }
      toast.success(`${name} removed`);
      onChanged();
    } finally {
      setRemoving(false);
    }
  };

  const lastError = item.last_error;

  return (
    <Card className={removed ? "opacity-70" : undefined}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          {name}
          <StatusBadge status={item.status} />
        </CardTitle>
        <CardDescription>
          {accountCount !== null && `${accountCount} account${accountCount === 1 ? "" : "s"} · `}
          Linked {formatRelative(item.created_at)}
        </CardDescription>
        {!removed && (
          <CardAction>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" aria-label="More actions">
                  <MoreHorizontal />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem variant="destructive" onSelect={() => setConfirmOpen(true)}>
                  <Trash2 />
                  Remove connection
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </CardAction>
        )}
      </CardHeader>
      <CardContent>
        <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
          <dt className="text-muted-foreground">Last successful sync</dt>
          <dd title={formatDateTime(item.last_successful_sync_at)}>{formatRelative(item.last_successful_sync_at)}</dd>
          {latestRun && (
            <>
              <dt className="text-muted-foreground">Last run</dt>
              <dd className="flex flex-wrap items-center gap-1.5">
                <StatusBadge status={latestRun.outcome} />
                <span className="text-muted-foreground">
                  +{latestRun.added} ~{latestRun.modified} −{latestRun.removed}
                </span>
              </dd>
            </>
          )}
          {latestJob && (
            <>
              <dt className="text-muted-foreground">Latest job</dt>
              <dd className="flex items-center gap-1.5">
                <StatusBadge status={latestJob.state} />
                <span className="text-muted-foreground">
                  {humanize(latestJob.kind)} · {formatRelative(latestJob.created_at)}
                </span>
              </dd>
            </>
          )}
          {item.consent_expires_at && (
            <>
              <dt className="text-muted-foreground">Consent expires</dt>
              <dd>{formatDateTime(item.consent_expires_at)}</dd>
            </>
          )}
        </dl>
        {lastError && !removed && (
          <p className="mt-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {lastError.message ?? `${lastError.type}: ${lastError.code}`}
          </p>
        )}
      </CardContent>
      {!removed && (
        <CardFooter className="gap-2">
          {needsRelink ? (
            <HostedLinkButton itemId={item.item_id} accountSelection={wantsAccountSelection} onLinked={onChanged}>
              Re-link
            </HostedLinkButton>
          ) : (
            <>
              <Button variant="outline" onClick={sync} disabled={syncing}>
                <RefreshCw className={syncing ? "animate-spin" : undefined} />
                {syncing ? "Syncing…" : "Sync now"}
              </Button>
              <HostedLinkButton variant="ghost" itemId={item.item_id} accountSelection={wantsAccountSelection} onLinked={onChanged}>
                Re-link
              </HostedLinkButton>
            </>
          )}
        </CardFooter>
      )}

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove {name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Plaid access is revoked and syncing stops. Already-synced transactions stay in the
              database. In Production, removing a connection does not free a Trial plan slot.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={removing}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={removing}
              onClick={(e) => {
                e.preventDefault();
                void remove();
              }}
            >
              {removing ? "Removing…" : "Remove"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
