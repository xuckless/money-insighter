import { join } from "node:path";

import { electronApp, is, optimizer } from "@electron-toolkit/utils";
import { app, BrowserWindow, shell } from "electron";

import { registerIpc } from "./ipc";
import { Logs } from "./logs";
import { prepareSafeStorage, SettingsStore } from "./settings";
import { Supervisor } from "./supervisor";

// MONEY_INSIGHTER_DATA_DIR moves everything the app writes (settings,
// secrets, the database, logs) elsewhere: for tests, or for a user who
// wants the data on another disk. Must be set before the ready event.
// The default would be named after package.json's name in development and
// productName when packaged; one fixed name keeps the data in one place.
app.setPath("userData", process.env.MONEY_INSIGHTER_DATA_DIR || join(app.getPath("appData"), "money-insighter"));

// One instance: two copies would fight over the Postgres data directory.
if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  main();
}

function main() {
  let window: BrowserWindow | undefined;
  let supervisor: Supervisor | undefined;
  let logs: Logs | undefined;
  let quitting = false;

  const createWindow = () => {
    window = new BrowserWindow({
      width: 1200,
      height: 800,
      minWidth: 800,
      minHeight: 560,
      show: false,
      autoHideMenuBar: true,
      title: "Money Insighter",
      webPreferences: {
        preload: join(__dirname, "../preload/index.js"),
        sandbox: true,
        contextIsolation: true,
        nodeIntegration: false,
      },
    });
    window.on("ready-to-show", () => window?.show());
    // Every link opens outside the app; the renderer never navigates.
    window.webContents.setWindowOpenHandler(({ url }) => {
      if (/^https?:/.test(url)) void shell.openExternal(url);
      return { action: "deny" };
    });
    window.webContents.on("will-navigate", (e, url) => {
      if (!url.startsWith(window?.webContents.getURL().split("#")[0] ?? "\0")) e.preventDefault();
    });
    if (is.dev && process.env.ELECTRON_RENDERER_URL) {
      void window.loadURL(process.env.ELECTRON_RENDERER_URL);
    } else {
      void window.loadFile(join(__dirname, "../renderer/index.html"));
    }
  };

  app.on("second-instance", () => {
    if (window) {
      if (window.isMinimized()) window.restore();
      window.focus();
    }
  });

  app.whenReady().then(() => {
    electronApp.setAppUserModelId("dev.xuckless.money-insighter");
    app.on("browser-window-created", (_, w) => optimizer.watchWindowShortcuts(w));

    logs = new Logs();
    const backend = prepareSafeStorage();
    const settings = new SettingsStore();
    supervisor = new Supervisor(settings, logs);
    registerIpc(supervisor, settings, logs);
    createWindow();
    logs.line("app", `Money Insighter ${app.getVersion()} starting; data in ${app.getPath("userData")}; secrets backend ${backend}`);
    void supervisor.start();

    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
  });

  // Closing the last window quits on every platform: the services must
  // not keep running invisibly.
  app.on("window-all-closed", () => app.quit());

  app.on("before-quit", (e) => {
    if (quitting || !supervisor) return;
    e.preventDefault();
    quitting = true;
    supervisor
      .stop()
      .catch((err) => logs?.line("app", `shutdown: ${err instanceof Error ? err.message : String(err)}`))
      .finally(() => {
        logs?.close();
        app.quit();
      });
  });
}
