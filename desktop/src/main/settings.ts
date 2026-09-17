import { randomBytes } from "node:crypto";
import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";

import { safeStorage } from "electron";

import type { PlaidEnv, Settings, SettingsPatch, SetupInput } from "@shared/api";

import { dataDir, paths } from "./paths";

// Config is config.json: the non-secret settings.
interface Config {
  version: 1;
  plaidEnv: PlaidEnv;
  plaidClientId: string;
  syncInterval: string;
  countryCodes: string;
  products: string;
  transactionsDaysRequested: number;
  linkClientName: string;
  kekBackedUp: boolean;
}

// Secrets is the plaintext of secrets.bin. It is only ever in memory here
// and in the environment of the two service processes.
export interface Secrets {
  plaidSecret: string;
  // Bearer token the app presents to plaidsync.
  apiToken: string;
  // Bearer token the app presents to the topper (read scope).
  topperToken: string;
  // Base64 of 32 random bytes: the key that encrypts bank access tokens
  // at rest. Losing it makes every linked bank unreadable.
  kek: string;
  kekVersion: number;
  // Password of the embedded Postgres superuser.
  dbPassword: string;
}

const defaults: Omit<Config, "plaidEnv" | "plaidClientId"> = {
  version: 1,
  syncInterval: "1h",
  countryCodes: "CA",
  products: "transactions",
  transactionsDaysRequested: 730,
  linkClientName: "Money Insighter",
  kekBackedUp: false,
};

function readJSON<T>(path: string): T | undefined {
  try {
    return JSON.parse(readFileSync(path, "utf8")) as T;
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === "ENOENT") return undefined;
    throw err;
  }
}

// writeAtomic writes via a temporary file and rename, so a crash mid-write
// never leaves a truncated config or secrets file.
function writeAtomic(path: string, data: string | Buffer, mode = 0o600) {
  mkdirSync(dataDir(), { recursive: true });
  const tmp = `${path}.tmp`;
  writeFileSync(tmp, data, { mode });
  renameSync(tmp, path);
}

// prepareSafeStorage must run once after the ready event. On Linux without
// a usable keyring (no Secret Service or KWallet on the session bus,
// --password-store=basic, a headless session) Electron refuses to encrypt
// at all; opting into its plain-text backend keeps the app working, and
// the settings screen tells the user their secrets file is only obfuscated.
export function prepareSafeStorage(): string {
  if (process.platform === "linux" && !safeStorage.isEncryptionAvailable()) {
    safeStorage.setUsePlainTextEncryption(true);
  }
  return secretsBackend();
}

function secretsBackend(): string {
  if (!safeStorage.isEncryptionAvailable()) return "unavailable";
  if (process.platform === "linux") {
    const backend = safeStorage.getSelectedStorageBackend();
    return backend === "basic_text" ? "unavailable" : backend;
  }
  return process.platform === "darwin" ? "keychain" : "dpapi";
}

export class SettingsStore {
  private config: Config | undefined;
  private secrets: Secrets | undefined;

  constructor() {
    this.config = readJSON<Config>(paths.config());
  }

  // configured reports whether first-run setup has completed.
  configured(): boolean {
    return this.config !== undefined && this.loadSecrets() !== undefined;
  }

  // view is the renderer-facing shape.
  view(): Settings {
    const c = this.config;
    return {
      plaidEnv: c?.plaidEnv ?? "sandbox",
      plaidClientId: c?.plaidClientId ?? "",
      syncInterval: c?.syncInterval ?? defaults.syncInterval,
      countryCodes: c?.countryCodes ?? defaults.countryCodes,
      products: c?.products ?? defaults.products,
      transactionsDaysRequested: c?.transactionsDaysRequested ?? defaults.transactionsDaysRequested,
      linkClientName: c?.linkClientName ?? defaults.linkClientName,
      kekBackedUp: c?.kekBackedUp ?? false,
      hasPlaidSecret: this.loadSecrets() !== undefined,
      secretsBackend: secretsBackend(),
    };
  }

  // current returns the config and secrets for starting the services. It
  // throws when setup has not run.
  current(): { config: Config; secrets: Secrets } {
    const secrets = this.loadSecrets();
    if (!this.config || !secrets) throw new Error("the app has not been set up yet");
    return { config: this.config, secrets };
  }

