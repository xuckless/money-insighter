/* global window, document */
// Captures the screenshots the documentation is built from, by driving the
// built app with Playwright exactly as a person would.
//
//   npm run build
//   node docs/demo/generate.mjs
//   PLAID_CLIENT_ID=... PLAID_SECRET=... npm run capture:docs
//
// The app runs against a scratch data directory and Plaid Sandbox, never the
// real one. What it photographs is the real product: a real first run, a real
// Plaid link and exchange, a real /transactions/sync, real categorisation.
//
// Two things are not real, and both are disclosed in docs/demo/README.md:
//
//   * Plaid's Sandbox serves only the last 30 days of a custom user, so the
//     earlier months of the demo year are inserted into the database in the
//     shape plaidsync writes (docs/demo/backfill.json).
//   * plaidsync records a balance snapshot per day from the day an account is
//     linked, so a fresh demo has one. The earlier days are reconstructed
//     from the transaction ledger, which is exact for these accounts.
//
// Nothing real is ever photographed: the Plaid client ID, the encryption key
// and the data directory are replaced with obvious fakes in the DOM before
// the shutter, and every shot is refused if the page still contains a real
// secret.
//
// Shots are of the window, not the scrolled page: a uniform frame is what a
// tour wants, and a full-page capture of a tall screen overflows. The window
// is sized to the display it is on, never larger, because a window bigger
// than the screen is clipped by the compositor and photographs badly.
// DOCS_SCALE=2 asks for a 2x capture, which is only honoured if the display
// is big enough to hold the window at that size.
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import pg from "pg";

import { completeSetup, dbCredentials, dismissKeyBackup, launchApp, nav, waitForReady, waitForSync } from "./lib/app.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const demoDir = join(root, "docs", "demo");
const shotsDir = join(root, "docs", "assets", "shots");
const dataOutDir = join(root, "docs", "assets", "data");

// Obvious fakes. The base64 one decodes to readable English so that anyone
// who bothers to decode it can see at once that it is not a key.
const FAKE_CLIENT_ID = "65f2a1c0d4e8b7390a1f6c22";
const FAKE_SECRET = "a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4";
const FAKE_KEK = "c2FtcGxlLWRlbW8ta2V5LW5vdC1yZWFsLWRvLW5vdC11c2UtZXZlcg=";
const FAKE_DATA_DIR = process.platform === "win32"
  ? "C:\\Users\\you\\AppData\\Roaming\\money-insighter"
  : "/home/you/.config/money-insighter";

// The Canadian Sandbox institution, so the screenshots name a Canadian bank.
const INSTITUTION = "ins_43";

const clientId = process.env.PLAID_CLIENT_ID;
const secret = process.env.PLAID_SECRET;
if (!clientId || !secret) {
  console.error("set PLAID_CLIENT_ID and PLAID_SECRET (Sandbox)");
  process.exit(2);
}
if (process.env.PLAID_ENV && process.env.PLAID_ENV !== "sandbox") {
  console.error(`refusing to run with PLAID_ENV=${process.env.PLAID_ENV}; this script is Sandbox only`);
  process.exit(2);
}

// The layout is tuned from about 1280px wide, and 1440 is the design width;
// going wider only stretches the panels. The height is generous on purpose:
// these pages are three rows of panels deep, and a short window cuts the
// last row in half. Both are clamped to the display below.
const WANT = { width: 1440, height: 1200 };
// Left for the window's own chrome, so the frame is never off-screen.
const CHROME = { width: 40, height: 120 };
const wantScale = Number(process.env.DOCS_SCALE ?? 1);
const keep = process.argv.includes("--keep");
const dataDir = mkdtempSync(join(tmpdir(), "money-insighter-docs-"));
const staging = mkdtempSync(join(tmpdir(), "money-insighter-shots-"));

const customUser = JSON.parse(readFileSync(join(demoDir, "custom-user.json"), "utf8"));
const backfill = JSON.parse(readFileSync(join(demoDir, "backfill.json"), "utf8"));

const step = (msg) => console.log(`\n== ${msg}`);

// Anything that must never appear in a screenshot. Checked against the whole
// rendered page before every shot; a hit aborts the run.
const forbidden = [clientId, secret, dataDir, "money-insighter-docs-"];

