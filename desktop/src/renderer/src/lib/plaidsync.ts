import type { HostedLink, HostedStatus, Item, Job, Run } from "@/lib/plaidsync-types";

export interface PlaidErrorDetail {
  type: string;
  code: string;
  message: string;
  request_id: string;
}

// PlaidsyncError carries plaidsync's {"error", "plaid"?} body and status.
export class PlaidsyncError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly plaid?: PlaidErrorDetail,
  ) {
    super(message);
    this.name = "PlaidsyncError";
  }
}

// call proxies one request through the main process, which holds the
// token and the port; the renderer never sees either.
async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const { status, body: data } = await window.api.plaidsync.request(method, path, body);
  if (status < 200 || status >= 300) {
    const e = data as { error?: string; plaid?: PlaidErrorDetail } | undefined;
    throw new PlaidsyncError(status, e?.error ?? `HTTP ${status}`, e?.plaid);
  }
  return data as T;
}

const id = encodeURIComponent;

export const plaidsync = {
  listItems: () => call<{ items: Item[] }>("GET", "/v1/items"),

  getItem: (itemId: string) =>
    call<{ item: Item; jobs: Job[]; runs: Run[] }>("GET", `/v1/items/${id(itemId)}`),

  syncItem: (itemId: string) => call<{ job: Job }>("POST", `/v1/items/${id(itemId)}/sync`),

  removeItem: (itemId: string) => call<{ item: Item }>("DELETE", `/v1/items/${id(itemId)}`),

  getJob: (jobId: string) => call<{ job: Job }>("GET", `/v1/jobs/${id(jobId)}`),

  sandboxItem: (opts: { institution_id?: string } = {}) =>
    call<{ item: Item; job: Job | null }>("POST", "/v1/sandbox/items", opts),

  hostedLink: () => call<HostedLink>("POST", "/v1/link/hosted", {}),

  hostedUpdateLink: (itemId: string, opts: { account_selection?: boolean } = {}) =>
    call<HostedLink>("POST", `/v1/items/${id(itemId)}/link/hosted`, opts),

  hostedStatus: (linkToken: string) =>
    call<HostedStatus>("POST", "/v1/link/hosted/status", { link_token: linkToken }),
};

// describe turns a plaidsync failure into a sentence for the UI.
export function describe(err: unknown): string {
  if (!(err instanceof PlaidsyncError)) {
    return err instanceof Error ? err.message : "Unexpected error";
  }
  switch (err.status) {
    case 409:
      if (/update mode/i.test(err.message)) {
        return "This connection needs to be re-linked before it can sync.";
      }
      if (/removed/i.test(err.message)) return "This connection has been removed.";
      if (/in progress/i.test(err.message)) return "A sync is already running for this connection.";
      return err.message;
    case 429:
      return "Plaid's Trial plan connection limit has been reached.";
    case 503:
      return `Temporarily unavailable, try again shortly (${err.plaid?.code ?? err.message}).`;
    case 502:
      return `Plaid error: ${err.plaid?.message ?? err.message}`;
    default:
      return err.plaid ? `${err.plaid.code}: ${err.plaid.message}` : err.message;
  }
}
