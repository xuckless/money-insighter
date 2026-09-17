import { describe, plaidsync } from "@/lib/plaidsync";
import type { HostedLink, HostedStatus, Item, Job } from "@/lib/plaidsync-types";
import type { Result } from "@/lib/result";

// Mutations report failures as values so callers can show them inline.
async function run<T>(what: string, fn: () => Promise<T>): Promise<Result<T>> {
  try {
    return { ok: true, data: await fn() };
  } catch (err) {
    console.error(`${what} failed`, err);
    return { ok: false, error: describe(err) };
  }
}

export const startHostedLink = (itemId?: string, accountSelection = false): Promise<Result<HostedLink>> =>
  run("start hosted link", () =>
    itemId ? plaidsync.hostedUpdateLink(itemId, accountSelection ? { account_selection: true } : {}) : plaidsync.hostedLink(),
  );

export const hostedLinkStatus = (linkToken: string): Promise<Result<HostedStatus>> =>
  run("hosted link status", () => plaidsync.hostedStatus(linkToken));

export const syncItem = (itemId: string): Promise<Result<Job>> =>
  run("sync item", async () => (await plaidsync.syncItem(itemId)).job);

export const removeItem = (itemId: string): Promise<Result<Item>> =>
  run("remove item", async () => (await plaidsync.removeItem(itemId)).item);

export const addSandboxItem = (): Promise<Result<{ item: Item; job: Job | null }>> =>
  run("add sandbox item", () => plaidsync.sandboxItem());