// pngSize reads the dimensions out of a PNG's IHDR, so the log says what was
// actually captured rather than what was asked for.
function pngSize(path) {
  const head = readFileSync(path).subarray(16, 24);
  return `${head.readUInt32BE(0)}x${head.readUInt32BE(4)}`;
}

const shots = [];
async function shot(page, name) {
  const text = await page.evaluate(() => document.body.innerText);
  for (const needle of forbidden) {
    if (needle && text.includes(needle)) {
      throw new Error(`refusing to photograph "${name}": the page still contains a real value`);
    }
  }
  await page.waitForTimeout(600);
  const path = join(staging, `${name}.png`);
  // The window only. A full-page capture of a long screen produces an image
  // no tour can lay out, and at 2x it is large enough to upset the renderer.
  await page.screenshot({ path });
  shots.push(name);
  console.log(`  shot ${name} (${pngSize(path)})`);
}

// redact replaces the text of the first node matching selector, for nodes the
// app will not re-render before the screenshot.
const redact = (page, selector, value) =>
  page.evaluate(([s, v]) => {
    const el = document.querySelector(s);
    if (!el) throw new Error(`redact: no node matches ${s}`);
    el.textContent = v;
  }, [selector, value]);

// The display has to be known before the window can be sized to it, and the
// scale factor can only be set at launch, so the first launch is a probe:
// it reports the work area, and if the wanted scale does not fit, the app is
// relaunched once at a scale that does.
let scale = wantScale;
// Playwright forces --password-store=basic from inside the main process, so
// without this the app would truthfully report "no OS keychain" and the
// Settings screenshot would misrepresent what an ordinary desktop does.
// DOCS_PASSWORD_STORE=kwallet6 for a KDE session.
const launchEnv = process.platform === "linux"
  ? { MONEY_INSIGHTER_PASSWORD_STORE: process.env.DOCS_PASSWORD_STORE ?? "gnome-libsecret" }
  : {};
let app = await launchApp({ dataDir, env: launchEnv, args: scale === 1 ? [] : [`--force-device-scale-factor=${scale}`] });
const workArea = await app.evaluate(({ screen }) => screen.getPrimaryDisplay().workAreaSize);
const fits = (s) =>
  WANT.width * s + CHROME.width <= workArea.width && WANT.height * s + CHROME.height <= workArea.height;
if (!fits(scale)) {
  const fallback = 1;
  console.log(`display work area is ${workArea.width}x${workArea.height}; ${scale}x does not fit, using ${fallback}x`);
  await app.close();
  rmSync(dataDir, { recursive: true, force: true });
  mkdirSync(dataDir, { recursive: true });
  scale = fallback;
  app = await launchApp({ dataDir, env: launchEnv });
}
// Never ask for a window larger than the screen can show.
const viewport = {
  width: Math.min(WANT.width, Math.floor((workArea.width - CHROME.width) / scale)),
  height: Math.min(WANT.height, Math.floor((workArea.height - CHROME.height) / scale)),
};
console.log(`display ${workArea.width}x${workArea.height}, capturing ${viewport.width}x${viewport.height} at ${scale}x`);

