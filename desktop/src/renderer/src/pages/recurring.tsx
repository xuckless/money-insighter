import { useState } from "react";
import { Link } from "react-router";

import { monthCells, StackedBar, WEEKDAY_INITIALS } from "@/components/charts/chart";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { Page, PageIntro } from "@/components/page-intro";
import { Chip, Empty, Panel, PanelHeader, PanelTitle } from "@/components/panel";
import { Pills } from "@/components/segmented";
import { Button } from "@/components/ui/button";
import { useSettings } from "@/hooks/use-app-state";
import { useDataVersion } from "@/hooks/use-data-version";
import { useLoad } from "@/hooks/use-load";
import { dayOfMonth, diffDays, fmtDay, fmtMonth, fmtRelativeDay, monthEnd, monthStart, todayISO, type ISODate } from "@/lib/dates";
import { currencyOf, primaryCurrency, shortAccount } from "@/lib/insights/accounts";
import {
  frequencyLabel,
  monthlyAmount,
  occurrences,
  streamAmount,
  streamGroup,
  streamName,
  varies,
  yearlyAmount,
  type PriceChange,
  type StreamGroup,
} from "@/lib/insights/recurring";
import { fmt } from "@/lib/money";
import { loadActiveStreams, resolvePriceChanges } from "@/lib/queries";
import { topper } from "@/lib/topper";
import type { StreamRow, SyncStatusRow } from "@/lib/topper-types";
import { cn } from "@/lib/utils";

const groups: { id: StreamGroup; title: string; color: string; tint: string; ink: string }[] = [
  { id: "bills", title: "Bills", color: "#A67A1F", tint: "#F1E5CC", ink: "#5C420D" },
  { id: "subscriptions", title: "Subscriptions", color: "#3F7F7A", tint: "#DCEAE8", ink: "#244B47" },
  { id: "income", title: "Income & transfers", color: "#3F6B4E", tint: "#DCE8DF", ink: "#2A4A35" },
];

const per: Record<StreamRow["frequency"], string> = {
  WEEKLY: "a week",
  BIWEEKLY: "every 2 weeks",
  SEMI_MONTHLY: "twice a month",
  MONTHLY: "a month",
  ANNUALLY: "a year",
  UNKNOWN: "each time",
};

const SUB_SHADES = ["#3F7F7A", "#5E948F", "#7DA9A4", "#9CBEBA", "#B5CFCB", "#CADDDA", "#DCE8E6"];

type Tab = "all" | StreamGroup;

export function RecurringPage() {
  const [settings] = useSettings();
  const { version } = useDataVersion();
  const enabled = settings?.recurringEnabled ?? false;

  const [data] = useLoad(async () => {
    if (!enabled) return null;
    const [accounts, streams, status] = await Promise.all([
      topper.accounts({ limit: 1000, select: ["account_id", "iso_currency_code", "unofficial_currency_code"] }),
      loadActiveStreams(),
      topper.syncStatus({ limit: 1000 }),
    ]);
    return { accounts: accounts.data, streams, status: status.data, changes: await resolvePriceChanges(streams) };
  }, [enabled, version]);

  if (settings === null || data.ok === "loading") return <Loading />;
  if (!enabled) return <RecurringOff />;
  if (!data.ok) return <LoadError what="recurring payments" message={data.error} />;
  if (!data.data) return <Loading />;
  return <RecurringView {...data.data} />;
}

function RecurringOff() {
  return (
    <Page>
      <PageIntro eyebrow="Recurring · Bills and subscriptions">
        See what leaves <em>on autopilot</em>, before it does.
      </PageIntro>
      <Panel className="max-w-2xl">
        <Empty
          title="Recurring is an add-on"
          action={
            <Button asChild>
              <Link to="/settings?focus=add-ons">Turn it on in Settings</Link>
            </Button>
          }
        >
          Plaid’s Recurring Transactions finds paycheques, bills and subscriptions in your history and predicts when each lands next. It
          powers this page, Coming up, Cash flow projections and price-change alerts. It works in Sandbox; in Production the add-on has
          to be enabled on your Plaid account and is billed by Plaid.
        </Empty>
      </Panel>
    </Page>
  );
}

