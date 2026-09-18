import { useState } from "react";
import { useSearchParams } from "react-router";
import { toast } from "sonner";

import { Area, ChartFrame, Dot, Line, linePath, Meter, scale, StackedBar, type Point } from "@/components/charts/chart";
import { HostedLinkButton, useHostedLink } from "@/components/hosted-link-button";
import { LockIcon, MoreIcon, PlusIcon } from "@/components/icons";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { Grid, Page, PageHeader } from "@/components/page-header";
import { Empty, ListHeader, Panel, PanelHeader, PanelTitle, Stat, Swatch } from "@/components/panel";
import { Button } from "@/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { useSettings } from "@/hooks/use-app-state";
import { useDataVersion } from "@/hooks/use-data-version";
import { useJobPoller } from "@/hooks/use-job-poller";
import { useLoad } from "@/hooks/use-load";
import { addDays, addMonths, diffDays, fmtDay, fmtMonthShort, monthStart, todayISO, yearStart, type ISODate } from "@/lib/dates";
import { formatRelative, humanize } from "@/lib/format";
import {
  accountClass,
  accountLabel,
  cardUtilization,
  currencyOf,
  initials,
  netWorthSeries,
  primaryCurrency,
  summarize,
  type AccountClass,
} from "@/lib/insights/accounts";
import { addSandboxItem, removeItem, syncItem } from "@/lib/actions";
import { fmt, fmtAxis, fmtPct, niceCeil, num } from "@/lib/money";
import { plaidsync } from "@/lib/plaidsync";
import { NEEDS_RELINK, type Item } from "@/lib/plaidsync-types";
import { topper } from "@/lib/topper";
import type { AccountRow, SyncStatusRow } from "@/lib/topper-types";
import { cn } from "@/lib/utils";

const groupOrder: { title: string; classes: AccountClass[] }[] = [
  { title: "Cash", classes: ["chequing", "savings"] },
  { title: "Credit cards", classes: ["credit"] },
  { title: "Loans", classes: ["loan"] },
  { title: "Investments", classes: ["investment"] },
  { title: "Other", classes: ["other"] },
];

const composition: { cls: AccountClass; label: string; color: string }[] = [
  { cls: "investment", label: "Investments", color: "#3F6B4E" },
  { cls: "savings", label: "Savings", color: "#4F6D8A" },
  { cls: "chequing", label: "Chequing", color: "#A67A1F" },
  { cls: "other", label: "Other", color: "#8C8174" },
];

