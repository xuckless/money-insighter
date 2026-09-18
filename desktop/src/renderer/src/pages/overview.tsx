import { Link, useSearchParams } from "react-router";

import { CategoryIcon } from "@/components/category-icon";
import { ChartFrame, Dot, Line, Area, bandPath, linePath, scale, Sparkline, StackedBar, Meter, type Point } from "@/components/charts/chart";
import { ChevronDownIcon } from "@/components/icons";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { Grid, Page, PageHeader } from "@/components/page-header";
import { Chip, Empty, Legend, Panel, PanelHeader, PanelLink, PanelTitle, Stat, Swatch } from "@/components/panel";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { useSettings } from "@/hooks/use-app-state";
import { useDataVersion } from "@/hooks/use-data-version";
import { useLoad } from "@/hooks/use-load";
import {
  addDays,
  addMonths,
  dayOfMonth,
  daysInMonth,
  diffDays,
  fmtDay,
  fmtLongDay,
  fmtMonth,
  fmtMonthShort,
  fmtMonthYear,
  fmtRelativeDay,
  minDate,
  monthEnd,
  monthStart,
  todayISO,
  type ISODate,
} from "@/lib/dates";
import { accountClass, cardUtilization, currencyOf, isCash, netWorthSeries, primaryCurrency, shortAccount, summarize } from "@/lib/insights/accounts";
import { projectBalance, streamFlows } from "@/lib/insights/cashflow";
import { notes, type Run } from "@/lib/insights/notes";
import { monthlyAmount, occurrences, streamName, varies } from "@/lib/insights/recurring";
import { byCategory, coveredFrom, cumulative, firstDataDay, indexDaily, indexMonthly, isPaceCategory, monthsBack, project, usualSoFar } from "@/lib/insights/spending";
import { fmt, fmtAxis, fmtPct, fmtSigned, niceCeil, num } from "@/lib/money";
import { NEEDS_RELINK } from "@/lib/plaidsync-types";
import { dateRange, loadActiveStreams, loadBudgets, resolvePriceChanges } from "@/lib/queries";
import { topper } from "@/lib/topper";
import type { AccountRow, StreamRow } from "@/lib/topper-types";

import { category, isBill, isIncome } from "@shared/categories";

const MONTH_PARAM = /^\d{4}-\d{2}$/;

