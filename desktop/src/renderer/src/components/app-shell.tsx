import { useEffect, useMemo, useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router";

import { ErrorBoundary } from "@/components/error-boundary";
import {
  AccountsIcon,
  CashFlowIcon,
  Mark,
  OverviewIcon,
  ProfileIcon,
  RecurringIcon,
  SearchIcon,
  SettingsIcon,
  SpendingIcon,
  SyncIcon,
  TagIcon,
  TransactionsIcon,
} from "@/components/icons";
import { KekBackupDialog } from "@/components/kek-backup-dialog";
import { LoadError } from "@/components/load-error";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useSettings } from "@/hooks/use-app-state";
import { CategoriesContext } from "@/hooks/use-categories";
import { useDataVersion } from "@/hooks/use-data-version";
import { useLoad } from "@/hooks/use-load";
import { useSyncAll } from "@/hooks/use-sync-all";
import { formatRelative } from "@/lib/format";
import { accountClass, currencyOf, primaryCurrency, signedBalance, summarize } from "@/lib/insights/accounts";
import { fmt } from "@/lib/money";
import { NEEDS_RELINK } from "@/lib/plaidsync-types";
import { loadCategories } from "@/lib/queries";
import { topper } from "@/lib/topper";
import type { AccountRow } from "@/lib/topper-types";
import { cn } from "@/lib/utils";

const nav = [
  { to: "/", label: "Overview", icon: OverviewIcon },
  { to: "/spending", label: "Spending", icon: SpendingIcon },
  { to: "/cash-flow", label: "Cash flow", icon: CashFlowIcon },
  { to: "/recurring", label: "Recurring", icon: RecurringIcon },
  { to: "/accounts", label: "Accounts", icon: AccountsIcon },
  { to: "/transactions", label: "Transactions", icon: TransactionsIcon },
] as const;

const secondary = [
  { to: "/categories", label: "Categories", icon: TagIcon },
  { to: "/profile", label: "Profile", icon: ProfileIcon },
  { to: "/settings", label: "Settings", icon: SettingsIcon },
] as const;

// AppShell is the frame around every page: the header with search and
// sync, the sidebar with navigation and balances, and the page itself. It
// also loads the category list every page depends on.
export function AppShell() {
  const { version } = useDataVersion();
  // The sync time in the header should age even when nothing reloads.
  const [minute, setMinute] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setMinute((m) => m + 1), 60_000);
    return () => clearInterval(id);
  }, []);

  const [accounts] = useLoad(() => topper.accounts({ limit: 1000 }), [version]);
  const [status] = useLoad(() => topper.syncStatus({ limit: 1000 }), [version, minute]);
  const [categories, reloadCategories] = useLoad(() => loadCategories(), [version]);
  const categoriesValue = useMemo(
    () => (categories.ok === true ? { list: categories.data, reload: reloadCategories } : null),
    [categories, reloadCategories],
  );

  const items = status.ok === true ? status.data.data.filter((i) => i.status !== "removed") : [];
  const attention = items.filter((i) => NEEDS_RELINK.has(i.status) || i.status === "error").length;

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-paper">
      <Header
        lastSync={items.reduce<string | null>((a, i) => (i.last_successful_sync_at && (!a || i.last_successful_sync_at > a) ? i.last_successful_sync_at : a), null)}
        busy={items.some((i) => i.latest_job_state === "queued" || i.latest_job_state === "running")}
        attention={attention}
        hasItems={items.length > 0}
      />
      <div className="flex min-h-0 flex-1">
        <Sidebar accounts={accounts.ok === true ? accounts.data.data : []} attention={attention} />
        <main className="min-w-0 flex-1 overflow-y-auto">
          <div className="mx-auto w-full max-w-[1360px] px-8 pt-6 pb-10">
            <ErrorBoundary>
              {categories.ok === false ? (
                <LoadError what="categories" message={categories.error} />
              ) : categoriesValue ? (
                <CategoriesContext.Provider value={categoriesValue}>
                  <Outlet />
                </CategoriesContext.Provider>
              ) : null}
            </ErrorBoundary>
          </div>
        </main>
      </div>
      <KekBackupDialog />
    </div>
  );
}

function Header({ lastSync, busy, attention, hasItems }: { lastSync: string | null; busy: boolean; attention: number; hasItems: boolean }) {
  const location = useLocation();
  const [syncing, syncAll] = useSyncAll();
  // The box shows the search Transactions is filtered by, and empties
  // anywhere else: remounting on navigation resets it.
  const initial = location.pathname === "/transactions" ? (new URLSearchParams(location.search).get("q") ?? "") : "";

  const working = syncing || busy;
  const dot = working ? "bg-stone animate-pulse" : attention ? "bg-ochre" : lastSync ? "bg-moss" : "bg-stone";

  return (
    <header className="flex h-12 shrink-0 items-center gap-4 border-b border-line-strong bg-chrome px-4">
      <div className="flex flex-1 justify-center">
        <SearchBox key={`${location.pathname}?${initial}`} initial={initial} />
      </div>
      <div className="flex items-center gap-2.5 text-[12.5px] text-ink-3">
        <span className={cn("size-[7px] rounded-full", dot)} />
        <span className="whitespace-nowrap">
          {working ? "Syncing…" : !hasItems ? "No accounts yet" : lastSync ? `Synced ${formatRelative(lastSync)}` : "Not synced yet"}
        </span>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              aria-label="Sync now"
              disabled={syncing}
              onClick={() => void syncAll()}
              className="flex size-8 cursor-pointer items-center justify-center rounded-[4px] border-0 bg-transparent text-ink-2 hover:bg-line-strong/60 disabled:cursor-default"
            >
              <SyncIcon size={16} className={syncing ? "animate-spin" : undefined} />
            </button>
          </TooltipTrigger>
          <TooltipContent>Sync every connection now</TooltipContent>
        </Tooltip>
      </div>
    </header>
  );
}