export function AccountsPage() {
  const [params, setParams] = useSearchParams();
  const showClosed = params.get("closed") === "1";
  const { version, bump } = useDataVersion();
  const [settings] = useSettings();
  const today = todayISO();

  const [data] = useLoad(async () => {
    const [accounts, items, status, balances] = await Promise.all([
      topper.accounts({ limit: 1000, toggles: { include_missing: 1 } }),
      plaidsync.listItems(),
      topper.syncStatus({ limit: 1000 }),
      topper.balancesDaily({ filters: { day: [`gte.${addMonths(monthStart(today), -12)}`] } }),
    ]);
    return { accounts: accounts.data, items: items.items, status: status.data, balances };
  }, [version]);

  if (data.ok === "loading") return <Loading />;
  if (!data.ok) return <LoadError what="accounts" message={data.error} />;

  const { accounts: all, items, status, balances } = data.data;
  const currency = primaryCurrency(all.filter((a) => !a.missing_since));
  const money = (n: number, d = 0) => fmt(n, d, currency);
  const open = all.filter((a) => !a.missing_since);
  const mine = open.filter((a) => currencyOf(a) === currency);
  const summary = summarize(mine);
  const util = cardUtilization(mine);
  const liveItems = items.filter((i) => i.status !== "removed");
  const statusById = new Map(status.map((s) => [s.item_id, s]));
  const itemById = new Map(items.map((i) => [i.item_id, i]));
  const sandbox = settings?.plaidEnv === "sandbox";

  const from = addMonths(monthStart(today), -11);
  const series = netWorthSeries(balances, currency, from, today);
  // Change since January when history reaches back that far, otherwise
  // since the first recorded day.
  const first = series[0];
  const janFirst = yearStart(today);
  const fromJanuary = first !== undefined && first.day <= janFirst;
  const base = fromJanuary ? (series.find((p) => p.day >= janFirst) ?? first) : first;
  const sinceLabel = fromJanuary ? "January" : base ? fmtDay(base.day) : "";
  const change = base && series.length > 1 ? summary.net - base.value : null;

  const shown = showClosed ? all : open;
  const withoutAccounts = liveItems.filter((i) => !all.some((a) => a.item_id === i.item_id));

  return (
    <Page>
      <PageHeader
        title="Accounts"
        subtitle={
          mine.length === 0
            ? "Connect a bank to see everything in one place."
            : `${liveItems.length} connection${liveItems.length === 1 ? "" : "s"} · ${mine.length} account${mine.length === 1 ? "" : "s"}${
                change !== null && Math.abs(change) >= 1 ? ` · net worth ${change >= 0 ? "up" : "down"} ${money(Math.abs(change))} since ${sinceLabel}` : ""
              }`
        }
        actions={
          <>
            {sandbox && <SandboxAdd onAdded={bump} />}
            <HostedLinkButton size="sm" onLinked={bump}>
              <PlusIcon size={15} /> Connect an account
            </HostedLinkButton>
          </>
        }
      />

      {mine.length > 0 && (
        <Grid>
          <Stat className="col-span-6 xl:col-span-3" label="Net worth" value={money(summary.net)} tone={change !== null && change < 0 ? "bad" : undefined}>
            {change !== null && base ? `${change >= 0 ? "+" : "−"}${base.value !== 0 ? fmtPct(Math.abs(change / base.value)) : money(Math.abs(change))} since ${sinceLabel}` : "History starts today"}
          </Stat>
          <Stat className="col-span-6 xl:col-span-3" label="Assets" value={money(summary.assets)}>
            Cash {money(summary.byClass.chequing + summary.byClass.savings)} · Investments {money(summary.byClass.investment)}
          </Stat>
          <Stat className="col-span-6 xl:col-span-3" label="Owed on cards" value={money(util.owed)}>
            {util.ratio !== null ? `${fmtPct(util.ratio)} of a ${money(util.limit)} combined limit` : summary.byClass.credit > 0 ? "No credit limits reported" : "No cards connected"}
          </Stat>
          <Stat className="col-span-6 xl:col-span-3" label="Loans" value={money(summary.byClass.loan)}>
            {summary.byClass.loan > 0 ? "Mortgages, lines of credit and loans" : "No loans connected"}
          </Stat>
        </Grid>
      )}

      {mine.length > 0 && (
        <Grid>
          <Panel className="col-span-12 xl:col-span-8">
            <PanelHeader title="Net worth" description="Everything you own, minus what you owe on cards and loans">
              <div className="text-right">
                <div className="figure text-[24px]">{money(summary.net, 2)}</div>
                {change !== null && base && (
                  <div className={cn("mt-1 text-[12px] font-semibold", change >= 0 ? "text-moss" : "text-clay")}>
                    {change >= 0 ? "+" : "−"}
                    {base.value !== 0 ? fmtPct(Math.abs(change / base.value)) : money(Math.abs(change))} since {sinceLabel}
                  </div>
                )}
              </div>
            </PanelHeader>
            <NetWorthChart series={series} today={today} money={money} />
          </Panel>

          <Panel className="col-span-12 xl:col-span-4">
            <PanelTitle>What it’s made of</PanelTitle>
            <div className="flex flex-col gap-2.5">
              <div className="flex items-baseline justify-between">
                <span className="eyebrow">Assets</span>
                <span className="num text-[15px] font-semibold">{money(summary.assets, 2)}</span>
              </div>
              <StackedBar segments={composition.map((c) => ({ key: c.cls, value: summary.byClass[c.cls], color: c.color, title: c.label }))} />
              <div className="num flex flex-col gap-2 text-[13px]">
                {composition
                  .filter((c) => summary.byClass[c.cls] > 0)
                  .map((c) => (
                    <div key={c.cls} className="flex items-center gap-2">
                      <Swatch color={c.color} className="size-[9px] rounded-[2px]" />
                      <span className="flex-1">{c.label}</span>
                      <span className="w-9 text-right text-ink-3">{summary.assets > 0 ? fmtPct(summary.byClass[c.cls] / summary.assets) : "—"}</span>
                      <span className="w-[84px] text-right font-semibold">{money(summary.byClass[c.cls])}</span>
                    </div>
                  ))}
              </div>
            </div>
            {(util.owed > 0 || summary.byClass.credit > 0) && (
              <div className="flex flex-col gap-2.5 border-t border-hairline pt-3.5">
                <div className="flex items-baseline justify-between">
                  <span className="eyebrow">Owed on cards</span>
                  <span className="num text-[15px] font-semibold">{money(-util.owed, 2)}</span>
                </div>
                {util.ratio !== null && <Meter ratio={util.ratio} height={10} className="rounded-[2px]" />}
                <div className="num text-[12.5px] text-ink-3">
                  {util.ratio !== null
                    ? `${fmtPct(util.ratio)} of your ${money(util.limit)} combined limit. ${util.ratio < 0.3 ? "Under 30% keeps credit scores happy." : "Getting under 30% helps your credit score."}`
                    : "Your card issuers did not report credit limits."}
                </div>
              </div>
            )}
            {summary.byClass.loan > 0 && (
              <div className="flex items-baseline justify-between border-t border-hairline pt-3.5">
                <span className="eyebrow">Loans</span>
                <span className="num text-[15px] font-semibold">{money(-summary.byClass.loan, 2)}</span>
              </div>
            )}
          </Panel>
        </Grid>
      )}

      <Panel className="gap-1.5">
        <ListHeader cols="grid-cols-[minmax(0,2.2fr)_1.2fr_1.1fr_1.7fr_150px]">
          <span className="eyebrow">Account</span>
          <span className="eyebrow">Type</span>
          <span className="eyebrow text-right">Balance</span>
          <span className="eyebrow">Status</span>
          <span />
        </ListHeader>
        {shown.length === 0 && withoutAccounts.length === 0 && (
          <Empty title="No accounts yet">
            Use “Connect an account” to open Plaid Link in your browser.{sandbox && " In Sandbox, sign in with user_good / pass_good."}
          </Empty>
        )}
        {withoutAccounts.length > 0 && (
          <Group title="Connecting" total="">
            {withoutAccounts.map((i) => (
              <div key={i.item_id} className="grid min-h-[54px] grid-cols-[minmax(0,2.2fr)_1.2fr_1.1fr_1.7fr_150px] items-center gap-4 border-t border-hairline px-2 py-1.5 text-[13.5px]">
                <AccountName ini={initials(i.institution_name)} name={i.institution_name ?? "New connection"} sub="Accounts appear after the first sync" />
                <span />
                <span />
                <ItemStatus item={i} status={statusById.get(i.item_id)} />
                <span className="flex justify-end">
                  <ConnectionMenu item={i} onChanged={bump} />
                </span>
              </div>
            ))}
          </Group>
        )}
        {groupOrder.map((g) => {
          const rows = shown.filter((a) => g.classes.includes(accountClass(a.type, a.subtype)));
          if (rows.length === 0) return null;
          const total = rows.filter((a) => !a.missing_since && currencyOf(a) === currency).reduce((acc, a) => acc + num(a.current_balance), 0);
          const liability = g.classes.includes("credit") || g.classes.includes("loan");
          return (
            <Group key={g.title} title={g.title} total={money(liability ? -total : total, 2)}>
              {rows.map((a) => (
                <AccountLine key={a.account_id} account={a} item={itemById.get(a.item_id)} status={statusById.get(a.item_id)} onChanged={bump} />
              ))}
            </Group>
          );
        })}
        <div className="mt-2 flex items-center gap-2.5 rounded-[4px] border border-line bg-paper px-3.5 py-2.5 text-[12.5px] text-ink-2">
          <LockIcon size={16} />
          <span className="flex-1">Connected through Plaid with read-only access. Money Insighter sees balances and transactions and can never move money.</span>
          {all.some((a) => a.missing_since) && (
            <button type="button" onClick={() => setParams(showClosed ? {} : { closed: "1" })} className="shrink-0 cursor-pointer border-0 bg-transparent p-0 text-[12.5px] font-semibold text-clay-ink hover:text-clay">
              {showClosed ? "Hide closed accounts" : "Show closed accounts"}
            </button>
          )}
        </div>
      </Panel>
    </Page>
  );
}

