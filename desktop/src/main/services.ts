import { spawn, type ChildProcess } from "node:child_process";

import type { LogService } from "@shared/api";

import { Logs } from "./logs";

// Service is one supervised Go process. It is started with an environment
// built by the supervisor, watched until its /readyz answers, and stopped
// with SIGTERM (graceful in both services) and a SIGKILL fallback.
export class Service {
  private child: ChildProcess | undefined;
  private exited: Promise<number | null> | undefined;

  constructor(
    readonly name: LogService & ("plaidsync" | "topper"),
    private readonly logs: Logs,
  ) {}

  get pid(): number | undefined {
    return this.child?.pid;
  }

  async start(binary: string, env: NodeJS.ProcessEnv, port: number, timeoutMs = 90_000): Promise<void> {
    if (this.child) throw new Error(`${this.name} already started`);
    this.logs.line("app", `starting ${this.name} on 127.0.0.1:${port}`);
    const child = spawn(binary, [], {
      env: { ...minimalEnv(), ...env },
      stdio: ["ignore", "pipe", "pipe"],
      windowsHide: true,
    });
    this.child = child;
    child.stdout?.on("data", (d: Buffer) => this.logs.append(this.name, d.toString()));
    child.stderr?.on("data", (d: Buffer) => this.logs.append(this.name, d.toString()));
    this.exited = new Promise((resolve) => {
      child.on("exit", (code, signal) => {
        this.logs.line("app", `${this.name} exited (code ${code}, signal ${signal})`);
        this.child = undefined;
        resolve(code);
      });
      child.on("error", (err) => {
        this.logs.line("app", `${this.name} failed to start: ${err.message}`);
        this.child = undefined;
        resolve(null);
      });
    });

    const deadline = Date.now() + timeoutMs;
    const url = `http://127.0.0.1:${port}/readyz`;
    while (Date.now() < deadline) {
      if (!this.child) {
        throw new Error(`${this.name} exited during startup:\n${this.logs.tail(this.name, 15).join("\n")}`);
      }
      try {
        const res = await fetch(url, { signal: AbortSignal.timeout(2000) });
        if (res.ok) {
          this.logs.line("app", `${this.name} is ready`);
          return;
        }
      } catch {
        // not listening yet
      }
      await sleep(300);
    }
    await this.stop();
    throw new Error(`${this.name} did not become ready within ${timeoutMs / 1000}s`);
  }

  async stop(): Promise<void> {
    const child = this.child;
    if (!child || !this.exited) return;
    this.logs.line("app", `stopping ${this.name}`);
    child.kill("SIGTERM");
    const done = await Promise.race([this.exited, sleep(15_000).then(() => "timeout" as const)]);
    if (done === "timeout") {
      this.logs.line("app", `${this.name} ignored SIGTERM; killing`);
      child.kill("SIGKILL");
      await this.exited;
    }
  }
}

// minimalEnv is the environment the services inherit: enough to resolve
// DNS, find certificates and, on Windows, load system DLLs, and nothing
// from the user's shell that a config loader might misread.
function minimalEnv(): NodeJS.ProcessEnv {
  const keep = ["PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "TEMP", "TMP", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy"];
  const out: NodeJS.ProcessEnv = {};
  for (const k of keep) if (process.env[k] !== undefined) out[k] = process.env[k];
  return out;
}

export function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
