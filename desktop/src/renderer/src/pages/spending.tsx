import { useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router";

import { BudgetDialog } from "@/components/budget-dialog";
import { monthCells, Sparkline, WEEKDAY_SHORT } from "@/components/charts/chart";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { Page, PageIntro } from "@/components/page-intro";
import { Empty, Panel, PanelHeader, Swatch } from "@/components/panel";
import { Segmented } from "@/components/segmented";
import { Button } from "@/components/ui/button";
import { useDataVersion } from "@/hooks/use-data-version";
import { useLoad } from "@/hooks/use-load";
import { rememberMerchant } from "@/lib/categorize";
import {
  addDays,
  addMonths,
  dayOfMonth,
  diffDays,
  fmtDay,
  fmtLongDay,
  fmtMonth,
  fmtMonthShort,
  minDate,
  monthStart,
  quarterStart,
  todayISO,
  yearStart,
  type ISODate,
} from "@/lib/dates";
import { humanize } from "@/lib/format";
import { primaryCurrency, shortAccount } from "@/lib/insights/accounts";
import {
  byCategory,
  coveredFrom,
  heatLevel,
  indexDaily,
  indexMonthly,
  spendingHeadline,
  suggestBudgets,
  usualSoFar,
  type Daily,
} from "@/lib/insights/spending";
import { fmt, num } from "@/lib/money";
import { dateRange, loadBudgets } from "@/lib/queries";
import { topper } from "@/lib/topper";
import type { CategorizedRow } from "@/lib/topper-types";
import { cn } from "@/lib/utils";

import { categories, category, isSpending } from "@shared/categories";

type Period = "month" | "quarter" | "year";

const periods: { value: Period; label: string }[] = [
  { value: "month", label: "Month" },
  { value: "quarter", label: "Quarter" },
  { value: "year", label: "Year" },
];

// How many months a period spans, how to step back one, and how many
// earlier periods "usual" averages over.
const shape: Record<Period, { months: number; start: (d: ISODate) => ISODate; compare: number }> = {
  month: { months: 1, start: monthStart, compare: 3 },
  quarter: { months: 3, start: quarterStart, compare: 3 },
  year: { months: 12, start: yearStart, compare: 1 },
};

const HEAT = ["#F3E4DA", "#E8C7B5", "#D9A184", "#C57A58", "#A3472B"];

export function SpendingPage() {
  const [params, setParams] = useSearchParams();
  const period: Period = (["month", "quarter", "year"] as const).find((p) => p === params.get("period")) ?? "month";
  const today = todayISO();
  const { version } = useDataVersion();
  const sortRef = useRef<HTMLElement>(null);

  const { months, start, compare } = shape[period];
  const periodStart = start(today);
  const periodEnd = addDays(addMonths(periodStart, months), -1);
  const thisMonth = monthStart(today);

  const [data] = useLoad(async () => {
    const dailyFrom = minDate(addMonths(periodStart, -months * compare), addMonths(thisMonth, -3));
    const [accounts, daily, monthly, merchants, budgets, queue] = await Promise.all([
      topper.accounts({ limit: 1000, select: ["account_id", "iso_currency_code", "unofficial_currency_code"] }),
      topper.categoriesDaily({ filters: { day: dateRange(dailyFrom, today) } }),
      topper.categoriesMonthly({ filters: { month: [`gte.${addMonths(thisMonth, -12)}`] } }),
      topper.merchantsMonthly({ filters: { month: dateRange(monthStart(periodStart), thisMonth) } }),
      loadBudgets(),
      topper.categorized({
        filters: { needs_category: "eq.true" },
        select: ["transaction_id", "date", "name", "merchant_name", "merchant_key", "amount", "iso_currency_code", "pfc_detailed", "plaid_category", "account_name", "account_mask", "institution_name"],
        orderBy: "date",
        order: "desc",
        limit: 50,
      }),
    ]);
    return { accounts: accounts.data, daily, monthly, merchants, budgets, queue: queue.data };
  }, [period, version]);

  useEffect(() => {
    if (data.ok === true && params.get("focus") === "sort") sortRef.current?.scrollIntoView({ block: "start", behavior: "smooth" });
  }, [data.ok, params]);

  if (data.ok === "loading") return <Loading />;
  if (!data.ok) return <LoadError what="spending" message={data.error} />;
  return (
    <SpendingView
      {...data.data}
      today={today}
      period={period}
      periodStart={periodStart}
      periodEnd={periodEnd}
      sortRef={sortRef}
      onPeriod={(p) => setParams(p === "month" ? {} : { period: p })}
    />
  );
}

function SpendingView({
  accounts,
  daily,
  monthly,
  merchants,
  budgets,
  queue,
  today,
  period,
  periodStart,
  periodEnd,
  sortRef,
  onPeriod,
}: {
  accounts: { iso_currency_code: string | null; unofficial_currency_code: string | null }[];
  daily: Awaited<ReturnType<typeof topper.categoriesDaily>>;
  monthly: Awaited<ReturnType<typeof topper.categoriesMonthly>>;
  merchants: Awaited<ReturnType<typeof topper.merchantsMonthly>>;
  budgets: Map<string, number>;
  queue: CategorizedRow[];
  today: ISODate;
  period: Period;
  periodStart: ISODate;
  periodEnd: ISODate;
  sortRef: React.RefObject<HTMLElement | null>;
  onPeriod: (p: Period) => void;
}) {
  const currency = primaryCurrency(accounts);
  const money = (n: number, d = 0) => fmt(n, d, currency);
  const { months, compare } = shape[period];
  const thisMonth = monthStart(today);

  const idx = indexDaily(daily, currency);
  const monthlyIdx = indexMonthly(monthly, currency);
  const suggestions = suggestBudgets(monthlyIdx, thisMonth);
  const hasData = coveredFrom(idx);

  const spent = byCategory(idx, periodStart, today);
  const totalSpent = [...spent.values()].reduce((a, v) => a + v, 0);
  const elapsed = diffDays(periodStart, today) + 1;
  const length = diffDays(periodStart, periodEnd) + 1;
  const paceTick = elapsed / length;
  const budgetTotal = [...budgets.values()].reduce((a, v) => a + v, 0) * months;

  const rows = categories
    .filter((c) => c.kind === "spending" && (Math.abs(spent.get(c.id) ?? 0) >= 0.005 || (budgets.get(c.id) ?? 0) > 0))
    .map((c) => {
      const amount = spent.get(c.id) ?? 0;
      const budget = (budgets.get(c.id) ?? 0) * months;
      const usual = usualSoFar(idx, periodStart, elapsed, compare, (s, i) => addMonths(s, -months * i), (x) => x === c.id, hasData);
      const history = Array.from({ length: 5 }, (_, i) => monthlyIdx.get(addMonths(thisMonth, i - 5))?.get(c.id) ?? 0);
      return { cat: c, amount, budget, usual, history };
    })
    .sort((a, b) => b.amount - a.amount);

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const selected = rows.find((r) => r.cat.id === selectedId) ?? rows[0];
  const [budgetOpen, setBudgetOpen] = useState(false);

  const periodName = period === "month" ? fmtMonth(periodStart) : period === "quarter" ? "this quarter" : periodStart.slice(0, 4);
  const headline = spendingHeadline(spent, periodName, (n) => money(n));
  const eyebrowRange =
    period === "month"
      ? `${fmtMonth(periodStart)} 1–${dayOfMonth(today)}`
      : `${fmtDay(periodStart)} – ${fmtDay(today)}`;
  const historyLabel = `${fmtMonthShort(addMonths(thisMonth, -5))}–${fmtMonthShort(addMonths(thisMonth, -1))}`;

  return (
    <Page>
      <PageIntro
        eyebrow={`Spending · ${eyebrowRange}`}
        actions={
          <>
            <Button variant="outline" onClick={() => setBudgetOpen(true)}>
              {budgets.size ? "Edit budgets" : "Set budgets"}
            </Button>
            <Segmented label="Period" value={period} options={periods} onChange={onPeriod} />
          </>
        }
      >
        {headline ? (
          <>
            {headline.lead}
            <em>{headline.amount}</em>
            {headline.tail}
          </>
        ) : (
          <>Nothing spent yet {period === "month" ? `in ${periodName}` : periodName}.</>
        )}
      </PageIntro>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <Panel className="gap-3.5">
          <PanelHeader
            title="Categories"
            description={budgets.size ? "The tick on each bar marks where you’d be if spending were even across the period." : "Set budgets to see how each category is tracking."}
          >
            <div className="num text-right">
              <div className="font-serif text-2xl leading-none">{money(totalSpent)}</div>
              {budgetTotal > 0 && <div className="mt-1 text-xs text-ink-3">of {money(budgetTotal)}</div>}
            </div>
          </PanelHeader>
          <div className="grid grid-cols-[1.55fr_0.75fr_1.75fr_0.85fr_0.9fr] gap-4 border-b border-line px-3 pb-2">
            <span className="eyebrow">Category</span>
            <span className="eyebrow text-right">Spent</span>
            <span className="eyebrow">Budget</span>
            <span className="eyebrow text-right">vs usual</span>
            <span className="eyebrow text-right">{historyLabel}</span>
          </div>
          <div className="-mt-2 flex flex-col gap-0.5">
            {rows.length === 0 && <p className="px-3 text-[13px] text-ink-3">No spending in this period yet.</p>}
            {rows.map((r) => {
              const on = r.cat.id === selected?.cat.id;
              const delta = r.usual && r.usual > 0 ? Math.round((r.amount / r.usual - 1) * 100) : null;
              const ahead = r.budget > 0 && !r.cat.fixed && r.amount / r.budget > paceTick + 0.1;
              return (
                <button
                  key={r.cat.id}
                  type="button"
                  aria-pressed={on}
                  onClick={() => setSelectedId(r.cat.id)}
                  className={cn(
                    "grid min-h-14 cursor-pointer grid-cols-[1.55fr_0.75fr_1.75fr_0.85fr_0.9fr] items-center gap-4 rounded-[10px] border-0 px-3 py-2 text-left text-ink",
                    on ? "bg-row-active" : "bg-transparent hover:bg-row-hover",
                  )}
                >
                  <span className={cn("flex items-center gap-2.5 text-sm", on ? "font-bold" : "font-medium")}>
                    <Swatch color={r.cat.color} />
                    {r.cat.label}
                  </span>
                  <span className="num text-right text-sm font-semibold">{money(r.amount)}</span>
                  <span className="flex flex-col gap-[5px]">
                    <span className="relative h-2 rounded-[4px] bg-track">
                      {r.budget > 0 && (
                        <>
                          <span className="absolute inset-y-0 left-0 rounded-[4px]" style={{ width: `${Math.min(100, Math.max(0, (r.amount / r.budget) * 100))}%`, background: r.cat.color }} />
                          <span className="absolute -top-[3px] h-3.5 w-0.5 rounded-[1px] bg-ink" style={{ left: `${Math.min(100, paceTick * 100)}%` }} />
                        </>
                      )}
                    </span>
                    <span className="flex justify-between text-[11.5px] text-ink-3">
                      <span className="num">
                        {r.budget > 0 ? (r.amount <= r.budget ? `${money(r.budget - r.amount)} left` : `${money(r.amount - r.budget)} over`) : "No budget"}
                      </span>
                      {ahead && <span className="font-semibold text-clay">Ahead of pace</span>}
                    </span>
                  </span>
                  <span
                    className={cn(
                      "num text-right text-[13.5px] font-semibold",
                      delta === null ? "text-ink-3" : delta > 5 ? "text-clay" : delta < -5 ? "text-moss" : "text-ink-3",
                    )}
                  >
                    {delta === null ? "—" : delta === 0 ? "Same" : `${delta > 0 ? "+" : "−"}${Math.abs(delta)}%`}
                  </span>
                  <span className="flex justify-end">
                    <Sparkline values={r.history} color={r.cat.color} />
                  </span>
                </button>
              );
            })}
          </div>
        </Panel>

        <Panel className="gap-[18px]" aria-live="polite">
          {selected ? (
            <SelectedCategory
              row={selected}
              merchants={merchants}
              currency={currency}
              monthlyIdx={monthlyIdx}
              thisMonth={thisMonth}
              today={today}
              period={period}
              periodStart={periodStart}
            />
          ) : (
            <Empty title="Nothing to show yet">Pick a category once transactions have synced.</Empty>
          )}
        </Panel>
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <DayByDay idx={idx} today={today} money={money} />
        <SortUnknowns queue={queue} today={today} currency={currency} sectionRef={sortRef} />
      </div>

      <BudgetDialog open={budgetOpen} onOpenChange={setBudgetOpen} budgets={budgets} suggestions={suggestions} currency={currency} />
    </Page>
  );
}

function SelectedCategory({
  row,
  merchants,
  currency,
  monthlyIdx,
  thisMonth,
  today,
  period,
  periodStart,
}: {
  row: { cat: (typeof categories)[number]; amount: number; budget: number; usual: number | null };
  merchants: Awaited<ReturnType<typeof topper.merchantsMonthly>>;
  currency: string;
  monthlyIdx: Map<ISODate, Map<string, number>>;
  thisMonth: ISODate;
  today: ISODate;
  period: Period;
  periodStart: ISODate;
}) {
  const money = (n: number, d = 0) => fmt(n, d, currency);
  const series = Array.from({ length: 6 }, (_, i) => {
    const m = addMonths(thisMonth, i - 5);
    return { month: m, value: monthlyIdx.get(m)?.get(row.cat.id) ?? 0 };
  });
  const max = Math.max(...series.map((s) => s.value), 1);

  const byMerchant = new Map<string, { name: string; amount: number; count: number }>();
  for (const m of merchants) {
    if (m.category !== row.cat.id || (m.iso_currency_code ?? "CAD") !== currency || m.month < monthStart(periodStart)) continue;
    const cur = byMerchant.get(m.merchant_key) ?? { name: m.merchant, amount: 0, count: 0 };
    cur.amount += num(m.amount);
    cur.count += m.transactions;
    byMerchant.set(m.merchant_key, cur);
  }
  const list = [...byMerchant.values()].sort((a, b) => b.amount - a.amount);
  const top = list.slice(0, 4);
  const others = list.slice(4);
  if (others.length) {
    top.push({ name: `${others.length} other${others.length === 1 ? "" : "s"}`, amount: others.reduce((a, o) => a + o.amount, 0), count: others.reduce((a, o) => a + o.count, 0) });
  }

  const usualText =
    row.usual !== null
      ? `usually ${money(row.usual)} by ${period === "month" ? fmtDay(today) : "this point"}`
      : "not enough history to compare";

  return (
    <>
      <div className="flex flex-col gap-1.5">
        <div className="flex items-center gap-2 text-[13px] font-semibold">
          <Swatch color={row.cat.color} />
          {row.cat.label}
        </div>
        <div className="num font-serif text-[52px] leading-none tracking-[-0.02em]">{money(row.amount, 2)}</div>
        <div className="text-[13px] text-ink-3">
          {row.budget > 0 ? `of ${money(row.budget)} budgeted · ` : ""}
          {usualText}
        </div>
      </div>
      <div className="flex flex-col gap-2">
        <div className="eyebrow">Last six months</div>
        <div className="flex h-[140px] items-end gap-2.5 border-b border-line">
          {series.map((s, i) => (
            <div key={s.month} className="flex h-full flex-1 basis-0 flex-col items-center justify-end gap-1">
              <span className="num text-[11px] text-ink-2">{money(s.value)}</span>
              <div
                className={cn("w-full rounded-t-[4px]", i === 5 && "hatched")}
                style={{ height: `${Math.max(0, Math.round((s.value / max) * 112))}px`, background: i === 5 ? undefined : row.cat.color, ["--hatch" as string]: row.cat.color }}
              />
            </div>
          ))}
        </div>
        <div className="flex gap-2.5">
          {series.map((s, i) => (
            <span key={s.month} className="flex-1 basis-0 text-center text-[11px] text-ink-3">
              {i === 5 ? `${fmtMonthShort(s.month)} so far` : fmtMonthShort(s.month)}
            </span>
          ))}
        </div>
      </div>
      <div className="flex flex-col">
        <div className="eyebrow mb-1.5">Where</div>
        {top.length === 0 && <p className="m-0 text-[13px] text-ink-3">No charges in this period.</p>}
        {top.map((m) => (
          <div key={m.name} className="flex items-center gap-2.5 border-t border-hairline py-2 text-[13.5px]">
            <span className="min-w-0 flex-1 truncate font-medium">{m.name}</span>
            <span className="text-xs text-ink-3">{m.count === 1 ? "1 charge" : `${m.count} charges`}</span>
            <span className="num w-[76px] text-right font-semibold">{money(m.amount, 2)}</span>
          </div>
        ))}
      </div>
    </>
  );
}

function DayByDay({ idx, today, money }: { idx: Daily; today: ISODate; money: (n: number) => string }) {
  const cells = monthCells(today);
  const totals = new Map<ISODate, number>();
  for (const c of cells) if (c.day && c.day <= today) totals.set(c.day, [...(idx.get(c.day) ?? [])].reduce((a, [cat, v]) => a + (isSpending(cat) ? v : 0), 0));
  const max = Math.max(0, ...totals.values());
  let biggest: ISODate | null = null;
  for (const [d, v] of totals) if (v > 0 && (!biggest || v > (totals.get(biggest) ?? 0))) biggest = d;

  return (
    <Panel className="gap-3.5">
      <PanelHeader title="Day by day" description={biggest ? `Biggest day so far: ${fmtLongDay(biggest)}, at ${money(totals.get(biggest)!)}.` : "Nothing spent yet this month."}>
        <div className="flex items-center gap-1 pt-1.5 text-[11px] text-ink-3">
          <span className="mr-1">Less</span>
          {HEAT.map((c) => (
            <span key={c} className="size-3.5 rounded-[3px]" style={{ background: c }} />
          ))}
          <span className="ml-1">More</span>
        </div>
      </PanelHeader>
      <div className="grid grid-cols-7 gap-1.5">
        {WEEKDAY_SHORT.map((w) => (
          <span key={w} className="eyebrow text-center">
            {w}
          </span>
        ))}
        {cells.map((c, i) => {
          if (!c.day) return <div key={i} />;
          const past = c.day <= today;
          const v = totals.get(c.day) ?? 0;
          const level = heatLevel(v, max);
          return (
            <div
              key={c.day}
              title={past ? `${fmtDay(c.day)}: ${money(v)}` : undefined}
              className={cn("flex h-[46px] flex-col justify-between rounded-lg px-[7px] py-[5px]", !past && "border border-dashed border-line-strong text-ink-3")}
              style={past ? { background: v > 0 ? HEAT[level] : "var(--color-hairline)", color: level === 4 && v > 0 ? "#fff" : undefined, border: c.day === today ? "2px solid var(--color-ink)" : undefined } : undefined}
            >
              <span className="num text-[11px] font-semibold">{dayOfMonth(c.day)}</span>
              <span className="num self-end text-[11px]">{past && v > 0 ? money(v) : ""}</span>
            </div>
          );
        })}
      </div>
    </Panel>
  );
}

function SortUnknowns({
  queue: initial,
  today,
  currency,
  sectionRef,
}: {
  queue: CategorizedRow[];
  today: ISODate;
  currency: string;
  sectionRef: React.RefObject<HTMLElement | null>;
}) {
  const { bump } = useDataVersion();
  // One entry per merchant: sorting a merchant sorts all its transactions.
  const queue = useMemo(() => {
    const seen = new Set<string>();
    return initial.filter((t) => (seen.has(t.merchant_key) ? false : (seen.add(t.merchant_key), true))).slice(0, 12);
  }, [initial]);
  const [index, setIndex] = useState(0);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const done = index >= queue.length;
  const cur = queue[Math.min(index, queue.length - 1)];

  const choose = async (cat: string) => {
    if (!cur) return;
    setSaving(true);
    setError(null);
    try {
      await rememberMerchant(cur.merchant_key, cat);
      setIndex((i) => i + 1);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const guess = cur && cur.plaid_category !== "other" ? cur.plaid_category : null;
  const why = cur?.pfc_detailed
    ? `Plaid guessed ${humanize(cur.pfc_detailed.replace(/^[A-Z]+(_AND_[A-Z]+)?_/, "")).toLowerCase()}, without much confidence.`
    : "Plaid could not place it.";

  return (
    <Panel ref={sectionRef} className="scroll-mt-6 gap-3.5">
      <PanelHeader title="Sort the unknowns" description="Pick a category once and Money Insighter remembers the merchant.">
        {queue.length > 0 && (
          <span className="flex h-[26px] items-center rounded-full bg-ochre-bg px-2.5 text-xs font-semibold whitespace-nowrap text-ochre-ink">
            {done ? "Done" : `${queue.length - index} left`}
          </span>
        )}
      </PanelHeader>
      {queue.length === 0 ? (
        <Empty title="All caught up.">Every transaction has a category you or Plaid are sure of.</Empty>
      ) : (
        <>
          <div className="flex gap-1">
            {queue.map((q, i) => (
              <span key={q.transaction_id} className={cn("h-[5px] flex-1 rounded-[3px]", i < index ? "bg-clay" : "bg-segment")} />
            ))}
          </div>
          {!done && cur ? (
            <div className="flex flex-col gap-3.5">
              <div className="flex items-start justify-between gap-4 rounded-xl bg-paper px-[18px] py-4">
                <div className="flex min-w-0 flex-col gap-1.5">
                  <span className="truncate font-mono text-[13.5px] font-semibold">{cur.name}</span>
                  <span className="text-[12.5px] text-ink-3">
                    {fmtRelativeOrLong(cur.date, today)} · {shortAccount(cur.account_name, cur.institution_name, cur.account_mask)}
                  </span>
                  <span className="text-[12.5px] text-ink-2">
                    {guess ? (
                      <>
                        Best guess: <strong>{category(guess).label}</strong>. {why}
                      </>
                    ) : (
                      why
                    )}
                  </span>
                </div>
                <span className="num font-serif text-[28px] leading-none">{fmt(num(cur.amount), 2, cur.iso_currency_code ?? currency)}</span>
              </div>
              <div className="flex flex-wrap gap-2">
                {categories
                  .filter((c) => c.kind !== "income")
                  .map((c) => {
                    const suggested = c.id === guess;
                    return (
                      <button
                        key={c.id}
                        type="button"
                        disabled={saving}
                        onClick={() => void choose(c.id)}
                        className={cn(
                          "flex h-10 cursor-pointer items-center gap-2 rounded-full border-[1.5px] px-3.5 text-[13px] text-ink disabled:opacity-60",
                          suggested ? "border-clay bg-clay-soft font-semibold" : "border-line-strong bg-sheet font-medium hover:bg-row-hover",
                        )}
                      >
                        <Swatch color={c.color} className="size-[9px] rounded-[2px]" />
                        {c.label}
                      </button>
                    );
                  })}
              </div>
              {error && <p className="m-0 text-[12.5px] text-destructive">{error}</p>}
            </div>
          ) : (
            <div className="flex flex-1 flex-col items-start justify-center gap-2.5 py-4">
              <span className="font-serif text-[30px] leading-tight">All caught up.</span>
              <span className="text-[13.5px] text-ink-3">
                {queue.length === 1 ? "One merchant" : `${queue.length} merchants`} learned. Next time they’ll be sorted automatically.
              </span>
              <Button
                variant="outline"
                onClick={() => {
                  setIndex(0);
                  bump();
                }}
              >
                Refresh
              </Button>
            </div>
          )}
        </>
      )}
    </Panel>
  );
}

function fmtRelativeOrLong(day: ISODate, today: ISODate): string {
  const n = diffDays(day, today);
  return n === 0 ? "Today" : n === 1 ? "Yesterday" : fmtLongDay(day).replace(/^(\w{3})\w*,/, "$1,");
}