function Group({ title, total, children }: { title: string; total: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="flex items-center justify-between px-2 pt-3 pb-1">
        <span className="text-[13px] font-bold">{title}</span>
        <span className="num text-[12.5px] text-ink-3">{total}</span>
      </div>
      {children}
    </div>
  );
}

function AccountName({ ini, name, sub }: { ini: string; name: string; sub: string }) {
  return (
    <span className="flex min-w-0 items-center gap-3">
      <span className="flex size-8 shrink-0 items-center justify-center rounded-[4px] bg-track text-[11px] font-bold tracking-[0.02em] text-ink-2">{ini}</span>
      <span className="flex min-w-0 flex-col gap-px">
        <span className="truncate font-semibold">{name}</span>
        <span className="truncate text-xs text-ink-3">{sub}</span>
      </span>
    </span>
  );
}

function AccountLine({ account: a, item, status, onChanged }: { account: AccountRow; item?: Item; status?: SyncStatusRow; onChanged: () => void }) {
  const cls = accountClass(a.type, a.subtype);
  const bal = num(a.current_balance);
  const limit = num(a.credit_limit);
  const liability = cls === "credit" || cls === "loan";
  const currency = currencyOf(a);
  return (
    <div className={cn("num grid min-h-[54px] grid-cols-[minmax(0,2.2fr)_1.2fr_1.1fr_1.7fr_150px] items-center gap-4 border-t border-hairline px-2 py-1.5 text-[13.5px]", a.missing_since && "opacity-60")}>
      <AccountName ini={initials(a.institution_name ?? a.name)} name={a.name} sub={a.institution_name ?? "—"} />
      <span className="text-ink-2">{a.missing_since ? "Closed" : accountLabel(a.type, a.subtype, a.mask)}</span>
      <span className="flex flex-col items-end gap-[5px]">
        <span className="font-semibold">{fmt(liability ? -bal : bal, 2, currency)}</span>
        {cls === "credit" && limit > 0 && (
          <span className="flex items-center gap-1.5 text-[11.5px] text-ink-3">
            <Meter ratio={bal / limit} height={4} className="w-11" />
            {fmtPct(bal / limit)} of {fmt(limit, 0, currency)}
          </span>
        )}
      </span>
      {item ? <ItemStatus item={item} status={status} /> : <span />}
      <span className="flex justify-end">{item && <ConnectionMenu item={item} onChanged={onChanged} />}</span>
    </div>
  );
}