  setup(input: SetupInput): Settings {
    validateSetup(input);
    const existing = this.loadSecrets();
    const secrets: Secrets = {
      plaidSecret: input.plaidSecret.trim(),
      // Everything below is kept across a re-run of setup so that a
      // database created by an earlier run stays readable.
      apiToken: existing?.apiToken ?? randomBytes(32).toString("hex"),
      topperToken: existing?.topperToken ?? randomBytes(32).toString("hex"),
      kek: existing?.kek ?? randomBytes(32).toString("base64"),
      kekVersion: existing?.kekVersion ?? 1,
      dbPassword: existing?.dbPassword ?? randomBytes(24).toString("hex"),
    };
    this.saveSecrets(secrets);
    this.config = {
      ...defaults,
      ...(this.config ?? {}),
      version: 1,
      plaidEnv: input.plaidEnv,
      plaidClientId: input.plaidClientId.trim(),
    };
    writeAtomic(paths.config(), JSON.stringify(this.config, null, 2) + "\n", 0o644);
    return this.view();
  }

  update(patch: SettingsPatch): Settings {
    const { config, secrets } = this.current();
    const next: Config = { ...config };
    if (patch.plaidEnv !== undefined) next.plaidEnv = patch.plaidEnv;
    if (patch.plaidClientId !== undefined) next.plaidClientId = patch.plaidClientId.trim();
    if (patch.syncInterval !== undefined) next.syncInterval = patch.syncInterval.trim();
    if (patch.countryCodes !== undefined) next.countryCodes = patch.countryCodes.trim();
    if (patch.products !== undefined) next.products = patch.products.trim();
    if (patch.transactionsDaysRequested !== undefined) next.transactionsDaysRequested = patch.transactionsDaysRequested;
    if (patch.linkClientName !== undefined) next.linkClientName = patch.linkClientName.trim();
    validateSetup({ plaidEnv: next.plaidEnv, plaidClientId: next.plaidClientId, plaidSecret: patch.plaidSecret ?? secrets.plaidSecret });
    validateConfig(next);
    if (patch.plaidSecret !== undefined && patch.plaidSecret.trim() !== "") {
      this.saveSecrets({ ...secrets, plaidSecret: patch.plaidSecret.trim() });
    }
    this.config = next;
    writeAtomic(paths.config(), JSON.stringify(this.config, null, 2) + "\n", 0o644);
    return this.view();
  }

  markKekBackedUp(): Settings {
    const { config } = this.current();
    this.config = { ...config, kekBackedUp: true };
    writeAtomic(paths.config(), JSON.stringify(this.config, null, 2) + "\n", 0o644);
    return this.view();
  }

  private loadSecrets(): Secrets | undefined {
    if (this.secrets) return this.secrets;
    let blob: Buffer;
    try {
      blob = readFileSync(paths.secrets());
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code === "ENOENT") return undefined;
      throw err;
    }
    let text: string;
    try {
      text = safeStorage.decryptString(blob);
    } catch (err) {
      throw new Error(
        `the secrets file cannot be decrypted (${(err as Error).message}). It was encrypted with the OS keyring; ` +
          `if that keyring is unavailable now, restore it, or delete ${paths.secrets()} and set the app up again ` +
          "(linked banks will need to be reconnected)",
      );
    }
    const parsed = JSON.parse(text) as Secrets;
    this.secrets = parsed;
    return parsed;
  }

  private saveSecrets(s: Secrets) {
    writeAtomic(paths.secrets(), safeStorage.encryptString(JSON.stringify(s)));
    this.secrets = s;
  }
}

function validateSetup(input: SetupInput) {
  if (input.plaidEnv !== "sandbox" && input.plaidEnv !== "production") {
    throw new Error("environment must be sandbox or production");
  }
  const id = input.plaidClientId.trim();
  if (!/^[0-9a-f]{24}$/i.test(id)) {
    throw new Error("the client ID should be the 24-character hex value from the Plaid dashboard");
  }
  const secret = input.plaidSecret.trim();
  if (!/^[0-9a-f]{30}$/i.test(secret)) {
    throw new Error(`the ${input.plaidEnv} secret should be the 30-character hex value from the Plaid dashboard`);
  }
}

function validateConfig(c: Config) {
  if (!/^\d+(ns|us|µs|ms|s|m|h)$/.test(c.syncInterval)) {
    throw new Error("sync interval must be a Go duration such as 30m, 1h or 6h");
  }
  if (!Number.isInteger(c.transactionsDaysRequested) || c.transactionsDaysRequested < 1 || c.transactionsDaysRequested > 730) {
    throw new Error("days of history must be a whole number from 1 to 730");
  }
  if (!/^[A-Z]{2}(,[A-Z]{2})*$/.test(c.countryCodes)) {
    throw new Error("country codes must be comma-separated two-letter codes such as CA or CA,US");
  }
  if (!/^[a-z_]+(,[a-z_]+)*$/.test(c.products)) {
    throw new Error("products must be comma-separated Plaid product names such as transactions");
  }
  if (c.linkClientName === "") throw new Error("the Link client name cannot be empty");
}
