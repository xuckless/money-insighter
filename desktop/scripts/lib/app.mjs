/* global window */
// Shared plumbing for the scripts that drive the built app with Playwright:
// scripts/smoke.mjs (assert that it works) and scripts/capture-docs.mjs
// (photograph it for the documentation). The knowledge here is the kind
// that must not be duplicated and then diverge.
import { _electron as electron } from "playwright";

// launchApp starts the built app against a scratch data directory.
//
// Playwright passes --password-store=basic, which disables the OS keyring;
// a later occurrence of the switch wins, so ask for the real one on Linux.
// Without this the app falls back to Electron's plain-text backend and the
// Settings page correctly reports that secrets are only obfuscated.
export async function launchApp({ dataDir, args = [], env = {} } = {}) {
  const executable = process.env.SMOKE_EXECUTABLE;
  const app = await electron.launch({
    ...(executable ? { executablePath: executable } : {}),
    args: [
      ...(executable ? [] : ["."]),
      ...(process.platform === "linux" ? ["--password-store=gnome-libsecret"] : []),
      ...args,
    ],
    env: { ...process.env, MONEY_INSIGHTER_DATA_DIR: dataDir, ELECTRON_ENABLE_LOGGING: "1", ...env },
    timeout: 60_000,
  });
  app.process().stderr?.on("data", (d) => process.stderr.write(`[electron] ${d}`));
  return app;
}

// nav returns the sidebar link with the given name.
export const nav = (page, name) =>
  page.getByRole("navigation", { name: "Main" }).getByRole("link", { name, exact: true });

// completeSetup fills the first-run screen and starts the stack. onFilled, if
// given, runs after the fields hold values but before the form is submitted —
// that is the only moment the setup screen can be photographed.
export async function completeSetup(page, { clientId, secret, onFilled } = {}) {
  await page.getByText("Welcome to Money Insighter").waitFor({ timeout: 30_000 });
  await page.getByLabel("Client ID").fill(clientId);
  await page.getByLabel("Sandbox secret").fill(secret);
  if (onFilled) await onFilled(page);
  await page.getByRole("button", { name: "Save and start" }).click();
}

// waitForReady waits for the stack to come up. The key-backup dialog opens as
// soon as the shell renders and marks the rest of the page aria-hidden, so it
// is the first thing to wait for; initdb and the migrations make this slow on
// a first run.
export async function waitForReady(page, { timeout = 240_000 } = {}) {
  await page.getByText("Back up your encryption key").waitFor({ timeout });
  const state = await page.evaluate(() => window.api.app.getState());
  if (state.phase !== "ready") throw new Error(`phase ${state.phase}: ${state.message}`);
  return state;
}

// dismissKeyBackup accepts the key-backup dialog and waits for it to close.
export async function dismissKeyBackup(page) {
  await page.getByRole("button", { name: "I saved it" }).click();
  await page.getByText("Back up your encryption key").waitFor({ state: "hidden", timeout: 10_000 });
}

// waitForSync polls until the given item reports a successful sync.
export async function waitForSync(page, { timeout = 120_000 } = {}) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const r = await page.evaluate(() => window.api.plaidsync.request("GET", "/v1/items"));
    const item = r.body.items?.[0];
    if (item?.last_successful_sync_at) return item;
    if (Date.now() > deadline) throw new Error(`item never synced: ${JSON.stringify(item)}`);
    await new Promise((res) => setTimeout(res, 2000));
  }
}

// dbCredentials reads the embedded cluster's port and password.
//
// The files are read here, in Node; only the decryption happens inside the
// app, because safeStorage is the one thing this side cannot do. The main
// process is bundled as an ES module with no require and no dynamic import,
// so the evaluated body must lean on globals alone — Buffer is one.
//
// Both values belong to a throwaway data directory and neither is printed.
export async function dbCredentials(app, dataDir) {
  const { readFileSync } = await import("node:fs");
  const { join } = await import("node:path");
  // Line 4 of postmaster.pid is the port the cluster is listening on.
  const pid = readFileSync(join(dataDir, "postgres", "postmaster.pid"), "utf8").split("\n");
  const blob = Array.from(readFileSync(join(dataDir, "secrets.bin")));
  const secrets = await app.evaluate(
    ({ safeStorage }, bytes) => JSON.parse(safeStorage.decryptString(Buffer.from(bytes))),
    blob,
  );
  return { password: secrets.dbPassword, port: Number(pid[3]) };
}
