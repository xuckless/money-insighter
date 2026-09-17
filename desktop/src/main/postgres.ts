import { existsSync } from "node:fs";
import { join } from "node:path";

import { createRequire } from "node:module";

import pg from "pg";

// embedded-postgres is an ES module. Loading it with Node's own
// require(esm) at runtime, instead of an import the bundler rewrites,
// returns the namespace whose default is the class; the bundler's interop
// helpers would hand back the namespace itself.
type EmbeddedPostgresCtor = typeof import("embedded-postgres").default;
type EmbeddedPostgres = InstanceType<EmbeddedPostgresCtor>;
const EmbeddedPostgres = (createRequire(__filename)("embedded-postgres") as { default: EmbeddedPostgresCtor }).default;

import { Logs } from "./logs";
import { paths } from "./paths";

export const dbUser = "plaidsync";
export const dbName = "plaidsync";

// Postgres runs the embedded cluster in <dataDir>/postgres. The first
// start runs initdb (a few seconds); later starts only launch the server.
export class Postgres {
  private server: EmbeddedPostgres | undefined;
  port = 0;

  constructor(
    private readonly password: string,
    private readonly logs: Logs,
  ) {}

  url(): string {
    return `postgres://${dbUser}:${encodeURIComponent(this.password)}@127.0.0.1:${this.port}/${dbName}?sslmode=disable`;
  }

  async start(port: number): Promise<void> {
    if (this.server) throw new Error("postgres already started");
    this.port = port;
    const dir = paths.postgres();
    const fresh = !existsSync(join(dir, "PG_VERSION"));
    const server = new EmbeddedPostgres({
      databaseDir: dir,
      user: dbUser,
      password: this.password,
      port,
      persistent: true,
      // The services only ever connect over loopback; scram keeps the
      // password out of the wire even so.
      authMethod: "scram-sha-256",
      initdbFlags: ["--encoding=UTF8", "--locale=C"],
      onLog: (msg) => this.logs.append("postgres", String(msg)),
      onError: (msg) => this.logs.append("postgres", String(msg)),
    });
    if (fresh) {
      this.logs.line("app", `initialising a new database cluster in ${dir}`);
      await server.initialise();
    }
    await server.start();
    this.server = server;

    if (fresh) {
      await server.createDatabase(dbName);
    }
    await this.prepare();
  }

  // prepare makes sure the database and the topper's schema exist. The
  // topper's migrations assume schema "topper" is there (the server
  // deployment created it with a dedicated role; here one superuser owns
  // everything), and the topper waits for plaidsync's tables itself.
  private async prepare(): Promise<void> {
    const admin = new pg.Client({ host: "127.0.0.1", port: this.port, user: dbUser, password: this.password, database: "postgres" });
    await admin.connect();
    try {
      const r = await admin.query("SELECT 1 FROM pg_database WHERE datname = $1", [dbName]);
      if (r.rowCount === 0) await admin.query(`CREATE DATABASE "${dbName}"`);
    } finally {
      await admin.end();
    }
    const client = new pg.Client({ host: "127.0.0.1", port: this.port, user: dbUser, password: this.password, database: dbName });
    await client.connect();
    try {
      await client.query("CREATE SCHEMA IF NOT EXISTS topper");
    } finally {
      await client.end();
    }
  }

  async stop(): Promise<void> {
    const s = this.server;
    this.server = undefined;
    if (s) await s.stop();
  }
}
