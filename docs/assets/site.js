// Interactive parts of the documentation site: the screenshot tour, the
// setup walkthrough, the clickable architecture map, and the two charts.
// Plain ES modules, no bundler, no CDN — the site is a folder of files.
import { categoryChart, netWorthChart } from "./charts.js";

// tabs wires an ARIA tablist: click, arrow keys, and a hash that survives
// a reload so a tab can be linked to.
function tabs(root, { onChange } = {}) {
  const buttons = [...root.querySelectorAll('[role="tab"]')];
  if (!buttons.length) return;

  const select = (button, { focus = false, push = false } = {}) => {
    for (const b of buttons) {
      const on = b === button;
      b.setAttribute("aria-selected", String(on));
      b.tabIndex = on ? 0 : -1;
      const panel = document.getElementById(b.getAttribute("aria-controls"));
      if (panel) panel.hidden = !on;
    }
    if (focus) button.focus();
    if (push && button.dataset.hash) {
      history.replaceState(null, "", `#${button.dataset.hash}`);
    }
    onChange?.(button);
  };

  root.addEventListener("click", (e) => {
    const button = e.target.closest('[role="tab"]');
    if (button) select(button, { push: true });
  });

  root.addEventListener("keydown", (e) => {
    const i = buttons.indexOf(document.activeElement);
    if (i < 0) return;
    const next = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: buttons.length - 1 }[e.key];
    if (next === undefined) return;
    e.preventDefault();
    select(buttons[(next + buttons.length) % buttons.length], { focus: true, push: true });
  });

  const fromHash = buttons.find((b) => b.dataset.hash && `#${b.dataset.hash}` === location.hash);
  select(fromHash ?? buttons[0]);
}

// ---------------------------------------------------------------- the tour

const TOUR = [
  ["03-overview", "Overview", "The month at a glance: what you have spent against an even pace of the budget, what you are worth, what is coming, and anything worth a look."],
  ["04-spending", "Spending", "Every category against its budget and against what you usually spend by this day of the month. The tick on each bar is where an even pace would have you."],
  ["05-cash-flow", "Cash flow", "Your cash balance projected forward from the bills and paycheques it already knows about, with the low point marked and a cushion you set."],
  ["06-recurring", "Recurring", "Bills and subscriptions found in your own history, with the next date and what the list costs over a year. The price-change card catches a rise before it bills."],
  ["07-accounts", "Accounts", "Net worth over time, what it is made of, and how much of each card's limit is in use."],
  ["08-transactions", "Transactions", "Everything, searchable by merchant or amount. Re-categorising one row can set a rule for every transaction from that merchant."],
  ["09-categories", "Categories", "Twenty-one categories to start with. Rename them, recolour them, add your own, and merge one into another when you remove it."],
  ["10-profile", "Profile", "Your Plaid keys, and the switch between Sandbox and Production. Each mode keeps its own secret, so you enter it once."],
  ["11-settings", "Settings", "How often it syncs, the Recurring add-on, the encryption key, where the data lives, and the logs of the local services."],
];

function buildTour(section) {
  const tablist = section.querySelector(".tour-tabs");
  const panels = section.querySelector(".tour-panels");
  if (!tablist || !panels) return;

  for (const [file, name, caption] of TOUR) {
    const id = `tour-${file}`;
    const button = document.createElement("button");
    button.type = "button";
    button.role = "tab";
    button.id = `${id}-tab`;
    button.setAttribute("aria-controls", id);
    button.dataset.hash = `tour-${name.toLowerCase().replace(/\s+/g, "-")}`;
    button.textContent = name;
    tablist.append(button);

    const panel = document.createElement("div");
    panel.className = "tour-panel";
    panel.id = id;
    panel.role = "tabpanel";
    panel.setAttribute("aria-labelledby", button.id);
    panel.hidden = true;
    panel.innerHTML = `
      <div class="tour-caption"><h3>${name}</h3><p>${caption}</p></div>
      <img class="shot" src="assets/shots/${file}.png" alt="The ${name} screen of Money Insighter" loading="lazy" width="1440" height="1200">
    `;
    panels.append(panel);
  }
  // The first image is what people see before they touch anything.
  panels.querySelector("img")?.setAttribute("loading", "eager");
  tabs(tablist);
}

// ---------------------------------------------------------- the walkthrough

const STEPS = [
  {
    title: "Get a Plaid account",
    body: "Sign up at dashboard.plaid.com. Sandbox access is free and immediate, and it is all you need to try the app end to end with a test bank. Production access, for your real banks, is a separate request on the same dashboard.",
  },
  {
    title: "Copy your keys",
    body: "In the dashboard, open <strong>Developers → Keys</strong>. You want the <em>client ID</em>, which is the same in every environment, and the <em>Sandbox secret</em>. Nothing else is needed: there is no redirect URI to register and no webhook URL to host.",
  },
  {
    title: "Install and start",
    body: "Download the AppImage or the Windows installer from the releases page, or run it from source. On the first start the app asks for the keys you just copied.",
    shot: "01-setup",
  },
  {
    title: "Save the encryption key",
    body: "Before anything else, the app shows the key that encrypts your bank access tokens and asks you to save it. It lives in your operating system's keyring; if that is ever lost, this key is the only way to read the connections you already made.",
    shot: "02-key-backup",
  },
  {
    title: "Connect a bank",
    body: "Press <strong>Connect an account</strong>. Plaid's own page opens in your browser — the app never runs Link itself — and the app waits until you are done. In Sandbox, sign in with <code>user_good</code> / <code>pass_good</code>, or press <strong>Add Sandbox item</strong> to skip the browser entirely.",
    shot: "07-accounts",
  },
  {
    title: "Watch the first sync",
    body: "The first sync pulls up to two years of transactions, categorises them, and fills every screen. After that it syncs every hour while the app is open, and whenever you press sync.",
    shot: "03-overview",
  },
];