function RecurringView({
  accounts,
  streams: all,
  status,
  changes,
}: {
  accounts: { iso_currency_code: string | null; unofficial_currency_code: string | null }[];
  streams: StreamRow[];
  status: SyncStatusRow[];
  changes: Map<string, PriceChange>;
}) {
  const [tab, setTab] = useState<Tab>("all");
  const today = todayISO();
  const currency = primaryCurrency(accounts);
  const money = (n: number, d = 0) => fmt(n, d, currency);
  const streams = all.filter((s) => currencyOf(s) === currency);

  const byGroup = new Map<StreamGroup, StreamRow[]>(groups.map((g) => [g.id, []]));
  for (const s of streams) byGroup.get(streamGroup(s))!.push(s);
  for (const list of byGroup.values()) {
    list.sort((a, b) => (a.predicted_next_date ?? "9999") < (b.predicted_next_date ?? "9999") ? -1 : 1);
  }
  const outMonthly = [...byGroup.get("bills")!, ...byGroup.get("subscriptions")!].reduce((a, s) => a + monthlyAmount(s), 0);
  const errors = status.filter((s) => s.status !== "removed" && s.recurring_error_code);
  const waiting = status.filter((s) => s.status !== "removed" && !s.recurring_checked_at);

  if (streams.length === 0) {
    return (
      <Page>
        <PageIntro eyebrow="Recurring · Bills and subscriptions">Nothing recurring found yet.</PageIntro>
        <Panel className="max-w-2xl">
          {errors.length > 0 ? (
            <Empty title="Plaid did not return recurring payments">
              {errors[0].institution_name ?? "A connection"}: {errors[0].recurring_error_message ?? errors[0].recurring_error_code}.{" "}
              {errors[0].recurring_error_code === "PRODUCT_NOT_ENABLED" || errors[0].recurring_error_code === "INVALID_PRODUCT"
                ? "Enable Recurring Transactions for your Plaid account, then sync again."
                : "It is retried after every sync."}
            </Empty>
          ) : (
            <Empty title={waiting.length ? "Looking for recurring payments" : "No recurring payments yet"}>
              {waiting.length
                ? "Plaid’s recurring streams are fetched after the next sync of each connection."
                : "Plaid needs a few months of history before a payment counts as recurring."}
            </Empty>
          )}
        </Panel>
      </Page>
    );
  }

  const tabs: { value: Tab; label: string; count: number }[] = [
    { value: "all", label: "All", count: streams.length },
    ...groups.map((g) => ({ value: g.id, label: g.title, count: byGroup.get(g.id)!.length })),
  ];

  // This month's calendar.
  const mStart = monthStart(today);
  const mEnd = monthEnd(today);
  const kinds = new Map<ISODate, Set<StreamGroup>>();
  let charges = 0;
  for (const s of streams) {
    for (const d of occurrences(s, mStart, mEnd)) {
      charges++;
      const set = kinds.get(d) ?? new Set<StreamGroup>();
      set.add(streamGroup(s));
      kinds.set(d, set);
    }
  }

  const subs = byGroup.get("subscriptions")!;
  const subsMonthly = subs.reduce((a, s) => a + monthlyAmount(s), 0);
  const subsSorted = [...subs].sort((a, b) => monthlyAmount(b) - monthlyAmount(a));
  const change = streams
    .filter((s) => changes.has(s.stream_id))
    .sort((a, b) => Math.abs(changes.get(b.stream_id)!.to - changes.get(b.stream_id)!.from) - Math.abs(changes.get(a.stream_id)!.to - changes.get(a.stream_id)!.from))[0];

  const columns = "grid grid-cols-[minmax(0,2fr)_1.2fr_0.7fr_1.2fr_0.8fr_0.8fr] gap-3.5";

  return (
    <Page>
      <PageIntro eyebrow="Recurring · Bills and subscriptions">
        <em>{money(outMonthly)}</em> a month leaves on autopilot. That’s {money(outMonthly * 12)} a year.
      </PageIntro>

      {errors.length > 0 && (
        <p className="m-0 rounded-[10px] bg-ochre-bg/60 px-3.5 py-2.5 text-[12.5px] text-ochre-ink">
          {errors.map((e) => e.institution_name ?? "A connection").join(", ")}: recurring payments could not be refreshed (
          {errors[0].recurring_error_code}). Showing what was found before.
        </p>
      )}

      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <Panel className="gap-3.5 px-6 py-5">
          <Pills label="Filter" value={tab} options={tabs} onChange={setTab} />
          <div className={cn(columns, "border-b border-line px-2 pt-1.5 pb-2")}>
            <span className="eyebrow">Name</span>
            <span className="eyebrow">Schedule</span>
            <span className="eyebrow">Next</span>
            <span className="eyebrow">Paid from</span>
            <span className="eyebrow text-right">Amount</span>
            <span className="eyebrow text-right">Per year</span>
          </div>
          <div className="-mt-2 flex flex-col">
            {groups
              .filter((g) => tab === "all" || tab === g.id)
              .filter((g) => byGroup.get(g.id)!.length > 0)
              .map((g) => {
                const list = byGroup.get(g.id)!;
                return (
                  <div key={g.id}>
                    <div className="flex items-center justify-between px-2 pt-3.5 pb-1.5">
                      <span className="flex items-center gap-2 text-[13px] font-bold">
                        <span className="size-[9px] rounded-full" style={{ background: g.color }} />
                        {g.title}
                      </span>
                      {g.id !== "income" && <span className="num text-[12.5px] text-ink-3">{money(list.reduce((a, s) => a + monthlyAmount(s), 0), 2)} a month</span>}
                    </div>
                    {list.map((s) => {
                      const inflow = s.direction === "inflow";
                      const ch = changes.get(s.stream_id);
                      const name = streamName(s);
                      const account = shortAccount(s.account_name, s.institution_name, s.account_mask);
                      return (
                        <div key={s.stream_id} className={cn(columns, "num min-h-11 items-center border-t border-hairline px-2 py-1 text-[13.5px]")}>
                          <span className="flex min-w-0 items-center gap-2.5">
                            <span
                              className="flex size-7 shrink-0 items-center justify-center rounded-[7px] text-xs font-bold"
                              style={{ background: g.tint, color: g.ink }}
                            >
                              {name.charAt(0).toUpperCase()}
                            </span>
                            <span className="truncate font-semibold" title={s.description}>
                              {name}
                            </span>
                            {ch && <Chip className="text-[11px]">Price {ch.to > ch.from ? "up" : "down"} {money(Math.abs(ch.to - ch.from), 2)}</Chip>}
                            {s.status === "EARLY_DETECTION" && <Chip className="bg-track text-ink-3">New</Chip>}
                          </span>
                          <span className="text-ink-2">
                            {frequencyLabel[s.frequency]}
                            {!ch && varies(s) && g.id !== "income" ? " · varies" : ""}
                          </span>
                          <span className="text-ink-2">{s.predicted_next_date ? fmtDay(s.predicted_next_date) : "—"}</span>
                          <span className="truncate text-ink-2">{inflow ? `Into ${account}` : account}</span>
                          <span className={cn("text-right font-semibold", inflow && "text-moss")}>
                            {inflow ? "+" : ""}
                            {money(streamAmount(s), 2)}
                          </span>
                          <span className="text-right text-ink-2">{money(yearlyAmount(s))}</span>
                        </div>
                      );
                    })}
                  </div>
                );
              })}
          </div>
        </Panel>

        <div className="flex flex-col gap-4">
          <Panel className="gap-3.5">
            <PanelHeader title={fmtMonth(today)} description={`${charges} charge${charges === 1 ? "" : "s"} and deposits this month`} />
            <div className="grid grid-cols-7 gap-1">
              {WEEKDAY_INITIALS.map((w, i) => (
                <span key={i} className="eyebrow text-center">
                  {w}
                </span>
              ))}
              {monthCells(today).map((c, i) => {
                if (!c.day) return <div key={i} className="h-11" />;
                const isToday = c.day === today;
                const past = c.day < today;
                return (
                  <div
                    key={c.day}
                    className={cn(
                      "flex h-11 flex-col items-center justify-center gap-1 rounded-lg border",
                      isToday ? "border-[1.5px] border-clay bg-clay-soft" : past ? "border-transparent" : "border-transparent bg-row-hover",
                    )}
                  >
                    <span className={cn("num text-xs", isToday ? "font-bold" : "font-medium", past ? "text-ink-3" : "text-ink")}>{dayOfMonth(c.day)}</span>
                    <span className="flex h-1.5 gap-[3px]">
                      {[...(kinds.get(c.day) ?? [])].map((k) => (
                        <span key={k} className="size-1.5 rounded-full" style={{ background: groups.find((g) => g.id === k)!.color }} />
                      ))}
                    </span>
                  </div>
                );
              })}
            </div>
            <div className="flex flex-wrap gap-3.5 text-xs text-ink-3">
              {groups.map((g) => (
                <span key={g.id} className="flex items-center gap-1.5">
                  <span className="size-2 rounded-full" style={{ background: g.color }} />
                  {g.title}
                </span>
              ))}
            </div>
          </Panel>

          {change && (
            <Panel className="gap-2.5">
              <div className="eyebrow text-clay-ink">Price change</div>
              <div className="font-serif text-2xl leading-tight">{streamName(change)}</div>
              <div className="num flex items-baseline gap-2.5 text-[15px]">
                <span className="text-ink-3 line-through">{money(changes.get(change.stream_id)!.from, 2)}</span>
                <span className="font-semibold">
                  {money(changes.get(change.stream_id)!.to, 2)} {per[change.frequency]}
                </span>
              </div>
              <p className="m-0 text-[13px] leading-[1.45] text-ink-2">
                {change.predicted_next_date && diffDays(today, change.predicted_next_date) >= 0
                  ? `Next charge ${fmtRelativeDay(change.predicted_next_date, today).toLowerCase() === "today" ? "today" : `on ${fmtRelativeDay(change.predicted_next_date, today)}`}`
                  : "Charged already"}{" "}
                to your {shortAccount(change.account_name, change.institution_name, change.account_mask)}.{" "}
                {money(
                  Math.abs(changes.get(change.stream_id)!.to - changes.get(change.stream_id)!.from) *
                    (yearlyAmount(change) / Math.max(streamAmount(change), 0.01)),
                  2,
                )}{" "}
                {changes.get(change.stream_id)!.to > changes.get(change.stream_id)!.from ? "more" : "less"} a year.
              </p>
            </Panel>
          )}

          {subs.length > 0 && (
            <Panel className="gap-3">
              <div className="flex items-baseline justify-between">
                <PanelTitle>Subscriptions</PanelTitle>
                <span className="num text-[13px] font-semibold">{money(subsMonthly, 2)}/mo</span>
              </div>
              <StackedBar
                height={12}
                segments={subsSorted.map((s, i) => ({ key: s.stream_id, value: monthlyAmount(s), color: SUB_SHADES[Math.min(i, SUB_SHADES.length - 1)], title: streamName(s) }))}
              />
              <p className="m-0 text-[12.5px] leading-[1.45] text-ink-3">
                {subsMonthly > 0
                  ? `${streamName(subsSorted[0])} is ${Math.round((monthlyAmount(subsSorted[0]) / subsMonthly) * 100)}% of your subscription spend.`
                  : ""}
              </p>
            </Panel>
          )}
          <p className="m-0 px-1 text-xs text-ink-4">
            Found by Plaid’s Recurring Transactions from your history. Next dates are Plaid’s predictions.
          </p>
        </div>
      </div>
    </Page>
  );
}
