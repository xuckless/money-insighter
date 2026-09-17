// Shapes returned by plaidsync's /v1 API. Timestamps are RFC 3339 strings.
// Kept free of server-only imports so client components can use the types.

export type ItemStatus =
  | "active"
  | "login_required"
  | "pending_expiration"
  | "permission_revoked"
  | "error"
  | "removed";

export type JobKind = "initial" | "manual" | "webhook" | "scheduled";
export type JobState = "queued" | "running" | "succeeded" | "failed" | "skipped";
export type RunOutcome =
  | "success"
  | "retryable_error"
  | "needs_reauth"
  | "fatal"
  | "locked"
  | "canceled"
  | "error";

export interface Item {
  item_id: string;
  institution_id: string | null;
  institution_name: string | null;
  status: ItemStatus;
  has_cursor: boolean;
  last_error: {
    code: string;
    type: string;
    message: string | null;
    at: string | null;
  } | null;
  last_successful_sync_at: string | null;
  consent_expires_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface Job {
  job_id: string;
  item_id: string;
  kind: JobKind;
  state: JobState;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  error_code: string | null;
  error_message: string | null;
}

export interface Run {
  run_id: number;
  item_id: string;
  job_id: string | null;
  trigger: JobKind;
  started_at: string;
  finished_at: string;
  pages: number;
  added: number;
  modified: number;
  removed: number;
  inserted: number;
  updated: number;
  superseded: number;
  accounts_seen: number;
  accounts_missing: number;
  outcome: RunOutcome;
  error_code: string | null;
  error_type: string | null;
  error_message: string | null;
  request_id: string | null;
}

export interface LinkToken {
  link_token: string;
  expiration: string;
  request_id: string;
}

// Item statuses plaidsync refuses to sync until Link update mode has run.
export const NEEDS_RELINK: ReadonlySet<string> = new Set([
  "login_required",
  "pending_expiration",
  "permission_revoked",
]);

export function isTerminal(state: JobState): boolean {
  return state === "succeeded" || state === "failed" || state === "skipped";
}

// Hosted Link: plaidsync creates the session, the user finishes it in the
// system browser, and the app polls for the outcome.
export interface HostedLink {
  link_token: string;
  hosted_link_url: string;
  expiration: string;
  request_id: string;
}

export type HostedStatus =
  | { status: "pending"; started: boolean }
  | { status: "completed"; item: Item; job: Job | null }
  | { status: "exited"; exit?: { type: string; code: string; message: string; request_id: string } }
  | { status: "expired" };