export function OverviewPage() {
  const [params, setParams] = useSearchParams();
  const today = todayISO();
  const thisMonth = monthStart(today);
  const requested = params.get("month");
  const month = requested && MONTH_PARAM.test(requested) && `${requested}-01` <= thisMonth ? `${requested}-01` : thisMonth;
  const { version } = useDataVersion();
  const [settings] = useSettings();
  const recurringOn = settings?.recurringEnabled ?? false;

  const [data] = useLoad(async () => {
    if (settings === null) return null;
    const mEnd = monthEnd(month);
    const historyFrom = minDate(addDays(month, -90), addMonths(month, -3));
    const [accounts, daily, monthly, budgets, balances, recent, needs, status, streams] = await Promise.all([
      topper.accounts({ limit: 1000 }),
      topper.categoriesDaily({ filters: { day: dateRange(historyFrom, mEnd) } }),
      topper.categoriesMonthly({ filters: { month: [`gte.${addMonths(month, -6)}`] } }),
      loadBudgets(),
      topper.balancesDaily({ filters: { day: [`gte.${addDays(today, -30)}`] } }),
      topper.categorized({
        filters: { date: [`lte.${mEnd}`] },
        select: ["transaction_id", "date", "name", "merchant_name", "amount", "iso_currency_code", "category", "needs_category", "account_name", "account_mask", "institution_name", "pending"],
        limit: 6,
      }),
      topper.categorized({ filters: { needs_category: "eq.true" }, select: ["transaction_id"], limit: 1, count: true }),
      topper.syncStatus({ limit: 1000 }),
      loadActiveStreams(today, recurringOn),
    ]);
    const priceChanges = await resolvePriceChanges(streams);
    return { accounts: accounts.data, daily, monthly, budgets, balances, recent: recent.data, needs: needs.total ?? needs.count, status: status.data, streams, priceChanges };
  }, [month, version, recurringOn, settings === null]);

  if (data.ok === "loading" || (data.ok === true && data.data === null)) return <Loading />;
  if (!data.ok) return <LoadError what="the overview" message={data.error} />;

  const d = data.data!;
  const currency = primaryCurrency(d.accounts);
  const accounts = d.accounts.filter((a) => currencyOf(a) === currency);
  const mEnd = monthEnd(month);
  const asOf = minDate(today, mEnd);
  const isCurrent = month === thisMonth;
  const money = (n: number, digits = 0) => fmt(n, digits, currency);

  // Spending.
  const idx = indexDaily(d.daily, currency);
  const hasData = coveredFrom(idx);
  const pace = project(idx, month, mEnd, asOf, isPaceCategory);
  const budgetTotal = [...d.budgets].filter(([c]) => isPaceCategory(c)).reduce((a, [, v]) => a + v, 0);
  const spentByCat = byCategory(idx, month, asOf, isPaceCategory);
  const elapsed = diffDays(month, asOf) + 1;
  const usual = new Map<string, number | null>();
  for (const cat of spentByCat.keys()) {
    usual.set(cat, usualSoFar(idx, month, elapsed, 3, monthsBack, (c) => c === cat, hasData));
  }

  // Balances.
  const summary = summarize(accounts);
  const nw = netWorthSeries(d.balances, currency, addDays(today, -30), today);
  const nwChange = nw.length >= 2 ? nw[nw.length - 1].value - nw[0].value : null;
  const util = cardUtilization(accounts);

  // Last complete month's savings.
  const monthly = indexMonthly(d.monthly, currency);
  const prev = addMonths(month, isCurrent ? -1 : 0);
  const prevTotals = monthly.get(prev) ?? new Map<string, number>();
  let income = 0;
  let spending = 0;
  for (const [cat, v] of prevTotals) {
    if (isIncome(cat)) income -= v;
    else if (category(cat).kind === "spending" || category(cat).kind === "bill") spending += v;
  }

  const reconnect = d.status.filter((s) => NEEDS_RELINK.has(s.status)).map((s) => s.institution_name ?? "A connection");
  const noteList = notes({
    currency,
    today,
    isCurrentMonth: isCurrent,
    spent: spentByCat,
    usual,
    fixed: isBill,
    budgetTotal,
    projected: pace.projected,
    priceChanges: d.streams
      .filter((s) => d.priceChanges.has(s.stream_id))
      .map((s) => ({ ...d.priceChanges.get(s.stream_id)!, name: streamName(s), next: s.predicted_next_date, yearly: monthlyAmount(s) * 12 })),
    needsCategory: d.needs,
    utilization: util.ratio,
    reconnect,
  }).slice(0, 4);

  const months = Array.from({ length: 12 }, (_, i) => addMonths(thisMonth, -i));

  return (
    <Page>
      <PageHeader
        title="Overview"
        subtitle={<Headline pace={pace} budgetTotal={budgetTotal} isCurrent={isCurrent} month={month} today={today} money={money} />}
        actions={
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                {fmtMonthYear(month)} <ChevronDownIcon size={14} />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-48">
              {months.map((m) => (
                <DropdownMenuItem key={m} onSelect={() => setParams(m === thisMonth ? {} : { month: m.slice(0, 7) })}>
                  {fmtMonthYear(m)}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        }
      />

      {accounts.length === 0 && (
        <Panel>
          <Empty title="Connect an account to begin" action={<Button asChild><Link to="/accounts">Go to Accounts</Link></Button>}>
            Money Insighter reads balances and transactions through Plaid and keeps them on this computer.
          </Empty>
        </Panel>
      )}

      <Grid>
        <Stat
          className="col-span-6 xl:col-span-3"
          label={isCurrent ? "Spent so far" : `Spent in ${fmtMonth(month)}`}
          value={money(pace.spent)}
          aside={budgetTotal > 0 ? <Meter ratio={pace.spent / budgetTotal} className="w-16" color={pace.spent > budgetTotal ? "var(--color-clay)" : "var(--color-moss)"} /> : undefined}
        >
          {budgetTotal > 0 ? `${money(budgetTotal)} budget · ${isCurrent ? `on pace for ${money(pace.projected)}` : pace.spent <= budgetTotal ? `${money(budgetTotal - pace.spent)} under` : `${money(pace.spent - budgetTotal)} over`}` : isCurrent ? `On pace for ${money(pace.projected)} · no budget set` : "Everyday spending, bills left out"}
        </Stat>
        <Stat
          className="col-span-6 xl:col-span-3"
          label="Net worth"
          value={money(summary.net)}
          aside={nw.length >= 2 ? <Sparkline values={nw.map((p) => p.value)} color="var(--color-moss)" width={88} height={28} /> : undefined}
        >
          {nwChange !== null && nw.length >= 7 ? (
            <span className={nwChange >= 0 ? "font-semibold text-moss" : "font-semibold text-clay"}>
              {fmtSigned(nwChange, 0, currency)} in the last {diffDays(nw[0].day, today)} days
            </span>
          ) : (
            <span>History builds from {fmtDay(nw[0]?.day ?? today)}</span>
          )}
        </Stat>
        <Stat className="col-span-6 xl:col-span-3" label="Cash" value={money(summary.byClass.chequing + summary.byClass.savings)}>
          Chequing {money(summary.byClass.chequing)} · Savings {money(summary.byClass.savings)}
        </Stat>
        <Stat className="col-span-6 xl:col-span-3" label="Owed on cards" value={money(util.owed)}>
          {util.ratio !== null ? (
            <span className="flex items-center gap-2.5">
              <Meter ratio={util.ratio} className="flex-1" />
              <span className="whitespace-nowrap">{fmtPct(util.ratio)} used</span>
            </span>
          ) : accounts.some((a) => accountClass(a.type, a.subtype) === "credit") ? (
            "No credit limits reported"
          ) : (
            "No cards connected"
          )}
        </Stat>
      </Grid>

      <Grid>
        <Panel className="col-span-12 xl:col-span-8">
          <PanelHeader title="Spending pace" description="Running total for the month. Bills, loans and transfers are left out.">
            <Legend
              items={[
                { label: fmtMonth(month), kind: "line", color: "var(--color-clay)" },
                { label: fmtMonth(addMonths(month, -1)), kind: "dash", color: "var(--color-stone)" },
                ...(isCurrent && pace.remainingDays > 0 ? [{ label: "Likely range", kind: "band" as const, color: "var(--color-band)" }] : []),
              ]}
            />
          </PanelHeader>
          <PaceChart idx={idx} month={month} asOf={asOf} pace={pace} budgetTotal={budgetTotal} showProjection={isCurrent} money={money} />
        </Panel>

        <Panel className="col-span-12 xl:col-span-4">
          <PanelHeader title="Where it went" description={income > 0 ? `Saved ${fmtPct((income - spending) / income)} of ${money(income)} take-home in ${fmtMonth(prev)}` : undefined}>
            <PanelLink to="/spending">All categories</PanelLink>
          </PanelHeader>
          <WhereItWent spent={spentByCat} money={money} />
        </Panel>
      </Grid>

      <Grid>
        <Panel className="col-span-12 gap-3 xl:col-span-4">
          <PanelHeader title="Recent">
            <PanelLink to="/transactions">All transactions</PanelLink>
          </PanelHeader>
          {d.recent.length === 0 ? (
            <p className="m-0 text-[13px] text-ink-3">No transactions yet.</p>
          ) : (
            <div className="flex flex-col">
              {d.recent.map((t) => {
                const cat = category(t.category);
                return (
                  <div key={t.transaction_id} className="flex items-center gap-3 border-t border-hairline py-2">
                    <CategoryIcon category={cat} muted={t.needs_category} size={30} />
                    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                      <span className="truncate text-[13px] font-semibold">{t.merchant_name ?? t.name}</span>
                      {t.needs_category ? (
                        <Chip>Needs a category</Chip>
                      ) : (
                        <span className="truncate text-xs text-ink-3">
                          {cat.label} · {shortAccount(t.account_name, t.institution_name, t.account_mask)}
                          {t.pending && " · Pending"}
                        </span>
                      )}
                    </div>
                    <div className="flex flex-col items-end gap-0.5">
                      <span className={`num text-[13px] font-semibold ${num(t.amount) < 0 ? "text-moss" : ""}`}>{fmtSigned(-num(t.amount), 2, t.iso_currency_code ?? currency)}</span>
                      <span className="text-xs text-ink-3">{fmtRelativeDay(t.date, today)}</span>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </Panel>

        <Panel className="col-span-12 gap-3 xl:col-span-4">
          <PanelHeader title="Coming up">
            <PanelLink to="/recurring">Recurring</PanelLink>
          </PanelHeader>
          <ComingUp streams={d.streams} accounts={accounts} priceChanges={d.priceChanges} today={today} money={money} />
        </Panel>

        <Panel className="col-span-12 gap-3 xl:col-span-4">
          <PanelTitle>Worth a look</PanelTitle>
          {noteList.length === 0 ? (
            <p className="m-0 text-[13px] text-ink-3">Nothing stands out right now.</p>
          ) : (
            <div className="flex flex-col">
              {noteList.map((n, i) => (
                <div key={n.id} className="flex gap-3 border-t border-hairline py-2.5">
                  <span className="num mt-px flex size-5 shrink-0 items-center justify-center rounded-full bg-clay-soft text-[11px] font-bold text-clay-ink">{i + 1}</span>
                  <p className="m-0 text-[13px] leading-[1.45]">
                    <Runs runs={n.runs} />
                  </p>
                </div>
              ))}
            </div>
          )}
        </Panel>
      </Grid>
    </Page>
  );
}

function Headline({
  pace,
  budgetTotal,
  isCurrent,
  month,
  today,
  money,
}: {
  pace: ReturnType<typeof project>;
  budgetTotal: number;
  isCurrent: boolean;
  month: ISODate;
  today: ISODate;
  money: (n: number) => string;
}) {
  const when = isCurrent ? fmtLongDay(today) : fmtMonthYear(month);
  if (!isCurrent) {
    return budgetTotal > 0
      ? `${when} · ${money(pace.spent)} spent, ${money(Math.abs(budgetTotal - pace.spent))} ${pace.spent <= budgetTotal ? "under" : "over"} the ${money(budgetTotal)} budget.`
      : `${when} · ${money(pace.spent)} spent on everyday things, bills left out.`;
  }
  if (pace.spent < 0) return `${when} · Refunds have outweighed spending so far, by ${money(-pace.spent)}.`;
  if (budgetTotal > 0) {
    const diff = budgetTotal - pace.projected;
    return `${when} · On pace to finish ${money(Math.abs(diff))} ${diff >= 0 ? "under" : "over"} your ${money(budgetTotal)} budget.`;
  }
  return `${when} · On pace for ${money(pace.projected)} of everyday spending by month's end.`;
}

function PaceChart({
  idx,
  month,
  asOf,
  pace,
  budgetTotal,
  showProjection,
  money,
}: {
  idx: ReturnType<typeof indexDaily>;
  month: ISODate;
  asOf: ISODate;
  pace: ReturnType<typeof project>;
  budgetTotal: number;
  showProjection: boolean;
  money: (n: number) => string;
}) {
  const days = daysInMonth(month);
  const mEnd = monthEnd(month);
  const prevStart = addMonths(month, -1);
  const prevDays = daysInMonth(prevStart);
  const current = cumulative(idx, month, asOf, isPaceCategory);
  const previous = cumulative(idx, prevStart, monthEnd(prevStart), isPaceCategory);
  const projecting = showProjection && pace.remainingDays > 0;
  const spreadEnd = pace.sd * Math.sqrt(pace.remainingDays);
  const top = Math.max(budgetTotal, ...previous, ...current, projecting ? pace.projected + spreadEnd : 0, 100);
  // Refunds can take a running total below zero.
  const bottom = Math.min(0, ...previous, ...current);
  const step = niceCeil((top - bottom) / 4);
  const max = step * Math.ceil((top * 1.04) / step);
  const min = step * Math.floor(bottom / step);
  const s = scale(220, min, max);
  const t = (dayIndex: number, len: number) => (len <= 1 ? 0 : dayIndex / (len - 1));

  const actual: Point[] = current.map((v, i) => [s.x(t(i, days)), s.y(v)]);
  const last: Point[] = previous.map((v, i) => [s.x(t(i, prevDays)), s.y(v)]);
  const k0 = current.length - 1;
  const proj: Point[] = [];
  const up: Point[] = [];
  const lo: Point[] = [];
  if (projecting) {
    for (let k = 0; k <= pace.remainingDays; k++) {
      const v = pace.spent + pace.rate * k;
      const sp = pace.sd * Math.sqrt(k);
      const x = s.x(t(k0 + k, days));
      proj.push([x, s.y(v)]);
      up.push([x, s.y(v + sp)]);
      lo.push([x, s.y(Math.max(min, v - sp))]);
    }
  }
  const todayT = t(k0, days);
  const tickDays = [1, 8, 15, 22];
  const yTicks = Array.from({ length: Math.round((max - min) / step) + 1 }, (_, i) => ({ value: min + i * step, label: fmtAxis(min + i * step) }));

  if (!firstDataDay(idx)) {
    return <Empty title="No spending yet">Once transactions sync, the month’s running total shows up here.</Empty>;
  }

  return (
    <ChartFrame
      scale={s}
      className="mt-2"
      label={`Cumulative spending: ${money(pace.spent)} by ${fmtDay(asOf)}${projecting ? `, projected ${money(pace.projected)} by ${fmtDay(mEnd)}` : ""}${budgetTotal ? ` against a ${money(budgetTotal)} budget` : ""}`}
      yTicks={yTicks}
      xTicks={[
        { t: 0, label: `${fmtMonthShort(month)} 1`, align: "start" },
        ...tickDays.slice(1).map((dd) => ({ t: t(dd - 1, days), label: `${fmtMonthShort(month)} ${dd}` })),
        { t: 1, label: `${fmtMonthShort(month)} ${days}`, align: "end" },
      ]}
      svg={
        <>
          {projecting && <Area d={bandPath(up, lo)} color="var(--color-band)" />}
          {budgetTotal > 0 && <Line d={`M0 ${s.y(budgetTotal).toFixed(1)} H1000`} color="var(--color-ink)" width={1} dashed />}
          {last.length > 1 && <Line d={linePath(last)} color="var(--color-stone)" width={1.75} dashed />}
          {projecting && <Line d={linePath(proj)} color="var(--color-clay)" width={1.75} dashed />}
          {actual.length > 1 && <Line d={linePath(actual)} color="var(--color-clay)" />}
        </>
      }
      overlay={
        <>
          {budgetTotal > 0 && (
            <div className="absolute right-0 bg-sheet pl-1.5 text-[11.5px] font-semibold text-ink" style={{ top: s.y(budgetTotal) - 20 }}>
              Budget {money(budgetTotal)}
            </div>
          )}
          <Dot left={s.pct(todayT)} top={s.y(pace.spent)} color="var(--color-clay)" />
          <div
            className="num absolute text-xs font-semibold whitespace-nowrap text-ink"
            style={{ left: s.pct(todayT), top: s.y(pace.spent), transform: `translate(${todayT > 0.85 ? "-100%" : todayT < 0.1 ? "0" : "-50%"}, -36px)` }}
          >
            {money(pace.spent)} {showProjection ? "today" : `by ${fmtDay(asOf)}`}
          </div>
          {projecting && (
            <div className="num absolute right-0 text-xs font-semibold text-clay-ink" style={{ top: s.y(pace.projected) + 10 }}>
              Projected {money(pace.projected)}
            </div>
          )}
        </>
      }
    />
  );
}

function WhereItWent({ spent, money }: { spent: Map<string, number>; money: (n: number) => string }) {
  const rows = [...spent].filter(([, v]) => v > 0).sort((a, b) => b[1] - a[1]);
  const total = rows.reduce((a, [, v]) => a + v, 0);
  if (total <= 0) return <p className="m-0 text-[13px] text-ink-3">Nothing spent yet this month.</p>;
  const shown = rows.slice(0, 7);
  const rest = rows.slice(7).reduce((a, [, v]) => a + v, 0);
  const list = shown.map(([cat, v]) => ({ key: cat, label: category(cat).label, color: category(cat).color, value: v }));
  if (rest > 0) list.push({ key: "rest", label: "Everything else", color: "var(--color-stone)", value: rest });
  return (
    <>
      <StackedBar segments={list.map((l) => ({ key: l.key, value: l.value, color: l.color, title: l.label }))} height={10} />
      <div className="flex flex-col">
        {list.map((l) => (
          <div key={l.key} className="flex items-center gap-2.5 border-t border-hairline py-[7px] text-[13px] first:border-t-0">
            <Swatch color={l.color} className="size-2 rounded-[2px]" />
            <span className="min-w-0 flex-1 truncate">{l.label}</span>
            <span className="num w-10 text-right text-ink-3">{fmtPct(l.value / total)}</span>
            <span className="num w-[72px] text-right font-semibold">{money(l.value)}</span>
          </div>
        ))}
      </div>
    </>
  );
}

function ComingUp({
  streams,
  accounts,
  priceChanges,
  today,
  money,
}: {
  streams: StreamRow[];
  accounts: AccountRow[];
  priceChanges: Map<string, { from: number; to: number }>;
  today: ISODate;
  money: (n: number, d?: number) => string;
}) {
  const horizon = addDays(today, 30);
  const upcoming = streams
    .flatMap((s) => occurrences(s, addDays(today, 1), horizon).map((day) => ({ day, s })))
    .sort((a, b) => (a.day < b.day ? -1 : a.day > b.day ? 1 : 0))
    .slice(0, 6);
  if (upcoming.length === 0) {
    return (
      <Empty title="Nothing due in the next 30 days" action={<Button variant="outline" size="sm" asChild><Link to="/recurring">Add a recurring payment</Link></Button>}>
        Bills and paycheques appear here once a few months of history show a pattern, or when you add them by hand.
      </Empty>
    );
  }
  const chequing = accounts.filter((a) => accountClass(a.type, a.subtype) === "chequing");
  const chequingIds = new Set(chequing.map((a) => a.account_id));
  const lastDay = upcoming[upcoming.length - 1].day;
  const points = projectBalance(
    chequing.reduce((a, c) => a + num(c.current_balance), 0),
    streamFlows(streams, chequingIds, addDays(today, 1), lastDay),
    { mean: 0, sd: 0 },
    today,
    diffDays(today, lastDay),
  );
  const lowest = Math.min(...points.map((p) => p.balance));
  const cashAccounts = accounts.filter((a) => isCash(accountClass(a.type, a.subtype)));

  return (
    <>
      <div className="flex flex-col">
        {upcoming.map(({ day, s }) => {
          const inflow = s.direction === "inflow";
          const account = s.account_id ? shortAccount(s.account_name, s.institution_name, s.account_mask) : "No account chosen";
          const change = priceChanges.get(s.stream_id);
          const sub = inflow
            ? `Into ${account}`
            : change
              ? `${account} · price ${change.to > change.from ? "up" : "down"} ${money(Math.abs(change.to - change.from), 2)}`
              : `${varies(s) && isBill(s.category) ? "Estimated · " : ""}${account}`;
          const amount = num(s.last_amount ?? s.average_amount);
          return (
            <div key={`${s.stream_id}-${day}`} className="flex items-center gap-3 border-t border-hairline py-2">
              <div className="flex w-8 shrink-0 flex-col items-center rounded-[3px] border border-line py-0.5">
                <span className="num text-[13px] leading-tight font-semibold">{dayOfMonth(day)}</span>
                <span className="text-[9.5px] leading-tight font-semibold tracking-[0.06em] text-ink-3 uppercase">{fmtMonthShort(day)}</span>
              </div>
              <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="truncate text-[13px] font-semibold">{streamName(s)}</span>
                <span className="truncate text-xs text-ink-3">{sub}</span>
              </div>
              <span className={`num text-[13px] font-semibold ${inflow ? "text-moss" : ""}`}>{fmtSigned(-amount, 2)}</span>
            </div>
          );
        })}
      </div>
      {chequing.length > 0 && cashAccounts.length > 0 && (
        <p className="mt-auto mb-0 text-[12.5px] text-ink-3">
          Chequing stays above <strong className="text-ink">{money(lowest)}</strong> through {fmtDay(lastDay)}.
        </p>
      )}
    </>
  );
}

function Runs({ runs }: { runs: Run[] }) {
  return (
    <>
      {runs.map((r, i) =>
        typeof r === "string" ? (
          <span key={i}>{r}</span>
        ) : "strong" in r ? (
          <strong key={i}>{r.strong}</strong>
        ) : (
          <Link key={i} to={r.to} className="font-semibold text-clay-ink hover:text-clay">
            {r.link}
          </Link>
        ),
      )}
    </>
  );
}
