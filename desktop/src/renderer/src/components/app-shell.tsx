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
  TransactionsIcon,
} from "@/components/icons";
import { KekBackupDialog } from "@/components/kek-backup-dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useSettings } from "@/hooks/use-app-state";
import { useDataVersion } from "@/hooks/use-data-version";
import { useLoad } from "@/hooks/use-load";
import { useSyncAll } from "@/hooks/use-sync-all";
import { formatRelative } from "@/lib/format";
import { accountClass, currencyOf, primaryCurrency, signedBalance, summarize } from "@/lib/insights/accounts";
import { fmt } from "@/lib/money";
import { NEEDS_RELINK } from "@/lib/plaidsync-types";
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

// AppShell is the frame around every page: the header with search and
// sync, the sidebar with navigation and balances, and the page itself.
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
          <div className="mx-auto w-full max-w-[1320px] px-11 pt-8 pb-10">
            <ErrorBoundary>
              <Outlet />
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
              className="flex size-9 cursor-pointer items-center justify-center rounded-lg border-0 bg-transparent text-ink-2 hover:bg-line-strong/60 disabled:cursor-default"
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
  return <span className="ml-auto rounded-full bg-track px-2 py-0.5 text-[10.5px] font-semibold tracking-[0.04em] text-ink-3 uppercase">Sandbox</span>;
}

function SearchBox({ initial }: { initial: string }) {
  const navigate = useNavigate();
  const [q, setQ] = useState(initial);
  return (
    <form
      role="search"
      className="flex h-[34px] w-full max-w-[440px] items-center gap-2 rounded-[9px] border border-line-strong bg-field px-3 text-ink-3 focus-within:border-clay"
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

function Sidebar({ accounts, attention }: { accounts: AccountRow[]; attention: number }) {
  const currency = primaryCurrency(accounts);
  const mine = useMemo(() => accounts.filter((a) => currencyOf(a) === currency), [accounts, currency]);
  const summary = summarize(mine);
  const top = [...mine]
    .sort((a, b) => Math.abs(signedBalance(b.type, b.subtype, b.current_balance)) - Math.abs(signedBalance(a.type, a.subtype, a.current_balance)))
    .slice(0, 5);

  return (
    <nav aria-label="Main" className="flex w-[232px] shrink-0 flex-col gap-7 border-r border-line-strong bg-chrome px-3.5 pt-[22px] pb-[18px]">
      <div className="flex items-center gap-2.5 px-2.5">
        <Mark />
        <span className="font-serif text-[22px] leading-none tracking-[-0.01em] whitespace-nowrap italic">Money Insighter</span>
      </div>
      <div className="flex flex-col gap-0.5">
        {nav.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            end={to === "/"}
            className={({ isActive }) =>
              cn(
                "flex h-10 items-center gap-3 rounded-[9px] px-3 text-sm no-underline transition-colors",
                isActive ? "bg-sheet font-semibold text-ink shadow-[0_1px_0_var(--color-line-strong)]" : "font-medium text-ink-2 hover:bg-line hover:text-ink",
              )
            }
          >
            <Icon />
            <span>{label}</span>
            {to === "/accounts" && attention > 0 && (
              <span
                className="ml-auto flex h-5 min-w-5 items-center justify-center rounded-full bg-ochre-bg px-1.5 text-[11px] font-semibold text-ochre-ink"
                aria-label={`${attention} connection${attention === 1 ? "" : "s"} need${attention === 1 ? "s" : ""} attention`}
              >
                {attention}
              </span>
            )}
          </NavLink>
        ))}
      </div>
      {mine.length > 0 && (
        <div className="flex flex-col gap-2.5 border-t border-line-strong px-2.5 pt-[18px]">
          <div className="eyebrow">Net worth</div>
          <div className="num mb-1.5 font-serif text-[28px] leading-none">{fmt(summary.net, 2, currency)}</div>
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
        {[
          { to: "/profile", label: "Profile", icon: ProfileIcon },
          { to: "/settings", label: "Settings", icon: SettingsIcon },
        ].map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              cn(
                "flex h-10 items-center gap-3 rounded-[9px] px-3 text-sm no-underline transition-colors",
                isActive ? "bg-sheet font-semibold text-ink" : "font-medium text-ink-2 hover:bg-line hover:text-ink",
              )
            }
          >
            <Icon />
            <span>{label}</span>
            {to === "/profile" && <ModeBadge />}
          </NavLink>
        ))}
      </div>
    </nav>
  );
}
