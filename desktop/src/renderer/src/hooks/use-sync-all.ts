import { useCallback, useState } from "react";
import { toast } from "sonner";

import { useDataVersion } from "@/hooks/use-data-version";
import { useJobPoller } from "@/hooks/use-job-poller";
import { syncItem } from "@/lib/actions";
import { plaidsync } from "@/lib/plaidsync";
import { NEEDS_RELINK } from "@/lib/plaidsync-types";

// useSyncAll syncs every connection that can sync, waits for the jobs and
// reports the outcome in one toast, then reloads the pages.
export function useSyncAll(): [boolean, () => Promise<void>] {
  const [syncing, setSyncing] = useState(false);
  const poll = useJobPoller();
  const { bump } = useDataVersion();

  const run = useCallback(async () => {
    setSyncing(true);
    try {
      const { items } = await plaidsync.listItems();
      const syncable = items.filter((i) => i.status !== "removed" && !NEEDS_RELINK.has(i.status));
      if (syncable.length === 0) {
        toast.info("Nothing to sync", { description: "Connect an account first, or reconnect one that needs it." });
        return;
      }
      const outcomes = await Promise.all(
        syncable.map(async (item) => {
          const name = item.institution_name ?? "A connection";
          const res = await syncItem(item.item_id);
          if (!res.ok) return { name, state: "failed" as const, detail: res.error };
          try {
            const job = await poll(res.data);
            return { name, state: job.state, detail: job.error_message ?? job.error_code ?? "" };
          } catch (err) {
            return { name, state: "running" as const, detail: (err as Error).message };
          }
        }),
      );
      const failed = outcomes.filter((o) => o.state === "failed");
      const synced = outcomes.filter((o) => o.state === "succeeded").length;
      if (failed.length) {
        toast.error(`${failed.map((f) => f.name).join(", ")} could not sync`, { description: failed[0].detail });
      } else if (synced === 0) {
        toast.info("Already up to date", { description: "Everything synced in the last few minutes." });
      } else {
        toast.success(synced === outcomes.length ? "Everything is synced" : `${synced} of ${outcomes.length} connections synced`);
      }
    } catch (err) {
      toast.error("Sync failed to start", { description: (err as Error).message });
    } finally {
      setSyncing(false);
      bump();
    }
  }, [bump, poll]);

  return [syncing, run];
}