try {
  const page = await app.firstWindow();
  page.on("pageerror", (err) => console.error("renderer error:", err));

  step("first run");
  await page.setViewportSize(viewport);
  await completeSetup(page, {
    clientId,
    secret,
    // The only moment the setup screen can be photographed is with the
    // fields filled; they hold the real keys, so shoot a fake pair first.
    onFilled: async () => {
      await page.getByLabel("Client ID").fill(FAKE_CLIENT_ID);
      await page.getByLabel("Sandbox secret").fill(FAKE_SECRET);
      await shot(page, "01-setup");
      await page.getByLabel("Client ID").fill(clientId);
      await page.getByLabel("Sandbox secret").fill(secret);
    },
  });

  step("stack starting (initdb + migrations)");
  const state = await waitForReady(page);
  console.log("services:", JSON.stringify(state.services));

  step("key backup");
  // The dialog's only state transitions are "copied" and "done", so the node
  // survives untouched until the screenshot is taken.
  const realKey = (await page.locator("code").first().innerText()).trim();
  forbidden.push(realKey);
  await redact(page, "code", FAKE_KEK);
  await shot(page, "02-key-backup");
  await dismissKeyBackup(page);
  await nav(page, "Accounts").waitFor({ timeout: 10_000 });

  step("linking the demo bank through Plaid Sandbox");
  const linked = await page.evaluate(
    ([institution, cfg]) =>
      window.api.plaidsync.request("POST", "/v1/sandbox/items", {
        institution_id: institution,
        override_username: "user_custom",
        user_config: cfg,
        days_requested: 730,
      }),
    [INSTITUTION, customUser],
  );
  if (linked.status !== 201) throw new Error(`link: ${linked.status} ${JSON.stringify(linked.body).slice(0, 300)}`);
  const item = await waitForSync(page);
  console.log(`item ${item.item_id} synced at ${item.last_successful_sync_at}`);

  step("backfilling the rest of the demo year");
  const { password, port } = await dbCredentials(app, dataDir);
  const client = new pg.Client({ host: "127.0.0.1", port, user: "plaidsync", password, database: "plaidsync" });
  await client.connect();
  try {
    const { rows: accounts } = await client.query(
      "SELECT account_id, mask, type FROM plaid_accounts WHERE item_id = $1",
      [item.item_id],
    );
    const byMask = new Map(accounts.map((a) => [a.mask, a]));
    const idFor = new Map(
      backfill.accounts.map((a) => {
        const found = byMask.get(a.mask);
        if (!found) throw new Error(`no synced account with mask ${a.mask}`);
        return [a.key, found.account_id];
      }),
    );

    // Written one statement per column array so the whole backfill is a
    // single round trip and a single transaction.
    const t = backfill.transactions;
    await client.query("BEGIN");
    await client.query(
      `INSERT INTO transactions (
         transaction_id, account_id, item_id, amount, iso_currency_code, date, authorized_date,
         name, merchant_name, pending, pfc_primary, pfc_detailed, pfc_confidence, raw)
       SELECT * FROM UNNEST(
         $1::text[], $2::text[], $3::text[], $4::numeric[], $5::text[], $6::date[], $7::date[],
         $8::text[], $9::text[], $10::boolean[], $11::text[], $12::text[], $13::text[], $14::jsonb[])
       ON CONFLICT (transaction_id) DO NOTHING`,
      [
        t.map((r) => `${item.item_id}-${r.id}`),
        t.map((r) => idFor.get(r.account)),
        t.map(() => item.item_id),
        t.map((r) => r.amount),
        t.map(() => "CAD"),
        t.map((r) => r.date),
        t.map((r) => r.authorized_date),
        t.map((r) => r.name),
        t.map((r) => r.merchant_name),
        t.map(() => false),
        t.map((r) => r.pfc_primary),
        t.map((r) => r.pfc_detailed),
        t.map(() => "VERY_HIGH"),
        t.map(() => JSON.stringify({ demo: true, source: "docs/demo/backfill.json" })),
      ],
    );

    // Balance history. plaidsync records one snapshot a day from the day an
    // account is linked, so only today exists; every earlier day is today's
    // balance wound back through the posted ledger. Exact for these accounts.
    await client.query(
      `INSERT INTO account_balance_snapshots (account_id, day, current_balance, available_balance, credit_limit, iso_currency_code)
       SELECT a.account_id, d.day,
              a.current_balance + CASE WHEN a.type = 'credit' THEN -1 ELSE 1 END *
                COALESCE((SELECT SUM(x.amount) FROM transactions x
                          WHERE x.account_id = a.account_id AND x.removed_at IS NULL
                            AND x.pending = false AND x.date > d.day), 0),
              NULL, a.credit_limit, a.iso_currency_code
         FROM plaid_accounts a
         CROSS JOIN generate_series($2::date, current_date - interval '1 day', interval '1 day') AS d(day)
        WHERE a.item_id = $1
       ON CONFLICT (account_id, day) DO NOTHING`,
      // From the demo's first transaction, not a flat twelve months back: a
      // day before the ledger starts would wind back to a meaningless
      // balance and put a false dip at the left edge of every chart.
      [item.item_id, backfill.transactions[0].date],
    );
    await client.query("COMMIT");

    const { rows: counted } = await client.query(
      "SELECT count(*)::int AS n, min(date) AS oldest FROM transactions WHERE item_id = $1",
      [item.item_id],
    );
    console.log(`transactions now ${counted[0].n}, oldest ${counted[0].oldest.toISOString().slice(0, 10)}`);
  } catch (err) {
    await client.query("ROLLBACK").catch(() => {});
    throw err;
  } finally {
    await client.end();
  }

  step("budgets and rules the demo person has set");
  const budgets = JSON.parse(readFileSync(join(demoDir, "budgets.json"), "utf8"));
  const posted = await page.evaluate((b) => window.api.topper.post("budgets", b), budgets);
  if (posted.status >= 300) throw new Error(`budgets: ${posted.status} ${JSON.stringify(posted.body)}`);

  step("recurring add-on");
  await page.evaluate(() => window.api.app.updateSettings({ recurringEnabled: true }));
  await page.getByText("Starting Money Insighter").waitFor({ timeout: 30_000 }).catch(() => {});
  await nav(page, "Recurring").waitFor({ timeout: 120_000 });
  const deadline = Date.now() + 120_000;
  for (;;) {
    const r = await page.evaluate(() => window.api.topper.get("/v1/views/sync/status", "select=recurring_checked_at,recurring_error_code"));
    const row = r.body?.data?.[0];
    if (row?.recurring_checked_at) {
      console.log(`recurring checked, error ${row.recurring_error_code ?? "none"}`);
      break;
    }
    if (Date.now() > deadline) throw new Error("recurring never checked");
    await new Promise((res) => setTimeout(res, 2000));
  }

  step("screens");
  await page.reload();
  await page.setViewportSize(viewport);
  await nav(page, "Overview").waitFor({ timeout: 60_000 });
  // Fonts must be settled or a shot can catch Newsreader mid-swap.
  await page.evaluate(() => document.fonts.ready);

  await nav(page, "Overview").click();
  await page.getByText("Where it went").waitFor({ timeout: 30_000 });
  await shot(page, "03-overview");

  await nav(page, "Spending").click();
  await page.getByText("Sort the unknowns").waitFor({ timeout: 30_000 });
  await shot(page, "04-spending");

  await nav(page, "Cash flow").click();
  await page.getByRole("heading", { name: "Safe to spend" }).waitFor({ timeout: 30_000 });
  await shot(page, "05-cash-flow");

  await nav(page, "Recurring").click();
  await page.getByText(/leaves on autopilot/).waitFor({ timeout: 30_000 });
  await shot(page, "06-recurring");

  await nav(page, "Accounts").click();
  await page.getByText("What it’s made of").waitFor({ timeout: 30_000 });
  await shot(page, "07-accounts");

  await nav(page, "Transactions").click();
  await page.getByRole("button", { name: /Needs a category|Dining|Groceries|Transport|Shopping|Other|Travel|Transfers|Income/ }).first().waitFor({ timeout: 30_000 });
  await shot(page, "08-transactions");

  await nav(page, "Categories").click();
  await page.getByText("Merchant rules", { exact: true }).waitFor({ timeout: 30_000 });
  await shot(page, "09-categories");

  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: /^Profile/ }).click();
  await page.getByText("Plaid keys", { exact: true }).waitFor({ timeout: 15_000 });
  await page.getByLabel("Client ID").fill(FAKE_CLIENT_ID);
  await shot(page, "10-profile");

  await nav(page, "Settings").click();
  await page.getByRole("switch", { name: "Recurring transactions" }).waitFor({ timeout: 15_000 });
  await page.getByText(/plaidsync is ready|http server listening/).first().waitFor({ timeout: 15_000 });
  // The data directory is a scratch path, and plaidsync prints the Plaid
  // client ID when it starts, so the log panel is rewritten wholesale.
  await page.evaluate(
    ([dir, real, fake]) => {
      for (const el of document.querySelectorAll("*")) {
        if (el.children.length === 0 && el.textContent?.includes(dir)) el.textContent = el.textContent.replaceAll(dir, fake);
        if (el.children.length === 0 && real && el.textContent?.includes(real)) el.textContent = el.textContent.replaceAll(real, "<client id>");
      }
    },
    [dataDir, clientId, FAKE_DATA_DIR],
  );
  await shot(page, "11-settings");

  step("deriving the data the site charts");
  // The site needs series, not tables: the raw views are hundreds of
  // kilobytes and carry Plaid's account and item ids, which have no business
  // in a public repository.
  const PAGE = 1000;
  const read = async (view) => {
    const all = [];
    for (let offset = 0; ; offset += PAGE) {
      const r = await page.evaluate(([v, q]) => window.api.topper.get(v, q), [view, `limit=${PAGE}&offset=${offset}`]);
      if (r.status >= 300) throw new Error(`${view}: ${r.status} ${JSON.stringify(r.body).slice(0, 200)}`);
      const rows = r.body.data ?? [];
      all.push(...rows);
      if (rows.length < PAGE) break;
    }
    return all;
  };
  const round = (v) => Math.round(Number(v) * 100) / 100;

  const accountRows = await read("/v1/views/accounts");
  const balanceRows = await read("/v1/views/balances/daily");
  const categoryRows = await read("/v1/views/categories/monthly");
  const categoryDefs = await read("/v1/categories");

  // Net worth is what you own minus what you owe, per day, the same sum the
  // Accounts screen draws.
  const sign = new Map(accountRows.map((a) => [a.account_id, a.type === "credit" || a.type === "loan" ? -1 : 1]));
  const perDay = new Map();
  for (const row of balanceRows) {
    const s = sign.get(row.account_id) ?? 1;
    perDay.set(row.day, (perDay.get(row.day) ?? 0) + s * Number(row.current_balance ?? 0));
  }
  const netWorth = [...perDay.entries()].sort((a, b) => a[0].localeCompare(b[0])).map(([day, v]) => [day, round(v)]);

  const charts = {
    generated_on: backfill.generated_on,
    currency: "CAD",
    note: "Derived from the demo dataset by desktop/scripts/capture-docs.mjs. No real account data.",
    accounts: accountRows.map((a) => ({
      name: a.name, mask: a.mask, type: a.type, subtype: a.subtype,
      balance: round(a.current_balance), limit: a.credit_limit === null ? null : round(a.credit_limit),
    })),
    categories: categoryDefs.map((c) => ({ id: c.id, label: c.label, color: c.color, kind: c.kind })),
    netWorth,
    categoryMonths: categoryRows.map((r) => ({ month: r.month.slice(0, 7), category: r.category, amount: round(r.amount) })),
  };
  mkdirSync(dataOutDir, { recursive: true });
  writeFileSync(join(dataOutDir, "charts.json"), JSON.stringify(charts));
  const size = statSync(join(dataOutDir, "charts.json")).size;
  console.log(`  charts.json ${netWorth.length} days, ${charts.categoryMonths.length} category-months (${(size / 1024).toFixed(0)} kB)`);
  if (size > 250_000) throw new Error(`charts.json is ${size} bytes; trim it`);

  // The site reads the data from the page itself, so the charts need no
  // fetch and docs/index.html can be opened straight off disk.
  const indexPath = join(root, "docs", "index.html");
  const index = readFileSync(indexPath, "utf8");
  const start = "<!-- demo-data:start -->";
  const end = "<!-- demo-data:end -->";
  const from = index.indexOf(start);
  const to = index.indexOf(end);
  if (from < 0 || to < 0) throw new Error("docs/index.html has lost its demo-data markers");
  const block = `\n<script type="application/json" id="demo-data">${JSON.stringify(charts)}</script>\n`;
  writeFileSync(indexPath, index.slice(0, from + start.length) + block + index.slice(to));
  console.log("  inlined into docs/index.html");

  step("publishing");
  mkdirSync(shotsDir, { recursive: true });
  let total = 0;
  for (const name of shots) {
    const from = join(staging, `${name}.png`);
    const size = statSync(from).size;
    total += size;
    if (size > 1_500_000) throw new Error(`${name}.png is ${(size / 1e6).toFixed(1)} MB; trim it or lower DOCS_SCALE`);
    writeFileSync(join(shotsDir, `${name}.png`), readFileSync(from));
  }
  console.log(`wrote ${shots.length} screenshots to docs/assets/shots (${(total / 1e6).toFixed(1)} MB)`);

  await app.close();
  console.log("\nCAPTURE OK");
} catch (err) {
  console.error("\nCAPTURE FAILED:", err);
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
  rmSync(staging, { recursive: true, force: true });
  if (!keep && process.exitCode !== 1) rmSync(dataDir, { recursive: true, force: true });
  else console.log(`kept ${dataDir} (${readdirSync(dataDir).join(", ")})`);
}
