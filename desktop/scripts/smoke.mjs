/* global window */
// End-to-end smoke test of the built app against Plaid Sandbox, driven
// with Playwright's Electron support. It runs the real main process with
// a scratch data directory: first-run setup, the stack coming up (initdb,
// migrations), the key-backup prompt, a Sandbox item syncing, the pages
// showing rows, a Hosted Link session being created, and a clean quit.
//
//   npm run build
//   PLAID_CLIENT_ID=... PLAID_SECRET=... node scripts/smoke.mjs
//
// SMOKE_EXECUTABLE=dist/linux-unpacked/money-insighter runs the packaged
// app instead of the development build; --keep leaves the data directory.
//
// Not a unit test: it needs network access to Plaid and takes a minute.
import { mkdtempSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { _electron as electron } from "playwright";

const clientId = process.env.PLAID_CLIENT_ID;
const secret = process.env.PLAID_SECRET;
if (!clientId || !secret) {
  console.error("set PLAID_CLIENT_ID and PLAID_SECRET (Sandbox)");
  process.exit(2);
}
const keep = process.argv.includes("--keep");
const dataDir = process.env.SMOKE_DATA_DIR ?? mkdtempSync(join(tmpdir(), "money-insighter-smoke-"));
console.log(`data dir: ${dataDir}`);

const step = (msg) => console.log(`\n== ${msg}`);
// Playwright passes --password-store=basic, which disables the OS keyring;
// a later occurrence of the switch wins, so ask for the real one on Linux.
const app = await electron.launch({
  ...(process.env.SMOKE_EXECUTABLE ? { executablePath: process.env.SMOKE_EXECUTABLE } : {}),
  args: [...(process.env.SMOKE_EXECUTABLE ? [] : ["."]), ...(process.platform === "linux" ? ["--password-store=gnome-libsecret"] : [])],
  env: { ...process.env, MONEY_INSIGHTER_DATA_DIR: dataDir, ELECTRON_ENABLE_LOGGING: "1" },
  timeout: 60_000,
});
app.process().stderr?.on("data", (d) => process.stderr.write(`[electron] ${d}`));
try {
  const page = await app.firstWindow();
  page.on("pageerror", (err) => console.error("renderer error:", err));
  page.on("console", (m) => {
    if (m.type() === "error") console.error("renderer console:", m.text());
  });

  step("setup screen");
  await page.getByText("Welcome to Money Insighter").waitFor({ timeout: 30_000 });
  await page.getByLabel("Client ID").fill(clientId);
  await page.getByLabel("Sandbox secret").fill(secret);
  await page.getByRole("button", { name: "Save and start" }).click();

  step("stack starting (initdb + migrations)");
  // The key-backup dialog opens as soon as the shell renders and marks the
  // rest of the page aria-hidden, so it is the first thing to wait for.
  await page.getByText("Back up your encryption key").waitFor({ timeout: 240_000 });
  const state = await page.evaluate(() => window.api.app.getState());
  console.log("services:", JSON.stringify(state.services));
  if (state.phase !== "ready") throw new Error(`phase ${state.phase}: ${state.message}`);

  step("key backup prompt");
  const keyText = await page.locator("code").first().innerText();
  if (!/^[A-Za-z0-9+/]{43}=$/.test(keyText.trim())) throw new Error(`unexpected key text ${keyText}`);
  await page.getByRole("button", { name: "I saved it" }).click();
  await page.getByText("Back up your encryption key").waitFor({ state: "hidden", timeout: 10_000 });
  await page.getByRole("link", { name: "Connections", exact: true }).waitFor({ timeout: 10_000 });

  step("sandbox item");
  await page.getByRole("link", { name: "Connections", exact: true }).click();
  await page.getByRole("button", { name: /Add Sandbox item/ }).click();
  // The card appears with the institution and the initial job runs.
  await page.getByText("First Platypus Bank").first().waitFor({ timeout: 60_000 });
  const deadline = Date.now() + 120_000;
  let items;
  for (;;) {
    const r = await page.evaluate(() => window.api.plaidsync.request("GET", "/v1/items"));
    items = r.body.items;
    const it = items[0];
    if (it && it.last_successful_sync_at) break;
    if (Date.now() > deadline) throw new Error(`item never synced: ${JSON.stringify(it)}`);
    await new Promise((r) => setTimeout(r, 2000));
  }
  console.log("item:", items[0].item_id, items[0].status, "synced", items[0].last_successful_sync_at);

  step("pages");
  await page.getByRole("link", { name: "Accounts", exact: true }).click();
  await page.getByText("Plaid Checking").first().waitFor({ timeout: 30_000 });
  await page.getByRole("link", { name: "Transactions", exact: true }).click();
  await page.locator("table tbody tr").first().waitFor({ timeout: 30_000 });
  const rows = await page.locator("table tbody tr").count();
  console.log(`transactions page rows: ${rows}`);
  if (rows === 0) throw new Error("no transactions rendered");
  await page.getByRole("link", { name: "Overview", exact: true }).click();
  await page.getByText("Recent transactions").waitFor({ timeout: 30_000 });

  step("hosted link session (created, not opened)");
  const hosted = await page.evaluate(() => window.api.plaidsync.request("POST", "/v1/link/hosted", {}));
  if (hosted.status !== 201 || !hosted.body.hosted_link_url?.startsWith("https://")) {
    throw new Error(`hosted link: ${hosted.status} ${JSON.stringify(hosted.body)}`);
  }
  console.log("hosted_link_url:", hosted.body.hosted_link_url.replace(/token=.*/, "token=…"));
  const status = await page.evaluate(
    (t) => window.api.plaidsync.request("POST", "/v1/link/hosted/status", { link_token: t }),
    hosted.body.link_token,
  );
  if (status.status !== 200 || status.body.status !== "pending") throw new Error(`hosted status: ${JSON.stringify(status)}`);
  console.log("hosted status:", status.body);

  step("logs page");
  await page.getByRole("link", { name: "Logs", exact: true }).click();
  await page.getByText(/plaidsync is ready|http server listening/).first().waitFor({ timeout: 15_000 });

  step("quit");
  await app.close();
  console.log("data dir contents:", readdirSync(dataDir).join(", "));
  console.log("\nSMOKE OK");
} catch (err) {
  console.error("\nSMOKE FAILED:", err);
  try {
    const page = await app.firstWindow();
    await page.screenshot({ path: join(dataDir, "failure.png") });
    console.error(`screenshot: ${join(dataDir, "failure.png")}`);
  } catch {
    // no window
  }
  await app.close().catch(() => {});
  process.exitCode = 1;
} finally {
  if (!keep && process.exitCode !== 1) rmSync(dataDir, { recursive: true, force: true });
  else console.log(`kept ${dataDir}`);
}