function ItemStatus({ item, status }: { item: Item; status?: SyncStatusRow }) {
  const relink = NEEDS_RELINK.has(item.status);
  const running = status?.latest_job_state === "queued" || status?.latest_job_state === "running";
  const text = relink
    ? `${item.status === "pending_expiration" ? "Access expires soon" : item.status === "permission_revoked" ? "Access revoked" : "Sign-in expired"} · last synced ${item.last_successful_sync_at ? fmtDay(item.last_successful_sync_at.slice(0, 10)) : "never"}`
    : item.status === "error"
      ? `Sync failed · ${item.last_error?.code ? humanize(item.last_error.code) : "try again"}`
      : running
        ? "Syncing…"
        : item.last_successful_sync_at
          ? `Synced ${formatRelative(item.last_successful_sync_at)}`
          : "Waiting for first sync";
  return (
    <span className={cn("flex items-center gap-2 text-[12.5px]", relink ? "text-ochre-text" : item.status === "error" ? "text-clay-ink" : "text-ink-2")} title={item.last_error?.message ?? undefined}>
      <span className={cn("size-[7px] shrink-0 rounded-full", relink ? "bg-ochre" : item.status === "error" ? "bg-clay" : running ? "animate-pulse bg-stone" : "bg-moss")} />
      <span className="truncate">{text}</span>
    </span>
  );
}

