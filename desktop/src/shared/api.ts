// The contract between the renderer and the main process. Everything the
// UI can do goes through window.api (see src/preload); nothing else of the
// main process is visible to the renderer. Keep this file free of imports
// from either side.

export type PlaidEnv = "sandbox" | "production";

// Settings is the non-secret configuration, persisted as config.json in
// the app's data directory. Secrets (the Plaid secret, the API tokens, the
// key that encrypts bank access tokens) never cross the bridge in this
// shape; only their presence does.
export interface Settings {
  plaidEnv: PlaidEnv;
  plaidClientId: string;
  // Go duration syntax, e.g. "1h". How often every connection is synced.
  syncInterval: string;
  // Comma-separated ISO country codes for Link, e.g. "CA".
  countryCodes: string;
  // Comma-separated Plaid products, e.g. "transactions".
  products: string;
  // Days of history requested at Link time, 1..730.
  transactionsDaysRequested: number;
  // Name shown inside Link.
  linkClientName: string;
  // Whether the user confirmed they saved a copy of the encryption key.
  kekBackedUp: boolean;
  // Derived, read-only.
  hasPlaidSecret: boolean;
  // How the OS protects the secrets file: safeStorage's backend name, or
  // "unavailable" when the app had to fall back to obfuscation only.
  secretsBackend: string;
}

// SetupInput is what the first-run screen collects. Everything else is
// generated (tokens, encryption key, database password) or defaulted.
export interface SetupInput {
  plaidEnv: PlaidEnv;
  plaidClientId: string;
  plaidSecret: string;
}

// SettingsPatch is what the settings screen may change later. A new Plaid
// secret is optional; the rest replace the stored values. Applying a
// patch restarts the services.
export interface SettingsPatch {
  plaidEnv?: PlaidEnv;
  plaidClientId?: string;
  plaidSecret?: string;
  syncInterval?: string;
  countryCodes?: string;
  products?: string;
  transactionsDaysRequested?: number;
  linkClientName?: string;
}

export type ServicePhase = "stopped" | "starting" | "ready" | "error";

export interface ServiceState {
  phase: ServicePhase;
  port?: number;
  pid?: number;
  // Short human-readable detail: what it is doing, or why it failed.
  detail?: string;
}

export type AppPhase = "setup" | "starting" | "ready" | "error" | "stopping" | "stopped";

// AppState is pushed to the renderer whenever anything changes.
export interface AppState {
  phase: AppPhase;
  // Progress or error text for the phase.
  message: string;
  services: {
    postgres: ServiceState;
    plaidsync: ServiceState;
    topper: ServiceState;
  };
  dataDir: string;
  version: string;
  platform: string;
}

export type LogService = "app" | "postgres" | "plaidsync" | "topper";

// HttpResult is the answer of a proxied request: the status and the parsed
// JSON body (undefined when the body was empty or not JSON). The renderer
// decides what a non-2xx means, exactly as the old web client did.
export interface HttpResult {
  status: number;
  body: unknown;
}

export interface DesktopApi {
  app: {
    getState(): Promise<AppState>;
    // Subscribes to state pushes; returns the unsubscribe function.
    onState(cb: (state: AppState) => void): () => void;
    getSettings(): Promise<Settings>;
    // First run: stores the input, generates every secret, starts the stack.
    saveSetup(input: SetupInput): Promise<Settings>;
    // Later changes; restarts the stack.
    updateSettings(patch: SettingsPatch): Promise<Settings>;
    restart(): Promise<void>;
    // The base64 key that encrypts bank access tokens, for the user to
    // back up. Shown once on request, never logged.
    revealKek(): Promise<string>;
    markKekBackedUp(): Promise<Settings>;
    getLogs(service: LogService, lines?: number): Promise<string[]>;
    // Opens an http(s) URL in the system browser; anything else is refused.
    openExternal(url: string): Promise<void>;
    openDataDir(): Promise<void>;
  };
  plaidsync: {
    request(method: string, path: string, body?: unknown): Promise<HttpResult>;
  };
  topper: {
    // GET only; search is the query string without the leading "?".
    get(path: string, search: string): Promise<HttpResult>;
  };
}
