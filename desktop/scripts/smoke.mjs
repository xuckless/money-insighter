/* global window */
// End-to-end smoke test of the built app against Plaid Sandbox, driven
// with Playwright's Electron support. It runs the real main process with
// a scratch data directory: first-run setup, the stack coming up (initdb,
// migrations), the key-backup prompt, a Sandbox item syncing, every screen
// rendering, the app's own tables accepting writes, the Recurring add-on
// fetching streams after a restart, a Hosted Link session being created,
// and a clean quit. SMOKE_SCREENSHOTS=dir saves a PNG of each screen.
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

import { completeSetup, dismissKeyBackup, launchApp, nav, waitForReady, waitForSync } from "./lib/app.mjs";

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
const shots = process.env.SMOKE_SCREENSHOTS;
const shot = async (page, name) => {
  if (!shots) return;
  await page.waitForTimeout(600);
  await page.screenshot({ path: join(shots, `${name}.png`), fullPage: true });
};
const app = await launchApp({ dataDir });
try {
  const page = await app.firstWindow();
  page.on("pageerror", (err) => console.error("renderer error:", err));
  page.on("console", (m) => {
    if (m.type() === "error") console.error("renderer console:", m.text());
  });

  step("setup screen");
  await completeSetup(page, { clientId, secret });

  step("stack starting (initdb + migrations)");
  const state = await waitForReady(page);
  console.log("services:", JSON.stringify(state.services));

  step("key backup prompt");
  const keyText = await page.locator("code").first().innerText();
  if (!/^[A-Za-z0-9+/]{43}=$/.test(keyText.trim())) throw new Error(`unexpected key text ${keyText}`);
  await dismissKeyBackup(page);
  await nav(page, "Accounts").waitFor({ timeout: 10_000 });
  await page.setViewportSize({ width: 1440, height: 1000 });

  step("sandbox item");
  await nav(page, "Accounts").click();
  await page.getByRole("button", { name: /Add Sandbox item/ }).click();
  // The row appears with the institution and the initial job runs.
  await page.getByText("First Platypus Bank").first().waitFor({ timeout: 60_000 });
  const item = await waitForSync(page);
  console.log("item:", item.item_id, item.status, "synced", item.last_successful_sync_at);

  step("screens");
  // Reload so every screen reads the synced data.
  await page.reload();
  await nav(page, "Accounts").click();
  await page.getByText("Plaid Checking").first().waitFor({ timeout: 30_000 });
  await page.getByText("What it’s made of").waitFor({ timeout: 30_000 });
  await shot(page, "accounts");
  await nav(page, "Transactions").click();
  await page.getByRole("button", { name: /Needs a category|Dining|Groceries|Transport|Shopping|Other|Travel|Transfers|Income/ }).first().waitFor({ timeout: 30_000 });
  const listed = await page.evaluate(() => window.api.topper.get("/v1/views/transactions/categorized", "limit=1&count=exact"));
  console.log(`categorized transactions: ${listed.body.total}`);
  if (!listed.body.total) throw new Error("no transactions in transactions/categorized");
  await shot(page, "transactions");
  await nav(page, "Spending").click();
  await page.getByText("Sort the unknowns").waitFor({ timeout: 30_000 });
  await shot(page, "spending");
  await nav(page, "Cash flow").click();
  await page.getByText("In and out").waitFor({ timeout: 30_000 });
  await shot(page, "cashflow-off");
  await nav(page, "Overview").click();
  await page.getByText("Where it went").waitFor({ timeout: 30_000 });
  await shot(page, "overview-off");
  await nav(page, "Categories").click();
  await page.getByText("Merchant rules", { exact: true }).waitFor({ timeout: 30_000 });
  await shot(page, "categories");

  step("user data writes");
  const cats = await page.evaluate(() => window.api.topper.get("/v1/categories", "limit=100"));
  if (!cats.body.data?.some((c) => c.id === "medical" && c.builtin)) throw new Error(`categories not seeded: ${JSON.stringify(cats.body).slice(0, 200)}`);
  const custom = await page.evaluate(() =>
    window.api.topper.post("categories", { id: "c_hobbies", label: "Hobbies", color: "#6B6FA3", icon: "palette", kind: "spending", builtin: false, sort_order: 500 }),
  );
  if (custom.status >= 300) throw new Error(`custom category: ${custom.status} ${JSON.stringify(custom.body)}`);
  const hobbyBudget = await page.evaluate(() => window.api.topper.post("budgets", { category: "c_hobbies", monthly_amount: "50" }));
  if (hobbyBudget.status >= 300) throw new Error(`budget on custom category: ${hobbyBudget.status}`);
  const gone = await page.evaluate(() => window.api.topper.delete("categories", "id=eq.c_hobbies"));
  if (gone.status >= 300) throw new Error(`delete custom category: ${gone.status} ${JSON.stringify(gone.body)}`);
  const leftover = await page.evaluate(() => window.api.topper.get("/v1/budgets", "category=eq.c_hobbies"));
  if (leftover.body.data?.length) throw new Error("budget survived its category");
  const entry = await page.evaluate(() =>
    window.api.topper.post("recurring_entries", { id: "smoke-rent", name: "Rent", amount: "1500", direction: "outflow", frequency: "MONTHLY", next_date: "2030-01-01", category: "housing" }),
  );
  if (entry.status >= 300) throw new Error(`recurring entry: ${entry.status} ${JSON.stringify(entry.body)}`);
  const budget = await page.evaluate(() => window.api.topper.post("budgets", [{ category: "dining", monthly_amount: "600" }]));
  if (budget.status !== 200 && budget.status !== 201) throw new Error(`budget upsert: ${budget.status} ${JSON.stringify(budget.body)}`);
  const bad = await page.evaluate(() => window.api.topper.post("budgets", [{ category: "income", monthly_amount: "1" }]));
  if (bad.status < 400) throw new Error(`a budget for income was accepted: ${bad.status}`);
  const rule = await page.evaluate(() => window.api.topper.post("merchant_rules", { merchant_key: "uber", category: "travel" }));
  if (rule.status >= 300) throw new Error(`merchant rule: ${rule.status} ${JSON.stringify(rule.body)}`);
  const ruled = await page.evaluate(() => window.api.topper.get("/v1/views/transactions/categorized", "merchant_key=eq.uber&select=category,category_source&limit=1"));
  console.log("uber after rule:", JSON.stringify(ruled.body.data));
  if (ruled.body.data.length && ruled.body.data[0].category !== "travel") throw new Error("merchant rule not applied");
  const refused = await page.evaluate(() => window.api.topper.delete("preferences", "").then(() => "sent", (e) => e.message));
  if (refused === "sent") throw new Error("an unfiltered delete was let through");

  step("recurring add-on");
  await page.evaluate(() => window.api.app.updateSettings({ recurringEnabled: true }));
  // The services restart; the shell comes back once they are ready.
  await page.getByText("Starting Money Insighter").waitFor({ timeout: 30_000 }).catch(() => {});
  await nav(page, "Recurring").waitFor({ timeout: 120_000 });
  const recurringDeadline = Date.now() + 120_000;
  let checked;
  for (;;) {
    const r = await page.evaluate(() => window.api.topper.get("/v1/views/sync/status", "select=recurring_checked_at,recurring_error_code"));
    checked = r.body?.data?.[0];
    if (checked?.recurring_checked_at) break;
    if (Date.now() > recurringDeadline) throw new Error(`recurring never checked: ${JSON.stringify(r.body)}`);
    await new Promise((res) => setTimeout(res, 2000));
  }
  const streams = await page.evaluate(() => window.api.topper.get("/v1/views/recurring/streams", "limit=100"));
  console.log(`recurring: checked, error ${checked.recurring_error_code ?? "none"}, ${streams.body.data.length} streams`);
  if (checked.recurring_error_code) throw new Error(`recurring refresh failed: ${checked.recurring_error_code}`);
  await page.reload();
  await nav(page, "Recurring").click();
  // Detected streams and the hand-added rent show even when Plaid has none.
  await page.getByText(/leaves on autopilot/).waitFor({ timeout: 30_000 });
  await page.getByText("Rent", { exact: true }).first().waitFor({ timeout: 30_000 });
  await shot(page, "recurring");
  await nav(page, "Cash flow").click();
  await page.getByRole("heading", { name: "Safe to spend" }).waitFor({ timeout: 30_000 });
  await shot(page, "cashflow");
  await nav(page, "Overview").click();
  await page.getByText("Coming up").waitFor({ timeout: 30_000 });
  await shot(page, "overview");

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

  step("profile");
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: /^Profile/ }).click();
  await page.getByText("Plaid keys", { exact: true }).waitFor({ timeout: 15_000 });
  await page.getByRole("button", { name: "Production" }).click();
  await page.getByText("Connections belong to the mode they were made in.").waitFor({ timeout: 5_000 });
  await shot(page, "profile-switch");
  await page.getByRole("button", { name: "Cancel" }).click();
  // Switching without a Production secret is refused by the main process.
  const refusedSwitch = await page.evaluate(() => window.api.app.updateSettings({ plaidEnv: "production" }).then(() => "switched", (e) => e.message));
  if (!/enter the production secret/.test(refusedSwitch)) throw new Error(`switch without a secret: ${refusedSwitch}`);

  step("settings and logs");
  await nav(page, "Settings").click();
  await page.getByRole("switch", { name: "Recurring transactions" }).waitFor({ timeout: 15_000 });
  await page.getByText(/plaidsync is ready|http server listening/).first().waitFor({ timeout: 15_000 });
  await shot(page, "settings");

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