// ModeBadge marks Sandbox mode in the sidebar so test data is never
// mistaken for real money.
function ModeBadge() {
  const [settings] = useSettings();
  if (settings?.plaidEnv !== "sandbox") return null;
  return <span className="ml-auto rounded-[3px] bg-track px-1.5 py-0.5 text-[10px] font-semibold tracking-[0.04em] text-ink-3 uppercase">Sandbox</span>;
}

function SearchBox({ initial }: { initial: string }) {
  const navigate = useNavigate();
  const [q, setQ] = useState(initial);
  return (
    <form
      role="search"
      className="flex h-8 w-full max-w-[440px] items-center gap-2 rounded-[4px] border border-line-strong bg-field px-3 text-ink-3 focus-within:border-clay"
      onSubmit={(e) => {
        e.preventDefault();
        const term = q.trim();
        navigate(term ? `/transactions?q=${encodeURIComponent(term)}` : "/transactions");
      }}
    >
      <SearchIcon size={15} />
      <label htmlFor="global-search" className="sr-only">
        Search
      </label>
      <input
        id="global-search"
        type="search"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="Search transactions, merchants, amounts"
        className="min-w-0 flex-1 border-0 bg-transparent text-[13px] text-ink outline-none"
      />
    </form>
  );
}

const navClass = ({ isActive }: { isActive: boolean }) =>
  cn(
    "flex h-9 items-center gap-2.5 rounded-[4px] px-2.5 text-[13.5px] no-underline transition-colors",
    isActive ? "bg-sheet font-semibold text-ink shadow-[inset_0_0_0_1px_var(--color-line-strong)]" : "font-medium text-ink-2 hover:bg-line hover:text-ink",
  );

function Sidebar({ accounts, attention }: { accounts: AccountRow[]; attention: number }) {
  const currency = primaryCurrency(accounts);
  const mine = useMemo(() => accounts.filter((a) => currencyOf(a) === currency), [accounts, currency]);
  const summary = summarize(mine);
  const top = [...mine]
    .sort((a, b) => Math.abs(signedBalance(b.type, b.subtype, b.current_balance)) - Math.abs(signedBalance(a.type, a.subtype, a.current_balance)))
    .slice(0, 5);

  return (
    <nav aria-label="Main" className="flex w-[224px] shrink-0 flex-col gap-6 border-r border-line-strong bg-chrome px-3 pt-4 pb-4">
      <div className="flex items-center gap-2.5 px-2">
        <Mark />
        <span className="text-[15px] leading-none font-semibold tracking-[-0.01em] whitespace-nowrap">Money Insighter</span>
      </div>
      <div className="flex flex-col gap-0.5">
        {nav.map(({ to, label, icon: Icon }) => (
          <NavLink key={to} to={to} end={to === "/"} className={navClass}>
            <Icon size={17} />
            <span>{label}</span>
            {to === "/accounts" && attention > 0 && (
              <span
                className="ml-auto flex h-5 min-w-5 items-center justify-center rounded-[3px] bg-ochre-bg px-1.5 text-[11px] font-semibold text-ochre-ink"
                aria-label={`${attention} connection${attention === 1 ? "" : "s"} need${attention === 1 ? "s" : ""} attention`}
              >
                {attention}
              </span>
            )}
          </NavLink>
        ))}
      </div>
      {mine.length > 0 && (
        <div className="flex flex-col gap-2 border-t border-line-strong px-2 pt-4">
          <div className="eyebrow">Net worth</div>
          <div className="figure mb-1.5 text-[24px]">{fmt(summary.net, 2, currency)}</div>
          {top.map((a) => (
            <div key={a.account_id} className="flex items-center gap-2 text-[12.5px]" title={a.institution_name ?? undefined}>
              <span className="min-w-0 flex-1 truncate text-ink-2">{a.name}</span>
              {a.item_status !== "active" && a.item_status !== "error" && (
                <span className="size-1.5 shrink-0 rounded-full bg-ochre" aria-label="Needs attention" />
              )}
              <span className="num font-medium text-ink">
                {fmt(accountClass(a.type, a.subtype) === "credit" || accountClass(a.type, a.subtype) === "loan" ? -Number(a.current_balance ?? 0) : Number(a.current_balance ?? 0), 2, currency)}
              </span>
            </div>
          ))}
          {mine.length > top.length && (
            <NavLink to="/accounts" className="text-[12px] font-semibold text-clay-ink no-underline hover:text-clay">
              {mine.length - top.length} more
            </NavLink>
          )}
        </div>
      )}
      <div className="flex-1" />
      <div className="flex flex-col gap-0.5">
        {secondary.map(({ to, label, icon: Icon }) => (
          <NavLink key={to} to={to} className={navClass}>
            <Icon size={17} />
            <span>{label}</span>
            {to === "/profile" && <ModeBadge />}
          </NavLink>
        ))}
      </div>
    </nav>
  );
}