// ConnectionMenu is what an account row can do to its connection:
// Reconnect when Plaid needs the user to sign in again, otherwise a menu
// with Sync now, Reconnect and Remove.
function ConnectionMenu({ item, onChanged }: { item: Item; onChanged: () => void }) {
  const poll = useJobPoller();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [removing, setRemoving] = useState(false);
  const name = item.institution_name ?? "This connection";
  const wantsAccountSelection = item.last_error?.code === "NEW_ACCOUNTS_AVAILABLE";
  const link = useHostedLink({ itemId: item.item_id, accountSelection: wantsAccountSelection, onLinked: onChanged });

  const sync = async () => {
    const res = await syncItem(item.item_id);
    if (!res.ok) {
      toast.error(`Sync of ${name} failed to start`, { description: res.error });
      return;
    }
    onChanged();
    try {
      const job = await poll(res.data);
      if (job.state === "succeeded") toast.success(`${name} synced`);
      else if (job.state === "skipped")
        toast.info(`${name} is already up to date`, { description: job.error_code === "debounced" ? "It synced in the last few minutes." : humanize(job.error_code) });
      else toast.error(`Sync of ${name} failed`, { description: job.error_message ?? humanize(job.error_code) });
    } catch (err) {
      toast.warning(`Sync of ${name} still running`, { description: (err as Error).message });
    }
    onChanged();
  };

  const remove = async () => {
    setRemoving(true);
    try {
      const res = await removeItem(item.item_id);
      setConfirmOpen(false);
      if (!res.ok) {
        toast.error(`Could not remove ${name}`, { description: res.error });
        return;
      }
      toast.success(`${name} removed`);
      onChanged();
    } finally {
      setRemoving(false);
    }
  };

  if (item.status === "removed") return <span className="text-xs text-ink-3">Removed</span>;

  return (
    <>
      {NEEDS_RELINK.has(item.status) ? (
        <Button variant="outline" size="sm" className="border-clay font-semibold text-clay-ink" onClick={link.start} disabled={link.busy}>
          Reconnect
        </Button>
      ) : (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button type="button" aria-label="Account options" className="flex size-8 cursor-pointer items-center justify-center rounded-[4px] border-0 bg-transparent text-ink-2 hover:bg-row-hover">
              <MoreIcon size={18} />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-52">
            <DropdownMenuItem onSelect={() => void sync()}>Sync now</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => void link.start()}>{wantsAccountSelection ? "Choose accounts" : "Update sign-in"}</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={() => setConfirmOpen(true)}>
              Remove connection
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
      {link.dialog}
      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove {name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Plaid access is revoked and syncing stops for every account at {name}. Transactions already synced stay on this computer.
              In Production, removing a connection does not free a Trial plan slot.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={removing}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={removing}
              onClick={(e) => {
                e.preventDefault();
                void remove();
              }}
            >
              {removing ? "Removing…" : "Remove"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function SandboxAdd({ onAdded }: { onAdded: () => void }) {
  const [pending, setPending] = useState(false);
  return (
    <Button
      variant="outline"
      size="sm"
      disabled={pending}
      onClick={async () => {
        setPending(true);
        try {
          const res = await addSandboxItem();
          if (!res.ok) {
            toast.error("Could not add a Sandbox item", { description: res.error });
            return;
          }
          toast.success(`${res.data.item.institution_name ?? "Sandbox item"} added`, { description: "The first sync is queued." });
          onAdded();
        } finally {
          setPending(false);
        }
      }}
    >
      {pending ? "Adding…" : "Add Sandbox item"}
    </Button>
  );
}

function NetWorthChart({ series, today, money }: { series: { day: ISODate; value: number }[]; today: ISODate; money: (n: number) => string }) {
  if (series.length < 2) {
    return (
      <Empty title="History starts today">
        Money Insighter records balances each day it syncs. The chart fills in from {fmtDay(series[0]?.day ?? today)}.
      </Empty>
    );
  }
  const values = series.map((p) => p.value);
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const pad = Math.max((hi - lo) * 0.15, Math.abs(hi) * 0.02, 100);
  const step = niceCeil((hi - lo + 2 * pad) / 3);
  const min = Math.floor((lo - pad) / step) * step;
  const max = Math.ceil((hi + pad) / step) * step;
  const s = scale(170, min, max);
  const start = series[0].day;
  const span = Math.max(1, diffDays(start, today));
  const pts: Point[] = series.map((p) => [s.x(diffDays(start, p.day) / span), s.y(p.value)]);
  const yTicks = [];
  for (let v = min; v <= max + step / 2; v += step) yTicks.push({ value: v, label: fmtAxis(v) });
  const xTicks = [];
  for (let d = start; d <= today; d = addDays(d, 1)) {
    if (d.endsWith("-01") || d === start) xTicks.push({ t: diffDays(start, d) / span, label: d.endsWith("-01") ? fmtMonthShort(d) : fmtDay(d), align: d === start ? ("start" as const) : undefined });
  }
  return (
    <ChartFrame
      scale={s}
      label={`Net worth from ${money(series[0].value)} on ${fmtDay(start)} to ${money(series.at(-1)!.value)} today`}
      yTicks={yTicks}
      xTicks={xTicks.filter((x, i) => i === 0 || x.t > 0.06)}
      svg={
        <>
          <Area d={`${linePath(pts)} L1000 ${s.height} L0 ${s.height} Z`} color="var(--color-moss-soft)" />
          <Line d={linePath(pts)} color="var(--color-moss)" />
        </>
      }
      overlay={<Dot left="100%" top={s.y(series.at(-1)!.value)} color="var(--color-moss)" />}
    />
  );
}
