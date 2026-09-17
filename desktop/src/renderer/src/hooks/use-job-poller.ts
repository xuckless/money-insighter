import { useCallback } from "react";

import { plaidsync } from "@/lib/plaidsync";
import { isTerminal, type Job } from "@/lib/plaidsync-types";

// useJobPoller returns a function that polls a job until it reaches a
// terminal state or the timeout passes.
export function useJobPoller(intervalMs = 1500, timeoutMs = 180_000) {
  return useCallback(
    async (job: Job): Promise<Job> => {
      const deadline = Date.now() + timeoutMs;
      let current = job;
      while (!isTerminal(current.state)) {
        if (Date.now() > deadline) {
          throw new Error("Still running; check back in a minute.");
        }
        await new Promise((r) => setTimeout(r, intervalMs));
        current = (await plaidsync.getJob(current.job_id)).job;
      }
      return current;
    },
    [intervalMs, timeoutMs],
  );
}
