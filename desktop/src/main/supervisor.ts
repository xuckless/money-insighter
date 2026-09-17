import { EventEmitter } from "node:events";

import { app } from "electron";

import type { AppState, ServiceState } from "@shared/api";

import { Logs } from "./logs";
import { dataDir, serviceBinary } from "./paths";
import { freePort } from "./ports";
import { Postgres } from "./postgres";
import { Service } from "./services";
import { SettingsStore } from "./settings";

// Supervisor owns the lifecycle of the local stack: Postgres, then
// plaidsync (which migrates the database), then the topper (which waits
// for plaidsync's tables). It publishes AppState on every change and is
// the only place that knows the services' ports and tokens.
export class Supervisor extends EventEmitter<{ state: [AppState] }> {
  private state: AppState;
  private postgres: Postgres | undefined;
  private plaidsync: Service;
  private topper: Service;
  private op: Promise<void> = Promise.resolve();

  constructor(
    private readonly settings: SettingsStore,
    private readonly logs: Logs,
  ) {
    super();
    this.plaidsync = new Service("plaidsync", logs);
    this.topper = new Service("topper", logs);
    this.state = {
      phase: settings.configured() ? "stopped" : "setup",
      message: settings.configured() ? "Not started" : "Waiting for setup",
      services: { postgres: { phase: "stopped" }, plaidsync: { phase: "stopped" }, topper: { phase: "stopped" } },
      dataDir: dataDir(),
      version: app.getVersion(),
      platform: process.platform,
    };
  }

  current(): AppState {
    return this.state;
  }

  // Endpoints the IPC layer proxies to. Undefined until ready.
  endpoints(): { plaidsync: { url: string; token: string }; topper: { url: string; token: string } } | undefined {
    if (this.state.phase !== "ready") return undefined;
    const { secrets } = this.settings.current();
    return {
      plaidsync: { url: `http://127.0.0.1:${this.state.services.plaidsync.port}`, token: secrets.apiToken },
      topper: { url: `http://127.0.0.1:${this.state.services.topper.port}`, token: secrets.topperToken },
    };
  }

  // start brings the stack up. Serialised with stop and restart so that
  // overlapping requests from the UI cannot interleave.
  start(): Promise<void> {
    return this.enqueue(() => this.doStart());
  }

  stop(): Promise<void> {
    return this.enqueue(() => this.doStop());
  }

  restart(): Promise<void> {
    return this.enqueue(async () => {
      await this.doStop();
      await this.doStart();
    });
  }

  private enqueue(fn: () => Promise<void>): Promise<void> {
    const next = this.op.then(fn, fn);
    this.op = next.catch(() => undefined);
    return next;
  }

  private async doStart(): Promise<void> {
    if (!this.settings.configured()) {
      this.set({ phase: "setup", message: "Waiting for setup" });
      return;
    }
    if (this.state.phase === "ready") return;
    const { config, secrets } = this.settings.current();
    try {
      this.set({ phase: "starting", message: "Starting the database" });
      this.service("postgres", { phase: "starting" });
      const pgPort = await freePort();
      this.postgres = new Postgres(secrets.dbPassword, this.logs);
      await this.postgres.start(pgPort);
      this.service("postgres", { phase: "ready", port: pgPort });

      this.set({ phase: "starting", message: "Starting the sync service" });
      this.service("plaidsync", { phase: "starting" });
      const psPort = await freePort();
      await this.plaidsync.start(
        serviceBinary("plaidsync"),
        {
          PLAIDSYNC_BIND_ADDR: `127.0.0.1:${psPort}`,
          PLAIDSYNC_DATABASE_URL: this.postgres.url(),
          PLAIDSYNC_API_TOKEN: secrets.apiToken,
          PLAIDSYNC_KEK_VERSION: String(secrets.kekVersion),
          PLAIDSYNC_KEK: secrets.kek,
          PLAID_CLIENT_ID: config.plaidClientId,
          PLAID_SECRET: secrets.plaidSecret,
          PLAID_ENV: config.plaidEnv,
          PLAIDSYNC_COUNTRY_CODES: config.countryCodes,
          PLAIDSYNC_PRODUCTS: config.products,
          PLAIDSYNC_TRANSACTIONS_DAYS_REQUESTED: String(config.transactionsDaysRequested),
          PLAIDSYNC_LINK_CLIENT_NAME: config.linkClientName,
          PLAIDSYNC_SYNC_INTERVAL: config.syncInterval,
          PLAIDSYNC_LOG_FORMAT: "text",
          PLAIDSYNC_LOG_LEVEL: "info",
        },
        psPort,
      );
      this.service("plaidsync", { phase: "ready", port: psPort, pid: this.plaidsync.pid });

      this.set({ phase: "starting", message: "Starting the data service" });
      this.service("topper", { phase: "starting" });
      const tpPort = await freePort();
      await this.topper.start(
        serviceBinary("topper"),
        {
          TOPPER_BIND_ADDR: `127.0.0.1:${tpPort}`,
          TOPPER_DATABASE_URL: this.postgres.url(),
          TOPPER_API_KEYS: `desktop:read:${secrets.topperToken}`,
          TOPPER_STARTUP_WAIT: "60s",
          // The UI refreshes right after a sync finishes; a long cache
          // would show stale rows.
          TOPPER_CACHE_TTL: "2s",
          TOPPER_LOG_FORMAT: "text",
          TOPPER_LOG_LEVEL: "info",
        },
        tpPort,
      );
      this.service("topper", { phase: "ready", port: tpPort, pid: this.topper.pid });

      this.set({ phase: "ready", message: "Running" });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      this.logs.line("app", `startup failed: ${msg}`);
      for (const name of ["postgres", "plaidsync", "topper"] as const) {
        if (this.state.services[name].phase === "starting") this.service(name, { phase: "error", detail: msg });
      }
      this.set({ phase: "error", message: msg });
      await this.tearDown();
    }
  }

  private async doStop(): Promise<void> {
    if (this.state.phase === "stopped" || this.state.phase === "setup") return;
    this.set({ phase: "stopping", message: "Stopping" });
    await this.tearDown();
    this.set({ phase: this.settings.configured() ? "stopped" : "setup", message: "Stopped" });
  }

  // tearDown stops whatever is running, in reverse order, and never throws.
  private async tearDown(): Promise<void> {
    for (const [name, svc] of [
      ["topper", this.topper],
      ["plaidsync", this.plaidsync],
    ] as const) {
      try {
        await svc.stop();
      } catch (err) {
        this.logs.line("app", `stopping ${name}: ${err instanceof Error ? err.message : String(err)}`);
      }
      this.service(name, { phase: "stopped" });
    }
    if (this.postgres) {
      try {
        await this.postgres.stop();
      } catch (err) {
        this.logs.line("app", `stopping postgres: ${err instanceof Error ? err.message : String(err)}`);
      }
      this.postgres = undefined;
    }
    this.service("postgres", { phase: "stopped" });
  }

  private set(patch: Partial<AppState>) {
    this.state = { ...this.state, ...patch };
    this.emit("state", this.state);
  }

  private service(name: keyof AppState["services"], s: ServiceState) {
    this.set({ services: { ...this.state.services, [name]: s } });
  }
}
