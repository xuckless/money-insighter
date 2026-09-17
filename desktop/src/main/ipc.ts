import { BrowserWindow, ipcMain, shell } from "electron";

import { writeTables, type LogService, type SettingsPatch, type SetupInput } from "@shared/api";

import { proxy } from "./http";
import { Logs } from "./logs";
import { dataDir } from "./paths";
import { SettingsStore } from "./settings";
import { Supervisor } from "./supervisor";

const logServices: ReadonlySet<string> = new Set(["app", "postgres", "plaidsync", "topper"]);
const writable: ReadonlySet<string> = new Set(writeTables);

// registerIpc wires every channel of the DesktopApi contract. Arguments
// come from the renderer and are validated as untrusted input even though
// the renderer is ours: a bug there must not become a shell.openExternal
// of file:// or a request to an arbitrary host.
export function registerIpc(supervisor: Supervisor, settings: SettingsStore, logs: Logs) {
  supervisor.on("state", (state) => {
    for (const w of BrowserWindow.getAllWindows()) w.webContents.send("app:state", state);
  });

  ipcMain.handle("app:getState", () => supervisor.current());
  ipcMain.handle("app:getSettings", () => settings.view());

  ipcMain.handle("app:saveSetup", async (_e, input: SetupInput) => {
    const view = settings.setup(input);
    logs.line("app", "setup saved; starting services");
    void supervisor.restart();
    return view;
  });

  ipcMain.handle("app:updateSettings", async (_e, patch: SettingsPatch) => {
    const view = settings.update(patch);
    logs.line("app", "settings changed; restarting services");
    void supervisor.restart();
    return view;
  });

  ipcMain.handle("app:restart", () => supervisor.restart());

  ipcMain.handle("app:revealKek", () => settings.current().secrets.kek);
  ipcMain.handle("app:markKekBackedUp", () => settings.markKekBackedUp());

  ipcMain.handle("app:getLogs", (_e, service: LogService, lines?: number) => {
    if (!logServices.has(service)) throw new Error("unknown log");
    return logs.tail(service, Math.min(Math.max(Number(lines) || 200, 1), 2000));
  });

  ipcMain.handle("app:openExternal", async (_e, url: string) => {
    let u: URL;
    try {
      u = new URL(url);
    } catch {
      throw new Error("invalid URL");
    }
    if (u.protocol !== "https:" && u.protocol !== "http:") throw new Error("only http(s) links can be opened");
    await shell.openExternal(u.toString());
  });

  ipcMain.handle("app:openDataDir", () => shell.openPath(dataDir()));

  ipcMain.handle("plaidsync:request", (_e, method: string, path: string, body?: unknown) => {
    const ep = supervisor.endpoints();
    if (!ep) return { status: 503, body: { error: "the services are not running" } };
    if (!["GET", "POST", "DELETE"].includes(method) || typeof path !== "string" || !path.startsWith("/v1/")) {
      throw new Error("invalid request");
    }
    return proxy(ep.plaidsync.url, ep.plaidsync.token, method, path, body);
  });

  ipcMain.handle("topper:get", (_e, path: string, search: string) => {
    const ep = supervisor.endpoints();
    if (!ep) return { status: 503, body: { error: "the services are not running" } };
    if (typeof path !== "string" || !path.startsWith("/v1/") || path.includes("?") || typeof search !== "string") {
      throw new Error("invalid request");
    }
    return proxy(ep.topper.url, ep.topper.token, "GET", search ? `${path}?${search}` : path);
  });

  ipcMain.handle("topper:post", (_e, table: string, rows: unknown) => {
    const ep = supervisor.endpoints();
    if (!ep) return { status: 503, body: { error: "the services are not running" } };
    if (!writable.has(table) || rows === null || typeof rows !== "object") throw new Error("invalid request");
    return proxy(ep.topper.url, ep.topper.token, "POST", `/v1/${table}`, rows);
  });

  ipcMain.handle("topper:delete", (_e, table: string, search: string) => {
    const ep = supervisor.endpoints();
    if (!ep) return { status: 503, body: { error: "the services are not running" } };
    // A delete without a filter would empty the table; the topper refuses
    // that too, but the renderer never has a reason to ask.
    if (!writable.has(table) || typeof search !== "string" || search === "" || search.includes("#")) {
      throw new Error("invalid request");
    }
    return proxy(ep.topper.url, ep.topper.token, "DELETE", `/v1/${table}?${search}`);
  });
}
