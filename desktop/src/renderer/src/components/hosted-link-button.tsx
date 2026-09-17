import { ExternalLink, Loader2 } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useJobPoller } from "@/hooks/use-job-poller";
import { hostedLinkStatus, startHostedLink } from "@/lib/actions";
import type { HostedLink, Job } from "@/lib/plaidsync-types";

type Phase =
  | { kind: "idle" }
  | { kind: "starting" }
  | { kind: "waiting"; session: HostedLink; started: boolean }
  | { kind: "failed"; session: HostedLink | null; message: string };

// HostedLinkButton runs Plaid Hosted Link: plaidsync creates a session, the
// user finishes it in the system browser, and this dialog polls until the
// session ends. A new connection when itemId is absent, update mode
// (re-authentication) for an existing item otherwise.
export function HostedLinkButton({
  itemId,
  accountSelection = false,
  onLinked,
  children,
  ...buttonProps
}: {
  itemId?: string;
  accountSelection?: boolean;
  // Called after the bank is linked and, when a sync was queued, after it
  // finished, so the caller can reload.
  onLinked: () => void;
  children: React.ReactNode;
} & Omit<React.ComponentProps<typeof Button>, "onClick">) {
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const poll = useJobPoller();
  const sessionRef = useRef<HostedLink | null>(null);

  const start = async () => {
    setPhase({ kind: "starting" });
    const res = await startHostedLink(itemId, accountSelection);
    if (!res.ok) {
      setPhase({ kind: "idle" });
      toast.error("Could not start Plaid Link", { description: res.error });
      return;
    }
    sessionRef.current = res.data;
    setPhase({ kind: "waiting", session: res.data, started: false });
    await openBrowser(res.data);
  };

  const openBrowser = async (session: HostedLink) => {
    try {
      await window.api.app.openExternal(session.hosted_link_url);
    } catch (err) {
      toast.error("Could not open your browser", { description: (err as Error).message });
    }
  };

  const finish = useCallback(
    async (job: Job | null, name: string) => {
      toast.success(itemId ? `${name} re-linked` : `${name} connected`, {
        description: job ? "Initial sync started." : undefined,
      });
      onLinked();
      if (!job) return;
      try {
        const done = await poll(job);
        if (done.state === "failed") {
          toast.error(`Sync of ${name} failed`, { description: done.error_message ?? done.error_code ?? undefined });
        }
      } catch (err) {
        toast.warning(`Sync of ${name} still running`, { description: (err as Error).message });
      }
      onLinked();
    },
    [itemId, onLinked, poll],
  );

  // Poll while the dialog waits. The interval is cleared when the phase
  // changes or the component unmounts (Cancel).
  useEffect(() => {
    if (phase.kind !== "waiting") return;
    const { session } = phase;
    let stopped = false;
    const tick = async () => {
      const res = await hostedLinkStatus(session.link_token);
      if (stopped) return;
      if (!res.ok) {
        setPhase({ kind: "failed", session: null, message: /not found|unknown/i.test(res.error) ? "The session expired. Start again." : res.error });
        return;
      }
      const s = res.data;
      switch (s.status) {
        case "pending":
          if (s.started !== phase.started) setPhase({ kind: "waiting", session, started: s.started });
          return;
        case "completed":
          setPhase({ kind: "idle" });
          void finish(s.job, s.item.institution_name ?? "Bank");
          return;
        case "exited":
          setPhase({
            kind: "failed",
            session,
            message: s.exit ? `${s.exit.message} (${s.exit.code})` : "You closed Plaid Link before finishing.",
          });
          return;
        case "expired":
          setPhase({ kind: "failed", session: null, message: "The session expired. Start again." });
          return;
      }
    };
    const id = setInterval(() => void tick(), 2000);
    return () => {
      stopped = true;
      clearInterval(id);
    };
  }, [phase, finish]);

  const open = phase.kind === "waiting" || phase.kind === "failed";

  return (
    <>
      <Button {...buttonProps} onClick={start} disabled={phase.kind !== "idle"}>
        {phase.kind === "starting" && <Loader2 className="animate-spin" />}
        {children}
      </Button>
      <Dialog open={open} onOpenChange={(o) => !o && setPhase({ kind: "idle" })}>
        <DialogContent>
          {phase.kind === "waiting" ? (
            <>
              <DialogHeader>
                <DialogTitle>Finish connecting in your browser</DialogTitle>
                <DialogDescription>
                  {phase.started
                    ? "Plaid Link is open in your browser. This window updates when you are done."
                    : "Plaid Link should have opened in your browser. If it did not, open it again below."}
                </DialogDescription>
              </DialogHeader>
              <div className="flex items-center gap-3 py-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Waiting for Plaid…
              </div>
              <DialogFooter className="sm:justify-between">
                <Button variant="ghost" onClick={() => openBrowser(phase.session)}>
                  <ExternalLink />
                  Open again
                </Button>
                <Button variant="outline" onClick={() => setPhase({ kind: "idle" })}>
                  Cancel
                </Button>
              </DialogFooter>
            </>
          ) : phase.kind === "failed" ? (
            <>
              <DialogHeader>
                <DialogTitle>Connection not completed</DialogTitle>
                <DialogDescription>{phase.message}</DialogDescription>
              </DialogHeader>
              <DialogFooter>
                <Button variant="outline" onClick={() => setPhase({ kind: "idle" })}>
                  Close
                </Button>
                <Button
                  onClick={() => {
                    setPhase({ kind: "idle" });
                    void start();
                  }}
                >
                  Retry
                </Button>
              </DialogFooter>
            </>
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  );
}
