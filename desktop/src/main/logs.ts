import { createWriteStream, mkdirSync, type WriteStream } from "node:fs";
import { join } from "node:path";

import type { LogService } from "@shared/api";

import { paths } from "./paths";

const keep = 2000;

// Logs keeps the last lines of every service in memory for the UI and
// appends everything to logs/<service>.log. Lines are stored as the
// services print them; secrets never appear because the services redact
// them and the app never logs its own.
export class Logs {
  private buffers = new Map<LogService, string[]>();
  private streams = new Map<LogService, WriteStream>();

  append(service: LogService, chunk: string) {
    const buf = this.buffers.get(service) ?? [];
    for (const line of chunk.split(/\r?\n/)) {
      if (line === "") continue;
      buf.push(line);
      if (buf.length > keep) buf.splice(0, buf.length - keep);
    }
    this.buffers.set(service, buf);
    this.stream(service).write(chunk.endsWith("\n") ? chunk : chunk + "\n");
  }

  // line writes one app log line with a timestamp, to the buffer, the
  // file and the console.
  line(service: LogService, msg: string) {
    const text = `${new Date().toISOString()} ${msg}`;
    console.log(`[${service}] ${msg}`);
    this.append(service, text);
  }

  tail(service: LogService, lines = 200): string[] {
    const buf = this.buffers.get(service) ?? [];
    return buf.slice(Math.max(0, buf.length - lines));
  }

  close() {
    for (const s of this.streams.values()) s.end();
    this.streams.clear();
  }

  private stream(service: LogService): WriteStream {
    let s = this.streams.get(service);
    if (!s) {
      mkdirSync(paths.logs(), { recursive: true });
      s = createWriteStream(join(paths.logs(), `${service}.log`), { flags: "a" });
      this.streams.set(service, s);
    }
    return s;
  }
}