function buildWalkthrough(section) {
  const list = section.querySelector(".steps");
  const body = section.querySelector(".step-body");
  if (!list || !body) return;

  const show = (i) => {
    for (const [j, b] of buttons.entries()) b.setAttribute("aria-current", String(j === i));
    const step = STEPS[i];
    body.innerHTML = `
      <p class="eyebrow">Step ${i + 1} of ${STEPS.length}</p>
      <h3>${step.title}</h3>
      <p>${step.body}</p>
      ${step.shot ? `<img class="shot" src="assets/shots/${step.shot}.png" alt="" loading="lazy" width="1440" height="1200">` : ""}
    `;
  };

  const buttons = STEPS.map((step, i) => {
    const li = document.createElement("li");
    const button = document.createElement("button");
    button.type = "button";
    button.innerHTML = `<span class="n">${i + 1}</span><span>${step.title}</span>`;
    button.addEventListener("click", () => show(i));
    li.append(button);
    list.append(li);
    return button;
  });

  show(0);
}

// ------------------------------------------------------- architecture map

const NODES = {
  renderer: {
    title: "The window",
    body: "React, sandboxed: no Node, context isolation on, and a content security policy that loads nothing from the network — which is why the fonts are bundled. It can reach the services only through four checked IPC channels, and never sees a token, a port or a database URL.",
    path: "desktop/src/renderer/",
  },
  main: {
    title: "The main process",
    body: "The only privileged part. It decrypts the secrets, starts Postgres and both services on loopback ports, holds their bearer tokens, and validates every request the window makes. Writes can reach seven of its own tables and no others; a delete with no filter is refused here.",
    path: "desktop/src/main/ipc.ts",
  },
  plaidsync: {
    title: "plaidsync",
    body: "Owns the Plaid relationship, and is the only thing that ever talks to Plaid. It creates Hosted Link sessions, exchanges the public token, encrypts the access token before storing it, and runs the sync engine and its job queue.",
    path: "plaid-backend-golang/",
  },
  topper: {
    title: "topper",
    body: "A read-only REST layer over plaidsync's tables, plus the app's own read-write tables. It never exposes the encrypted access token or its key version. The views apply your category overrides and merchant rules in SQL, so re-categorising one transaction moves every chart at once.",
    path: "postgres-topper/",
  },
  postgres: {
    title: "Postgres",
    body: "Embedded, in your user-data directory, listening on loopback with scram-sha-256 authentication. The two services never call each other; this database is the only thing they share.",
    path: "desktop/src/main/postgres.ts",
  },
  plaid: {
    title: "Plaid",
    body: "The only thing outside your machine. Thirteen endpoints are called, all of them from one file. There is no server of ours, no telemetry and no analytics — the app has nowhere else to send anything.",
    path: "plaid-backend-golang/internal/plaid/http.go",
  },
};

function buildDiagram(section) {
  const svg = section.querySelector("svg");
  const detail = section.querySelector(".node-detail");
  if (!svg || !detail) return;

  const show = (key) => {
    const node = NODES[key];
    if (!node) return;
    for (const g of svg.querySelectorAll(".node")) {
      g.setAttribute("aria-current", String(g.dataset.node === key));
    }
    detail.innerHTML = `<h3>${node.title}</h3><p>${node.body}</p><code class="path">${node.path}</code>`;
  };

  for (const g of svg.querySelectorAll(".node")) {
    g.setAttribute("role", "button");
    g.setAttribute("tabindex", "0");
    g.addEventListener("click", () => show(g.dataset.node));
    g.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        show(g.dataset.node);
      }
    });
  }
  show("plaidsync");
}

// ------------------------------------------------------------------- boot

const section = (id) => document.getElementById(id);

if (section("tour")) buildTour(section("tour"));
if (section("walkthrough")) buildWalkthrough(section("walkthrough"));
if (section("architecture")) buildDiagram(section("architecture"));

// The demo data is inlined into the page by the capture script, so the charts
// need no fetch: one less request, no loading state, and no chance of the
// chart data going missing when the page is copied somewhere else.
const dataEl = document.getElementById("demo-data");
if (dataEl) {
  try {
    const data = JSON.parse(dataEl.textContent);
    const worth = document.getElementById("chart-net-worth");
    const cats = document.getElementById("chart-categories");
    if (worth) netWorthChart(worth, data.netWorth, data.currency);
    if (cats) categoryChart(cats, data.categoryMonths, data.categories, data.currency);
  } catch (err) {
    console.error("demo data:", err);
  }
}
