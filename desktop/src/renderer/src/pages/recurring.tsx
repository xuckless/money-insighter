import { useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { CategoryIcon } from "@/components/category-icon";
import { monthCells, StackedBar, WEEKDAY_INITIALS } from "@/components/charts/chart";
import { MoreIcon, PlusIcon } from "@/components/icons";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { Grid, Page, PageHeader } from "@/components/page-header";
import { Chip, Empty, ListHeader, Notice, Panel, PanelHeader, PanelTitle, Stat } from "@/components/panel";
import { RecurringEntryDialog } from "@/components/recurring-entry-dialog";
import { Segmented, Tabs } from "@/components/segmented";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { useSettings } from "@/hooks/use-app-state";
import { useDataVersion } from "@/hooks/use-data-version";
import { useLoad } from "@/hooks/use-load";
import { addDays, dayOfMonth, diffDays, fmtDay, fmtMonth, fmtRelativeDay, monthEnd, monthStart, todayISO, type ISODate } from "@/lib/dates";
import { currencyOf, primaryCurrency, shortAccount } from "@/lib/insights/accounts";
import {
  frequencyLabel,
  monthlyAmount,
  occurrences,
  sourceLabel,
  streamAmount,
  streamGroup,
  streamName,
  varies,
  yearlyAmount,
  type PriceChange,
  type StreamGroup,
} from "@/lib/insights/recurring";
import { fmt } from "@/lib/money";
import { hideStream, loadStreams, resolvePriceChanges, unhideStream } from "@/lib/queries";
import { topper } from "@/lib/topper";
import type { RecurringEntryRow, StreamRow, StreamSource, SyncStatusRow } from "@/lib/topper-types";
import { cn } from "@/lib/utils";

import { category } from "@shared/categories";

const groups: { id: StreamGroup; title: string; color: string }[] = [
  { id: "bills", title: "Bills", color: "#A67A1F" },
  { id: "subscriptions", title: "Subscriptions", color: "#3F7F7A" },
  { id: "income", title: "Income & transfers", color: "#3F6B4E" },
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
type Source = "all" | StreamSource;

export function RecurringPage() {
  const [settings] = useSettings();
  const { version } = useDataVersion();
  const plaidOn = settings?.recurringEnabled ?? false;
  const today = todayISO();

  const [data] = useLoad(async () => {
    if (settings === null) return null;
    const [accounts, streams, status, entries] = await Promise.all([
      topper.accounts({ limit: 1000, select: ["account_id", "name", "mask", "institution_name", "iso_currency_code", "unofficial_currency_code"] }),
      loadStreams(today, plaidOn),
      topper.syncStatus({ limit: 1000 }),
      topper.recurringEntries(),
    ]);
    return { accounts: accounts.data, streams, status: status.data, entries, changes: await resolvePriceChanges(streams.active) };
  }, [plaidOn, version, settings === null]);

  if (settings === null || data.ok === "loading" || (data.ok === true && data.data === null)) return <Loading />;
  if (!data.ok) return <LoadError what="recurring payments" message={data.error} />;
  return <RecurringView {...data.data!} plaidOn={plaidOn} today={today} />;
}

function RecurringView({
  accounts,
  streams: all,
  status,
  entries,
  changes,
  plaidOn,
  today,
}: {
  accounts: { account_id: string; name: string; mask: string | null; institution_name: string | null; iso_currency_code: string | null; unofficial_currency_code: string | null }[];
  streams: { active: StreamRow[]; hidden: StreamRow[] };
  status: SyncStatusRow[];
  entries: RecurringEntryRow[];
  changes: Map<string, PriceChange>;
  plaidOn: boolean;
  today: ISODate;
}) {
  const { bump } = useDataVersion();
  const [tab, setTab] = useState<Tab>("all");
  const [source, setSource] = useState<Source>("all");
  const [showHidden, setShowHidden] = useState(false);
  const [dialog, setDialog] = useState<{ open: boolean; editing: RecurringEntryRow | null; prefill?: Partial<Omit<RecurringEntryRow, "id">> }>({ open: false, editing: null });
  const currency = primaryCurrency(accounts);
  const money = (n: number, d = 0) => fmt(n, d, currency);
  const streams = all.active.filter((s) => currencyOf(s) === currency || !s.account_id);
  const accountOptions = accounts.map((a) => ({ id: a.account_id, label: `${a.institution_name ? `${a.institution_name} · ` : ""}${a.name}${a.mask ? ` ••${a.mask}` : ""}` }));
  const entryById = new Map(entries.map((e) => [e.id, e]));

  const byGroup = new Map<StreamGroup, StreamRow[]>(groups.map((g) => [g.id, []]));
  for (const s of streams) byGroup.get(streamGroup(s))!.push(s);
  for (const list of byGroup.values()) {
    list.sort((a, b) => ((a.predicted_next_date ?? "9999") < (b.predicted_next_date ?? "9999") ? -1 : 1));
  }
  const outStreams = [...byGroup.get("bills")!, ...byGroup.get("subscriptions")!];
  const outMonthly = outStreams.reduce((a, s) => a + monthlyAmount(s), 0);
  const errors = status.filter((s) => s.status !== "removed" && s.recurring_error_code);
  const counts: Record<StreamSource, number> = { plaid: 0, detected: 0, manual: 0 };
  for (const s of streams) counts[s.source]++;

  const openAdd = (prefill?: Partial<Omit<RecurringEntryRow, "id">>) => setDialog({ open: true, editing: null, prefill });
  const openEdit = (s: StreamRow) => {
    const e = entryById.get(s.stream_id.replace(/^manual:/, ""));
    if (e) setDialog({ open: true, editing: e });
  };
  const hide = async (s: StreamRow) => {
    try {
      await hideStream(s.stream_id);
      toast.success(`${streamName(s)} marked as not recurring`);
      bump();
    } catch (err) {
      toast.error("Could not hide it", { description: (err as Error).message });
    }
  };
  const unhide = async (s: StreamRow) => {
    try {
      await unhideStream(s.stream_id);
      bump();
    } catch (err) {
      toast.error("Could not restore it", { description: (err as Error).message });
    }
  };
  const fromStream = (s: StreamRow): Partial<Omit<RecurringEntryRow, "id">> => ({
    name: streamName(s),
    amount: streamAmount(s).toFixed(2),
    direction: s.direction,
    frequency: s.frequency === "UNKNOWN" ? "MONTHLY" : s.frequency,
    next_date: s.predicted_next_date ?? today,
    category: s.category,
    account_id: s.account_id || null,
    merchant_key: s.merchant_key || null,
  });

  const addButton = (
    <Button size="sm" onClick={() => openAdd()}>
      <PlusIcon size={15} /> Add recurring
    </Button>
  );
  const entryDialog = (
    <RecurringEntryDialog open={dialog.open} onOpenChange={(o) => setDialog((d) => ({ ...d, open: o }))} editing={dialog.editing} prefill={dialog.prefill} accounts={accountOptions} />
  );

  if (streams.length === 0 && all.hidden.length === 0) {
    return (
      <Page>
        <PageHeader title="Recurring" subtitle="Bills, subscriptions and paycheques, and when each lands next." actions={addButton} />
        <Panel className="max-w-2xl">
          <Empty title="Nothing recurring found yet" action={addButton}>
            Money Insighter finds streams in your transaction history once a merchant has charged you a few times at a steady interval. Anything else, such as rent paid by
            e-transfer, can be added by hand.
            {!plaidOn && " Plaid’s Recurring Transactions add-on, in Settings, adds Plaid’s own detection."}
          </Empty>
        </Panel>
        {entryDialog}
      </Page>
    );
  }

  const tabs: { value: Tab; label: string; count: number }[] = [
    { value: "all", label: "All", count: streams.length },
    ...groups.map((g) => ({ value: g.id, label: g.title, count: byGroup.get(g.id)!.length })),
  ];
  const sources: { value: Source; label: string }[] = [
    { value: "all", label: "All sources" },
    { value: "detected", label: `${sourceLabel.detected} ${counts.detected}` },
    { value: "manual", label: `${sourceLabel.manual} ${counts.manual}` },
    ...(plaidOn ? [{ value: "plaid" as const, label: `${sourceLabel.plaid} ${counts.plaid}` }] : []),
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
  const week = outStreams.flatMap((s) => occurrences(s, addDays(today, 1), addDays(today, 7)).map(() => streamAmount(s)));

  const subs = byGroup.get("subscriptions")!;
  const subsMonthly = subs.reduce((a, s) => a + monthlyAmount(s), 0);
  const subsSorted = [...subs].sort((a, b) => monthlyAmount(b) - monthlyAmount(a));
  const change = streams
    .filter((s) => changes.has(s.stream_id))
    .sort((a, b) => Math.abs(changes.get(b.stream_id)!.to - changes.get(b.stream_id)!.from) - Math.abs(changes.get(a.stream_id)!.to - changes.get(a.stream_id)!.from))[0];

  const cols = "grid-cols-[minmax(0,2fr)_1.1fr_0.7fr_1.2fr_0.8fr_0.8fr_28px]";
  const visible = (list: StreamRow[]) => list.filter((s) => source === "all" || s.source === source);

  return (
    <Page>
      <PageHeader
        title="Recurring"
        subtitle={`${money(outMonthly)} a month leaves on autopilot · ${money(outMonthly * 12)} a year`}
        actions={
          <>
            <Segmented label="Source" value={source} options={sources} onChange={setSource} />
            {addButton}
          </>
        }
      />

      {errors.length > 0 && plaidOn && (
        <Notice tone="warn">
          {errors.map((e) => e.institution_name ?? "A connection").join(", ")}: Plaid’s recurring payments could not be refreshed ({errors[0].recurring_error_code}). Streams
          detected from your history are still shown.
        </Notice>
      )}

      <Grid>
        <Stat className="col-span-6 xl:col-span-3" label="Every month" value={money(outMonthly)}>
          {outStreams.length} bill{outStreams.length === 1 ? "" : "s"} and subscription{outStreams.length === 1 ? "" : "s"}
        </Stat>
        <Stat className="col-span-6 xl:col-span-3" label="Every year" value={money(outMonthly * 12)}>
          What the same list costs over twelve months
        </Stat>
        <Stat className="col-span-6 xl:col-span-3" label="Subscriptions" value={money(subsMonthly)}>
          {subs.length ? `${subs.length} subscription${subs.length === 1 ? "" : "s"} a month` : "None found"}
        </Stat>
        <Stat className="col-span-6 xl:col-span-3" label="Next 7 days" value={money(week.reduce((a, b) => a + b, 0))}>
          {week.length ? `${week.length} charge${week.length === 1 ? "" : "s"} due` : "Nothing due this week"}
        </Stat>
      </Grid>

      <Grid className="items-start">
        <Panel className="col-span-12 gap-3 xl:col-span-8">
          <Tabs label="Filter" value={tab} options={tabs} onChange={setTab} />
          <ListHeader cols={cols}>
            <span className="eyebrow">Name</span>
            <span className="eyebrow">Schedule</span>
            <span className="eyebrow">Next</span>
            <span className="eyebrow">Paid from</span>
            <span className="eyebrow text-right">Amount</span>
            <span className="eyebrow text-right">Per year</span>
            <span />
          </ListHeader>
          <div className="-mt-3 flex flex-col">
            {groups
              .filter((g) => tab === "all" || tab === g.id)
              .map((g) => ({ g, list: visible(byGroup.get(g.id)!) }))
              .filter(({ list }) => list.length > 0)
              .map(({ g, list }) => (
                <div key={g.id}>
                  <div className="flex items-center justify-between px-2 pt-3 pb-1">
                    <span className="flex items-center gap-2 text-[12.5px] font-bold">
                      <span className="size-2 rounded-full" style={{ background: g.color }} />
                      {g.title}
                    </span>
                    {g.id !== "income" && <span className="num text-[12px] text-ink-3">{money(list.reduce((a, s) => a + monthlyAmount(s), 0), 2)} a month</span>}
                  </div>
                  {list.map((s) => {
                    const inflow = s.direction === "inflow";
                    const ch = changes.get(s.stream_id);
                    const name = streamName(s);
                    const account = s.account_id ? shortAccount(s.account_name, s.institution_name, s.account_mask) : "—";
                    return (
                      <div key={s.stream_id} className={cn("num grid min-h-11 items-center gap-3.5 border-t border-hairline px-2 py-1 text-[13px]", cols)}>
                        <span className="flex min-w-0 items-center gap-2.5">
                          <CategoryIcon id={s.category} size={28} />
                          <span className="flex min-w-0 flex-col">
                            <span className="truncate font-semibold" title={s.description}>
                              {name}
                            </span>
                            <span className="truncate text-[11px] text-ink-3">
                              {category(s.category).label} · {sourceLabel[s.source]}
                            </span>
                          </span>
                          {ch && (
                            <Chip>
                              Price {ch.to > ch.from ? "up" : "down"} {money(Math.abs(ch.to - ch.from), 2)}
                            </Chip>
                          )}
                          {s.status === "EARLY_DETECTION" && <Chip className="bg-track text-ink-3">New</Chip>}
                        </span>
                        <span className="text-ink-2">
                          {frequencyLabel[s.frequency]}
                          {!ch && varies(s) && g.id !== "income" ? " · varies" : ""}
                        </span>
                        <span className="text-ink-2">{s.predicted_next_date ? fmtDay(s.predicted_next_date) : "—"}</span>
                        <span className="truncate text-ink-2">{inflow && s.account_id ? `Into ${account}` : account}</span>
                        <span className={cn("text-right font-semibold", inflow && "text-moss")}>
                          {inflow ? "+" : ""}
                          {money(streamAmount(s), 2)}
                        </span>
                        <span className="text-right text-ink-2">{money(yearlyAmount(s))}</span>
                        <RowMenu stream={s} onEdit={() => openEdit(s)} onAdd={() => openAdd(fromStream(s))} onHide={() => void hide(s)} />
                      </div>
                    );
                  })}
                </div>
              ))}
            {visible(streams).filter((s) => tab === "all" || streamGroup(s) === tab).length === 0 && (
              <p className="px-2 pt-4 text-[13px] text-ink-3">Nothing here from {source === "all" ? "any source" : sourceLabel[source].toLowerCase()}.</p>
            )}
          </div>
          {all.hidden.length > 0 && (
            <div className="border-t border-line pt-3">
              <button type="button" onClick={() => setShowHidden((v) => !v)} className="cursor-pointer border-0 bg-transparent p-0 text-[12.5px] font-semibold text-clay-ink hover:text-clay">
                {showHidden ? "Hide" : "Show"} {all.hidden.length} marked as not recurring
              </button>
              {showHidden && (
                <div className="mt-2 flex flex-col">
                  {all.hidden.map((s) => (
                    <div key={s.stream_id} className="flex items-center gap-3 border-t border-hairline py-1.5 text-[13px] text-ink-3">
                      <span className="min-w-0 flex-1 truncate">
                        {streamName(s)} · {frequencyLabel[s.frequency]} · {money(streamAmount(s), 2)}
                      </span>
                      <Button variant="ghost" size="xs" onClick={() => void unhide(s)}>
                        Show again
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </Panel>

        <div className="col-span-12 flex flex-col gap-4 xl:col-span-4">
          <Panel className="gap-3">
            <PanelHeader title={fmtMonth(today)} description={`${charges} charge${charges === 1 ? "" : "s"} and deposit${charges === 1 ? "" : "s"} this month`} />
            <div className="grid grid-cols-7 gap-1">
              {WEEKDAY_INITIALS.map((w, i) => (
                <span key={i} className="eyebrow text-center">
                  {w}
                </span>
              ))}
              {monthCells(today).map((c, i) => {
                if (!c.day) return <div key={i} className="h-10" />;
                const isToday = c.day === today;
                const past = c.day < today;
                return (
                  <div
                    key={c.day}
                    className={cn(
                      "flex h-10 flex-col items-center justify-center gap-1 rounded-[3px] border",
                      isToday ? "border-clay bg-clay-soft" : past ? "border-transparent" : "border-transparent bg-row-hover",
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
            <div className="flex flex-wrap gap-3 text-xs text-ink-3">
              {groups.map((g) => (
                <span key={g.id} className="flex items-center gap-1.5">
                  <span className="size-2 rounded-full" style={{ background: g.color }} />
                  {g.title}
                </span>
              ))}
            </div>
          </Panel>

          {change && (
            <Panel className="gap-2">
              <div className="eyebrow text-clay-ink">Price change</div>
              <div className="text-[15px] font-semibold">{streamName(change)}</div>
              <div className="num flex items-baseline gap-2.5 text-[14px]">
                <span className="text-ink-3 line-through">{money(changes.get(change.stream_id)!.from, 2)}</span>
                <span className="font-semibold">
                  {money(changes.get(change.stream_id)!.to, 2)} {per[change.frequency]}
                </span>
              </div>
              <p className="m-0 text-[12.5px] leading-[1.45] text-ink-2">
                {change.predicted_next_date && diffDays(today, change.predicted_next_date) >= 0
                  ? `Next charge ${fmtRelativeDay(change.predicted_next_date, today).toLowerCase() === "today" ? "today" : `on ${fmtRelativeDay(change.predicted_next_date, today)}`}`
                  : "Charged already"}{" "}
                to your {shortAccount(change.account_name, change.institution_name, change.account_mask)}.{" "}
                {money(Math.abs(changes.get(change.stream_id)!.to - changes.get(change.stream_id)!.from) * (yearlyAmount(change) / Math.max(streamAmount(change), 0.01)), 2)}{" "}
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
                height={10}
                segments={subsSorted.map((s, i) => ({ key: s.stream_id, value: monthlyAmount(s), color: SUB_SHADES[Math.min(i, SUB_SHADES.length - 1)], title: streamName(s) }))}
              />
              <p className="m-0 text-[12.5px] leading-[1.45] text-ink-3">
                {subsMonthly > 0 ? `${streamName(subsSorted[0])} is ${Math.round((monthlyAmount(subsSorted[0]) / subsMonthly) * 100)}% of your subscription spend.` : ""}
              </p>
            </Panel>
          )}
          <p className="m-0 px-1 text-xs text-ink-4">
            Detected streams come from your transaction history; next dates are estimates.{" "}
            {plaidOn ? "Plaid’s streams use Plaid’s predictions." : (
              <>
                Plaid’s own detection is an add-on in <Link to="/settings?focus=add-ons" className="font-semibold text-clay-ink">Settings</Link>.
              </>
            )}
          </p>
        </div>
      </Grid>
      {entryDialog}
    </Page>
  );
}

function RowMenu({ stream, onEdit, onAdd, onHide }: { stream: StreamRow; onEdit: () => void; onAdd: () => void; onHide: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button" aria-label={`Options for ${streamName(stream)}`} className="flex size-7 cursor-pointer items-center justify-center rounded-[3px] border-0 bg-transparent text-ink-3 hover:bg-row-hover hover:text-ink">
          <MoreIcon size={16} />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        {stream.source === "manual" ? (
          <DropdownMenuItem onSelect={onEdit}>Edit…</DropdownMenuItem>
        ) : (
          <>
            <DropdownMenuItem onSelect={onAdd}>Add by hand from this…</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={onHide}>Not recurring</DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
