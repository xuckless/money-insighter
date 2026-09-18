import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { toast } from "sonner";

import { Area, bandPath, ChartFrame, Dot, Line, linePath, scale, type Point } from "@/components/charts/chart";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { Grid, Page, PageHeader } from "@/components/page-header";
import { Empty, Legend, Panel, PanelHeader, Stat } from "@/components/panel";
import { Segmented } from "@/components/segmented";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { useSettings } from "@/hooks/use-app-state";
import { useDataVersion } from "@/hooks/use-data-version";
import { useLoad } from "@/hooks/use-load";
import { addDays, addMonths, diffDays, fmtDay, fmtLongDay, fmtMonthShort, monthStart, todayISO, type ISODate } from "@/lib/dates";
import { accountClass, currencyOf, isCash, primaryCurrency } from "@/lib/insights/accounts";
import {
  everydaySpending,
  inAndOut,
  lowPoint,
  pastBalances,
  projectBalance,
  safeToSpend,
  streamFlows,
  type Flow,
  type FlowKind,
} from "@/lib/insights/cashflow";
import { indexMonthly } from "@/lib/insights/spending";
import { fmt, fmtAxis, fmtSigned, niceCeil, num } from "@/lib/money";
import { loadActiveStreams, loadPreferences, savePreference } from "@/lib/queries";
import { topper } from "@/lib/topper";
import type { AccountRow, StreamRow } from "@/lib/topper-types";
import { cn } from "@/lib/utils";

const PAST_DAYS = 60;
const horizons = [30, 60, 90] as const;
type Horizon = (typeof horizons)[number];

const eventColor: Record<FlowKind, string | null> = {
  pay: "var(--color-moss)",
  payment: "var(--color-clay)",
  move: "#8C8174",
  bill: null,
};

export function CashFlowPage() {
  const [params, setParams] = useSearchParams();
  const horizon: Horizon = horizons.find((h) => String(h) === params.get("days")) ?? 90;
  const [settings] = useSettings();
  const { version } = useDataVersion();
  const today = todayISO();
  const recurringOn = settings?.recurringEnabled ?? false;

  const [data] = useLoad(async () => {
    if (settings === null) return null;
    const thisMonth = monthStart(today);
    const [accounts, txns, monthly, streams, prefs] = await Promise.all([
      topper.accounts({ limit: 1000 }),
      topper.categorizedAll({
        filters: { date: [`gte.${addDays(today, -PAST_DAYS)}`], account_type: "eq.depository" },
        select: ["date", "amount", "pending", "category", "merchant_key", "account_id", "iso_currency_code"],
      }),
      topper.categoriesMonthly({ filters: { month: [`gte.${addMonths(thisMonth, -5)}`] } }),
      loadActiveStreams(today, recurringOn),
      loadPreferences(),
    ]);
    return { accounts: accounts.data, txns, monthly, streams, cushion: Number(prefs.cushion) || 0 };
  }, [version, recurringOn, settings === null]);

  if (data.ok === "loading" || (data.ok === true && data.data === null)) return <Loading />;
  if (!data.ok) return <LoadError what="cash flow" message={data.error} />;
  return <CashFlowView {...data.data!} today={today} horizon={horizon} onHorizon={(h) => setParams(h === 90 ? {} : { days: String(h) })} />;
}

function CashFlowView({
  accounts: allAccounts,
  txns: allTxns,
  monthly,
  streams,
  cushion,
  today,
  horizon,
  onHorizon,
}: {
  accounts: AccountRow[];
  txns: Awaited<ReturnType<typeof topper.categorizedAll>>;
  monthly: Awaited<ReturnType<typeof topper.categoriesMonthly>>;
  streams: StreamRow[];
  cushion: number;
  today: ISODate;
  horizon: Horizon;
  onHorizon: (h: Horizon) => void;
}) {
  const currency = primaryCurrency(allAccounts);
  const money = (n: number, d = 0) => fmt(n, d, currency);
  const [cushionOpen, setCushionOpen] = useState(false);

  const cash = allAccounts.filter((a) => currencyOf(a) === currency && isCash(accountClass(a.type, a.subtype)));
  const cashIds = new Set(cash.map((a) => a.account_id));
  const chequing = cash.filter((a) => accountClass(a.type, a.subtype) === "chequing");
  const chequingIds = new Set(chequing.map((a) => a.account_id));
  const current = cash.reduce((a, c) => a + num(c.current_balance), 0);
  const txns = allTxns.filter((t) => cashIds.has(t.account_id));

  const past = pastBalances(current, txns, addDays(today, -PAST_DAYS), today);
  const flows = streamFlows(streams, cashIds, addDays(today, 1), addDays(today, horizon));
  const streamKeys = new Set(streams.filter((s) => cashIds.has(s.account_id) && s.merchant_key).map((s) => s.merchant_key));
  const rate = everydaySpending(txns, addDays(today, -PAST_DAYS), addDays(today, -1), streamKeys);
  const projected = cash.length > 0 ? projectBalance(current, flows, rate, today, horizon) : [];
  const low = lowPoint(projected);
  const end = projected.at(-1);
  const hasStreams = streams.some((s) => cashIds.has(s.account_id));

  const chequingFlows = flows.filter((f) => chequingIds.has(f.stream.account_id));
  const safe = safeToSpend(
    chequing.reduce((a, c) => a + num(c.current_balance), 0),
    chequingFlows,
    today,
    cushion,
  );

  const months = Array.from({ length: 6 }, (_, i) => addMonths(monthStart(today), i - 5));
  const inOut = inAndOut(indexMonthly(monthly, currency), months);

  return (
    <Page>
      <PageHeader
        title="Cash flow"
        subtitle={
          cash.length === 0
            ? "No chequing or savings accounts connected yet."
            : end
              ? `${money(current)} in cash today · at this pace ${money(end.balance)} by ${fmtDay(end.day)}`
              : `${money(current)} in cash today`
        }
        actions={<Segmented label="Projection horizon" value={horizon} options={horizons.map((h) => ({ value: h, label: `${h} days` }))} onChange={onHorizon} />}
      />

      <Grid>
        <Stat className="col-span-6 xl:col-span-3" label="Cash today" value={money(current)}>
          Chequing {money(chequing.reduce((a, c) => a + num(c.current_balance), 0))} · Savings {money(current - chequing.reduce((a, c) => a + num(c.current_balance), 0))}
        </Stat>
        <Stat className="col-span-6 xl:col-span-3" label={`In ${horizon} days`} value={end ? money(end.balance) : "—"} tone={end && end.balance < current ? "bad" : end ? "good" : undefined}>
          {end ? `${fmtSigned(end.balance - current, 0, currency)} from today, ${hasStreams ? "bills and pay included" : "everyday spending only"}` : "Connect a cash account"}
        </Stat>
        <Stat className="col-span-6 xl:col-span-3" label="Low point" value={low ? money(low.balance) : "—"} tone={low && low.balance < cushion ? "bad" : undefined}>
          {low ? `${fmtDay(low.day)}${low.balance < cushion ? ` · below your ${money(cushion)} cushion` : ""}` : "Nothing projected yet"}
        </Stat>
        <Stat className="col-span-6 xl:col-span-3" label="Safe to spend" value={chequing.length ? money(safe.safe) : "—"} tone={chequing.length && safe.safe < 0 ? "bad" : undefined}>
          {chequing.length ? (safe.payday ? `Until payday on ${fmtDay(safe.payday.day)}` : `Over the next two weeks`) : "Needs a chequing account"}
        </Stat>
      </Grid>

      <Grid>
        <Panel className="col-span-12 xl:col-span-8">
          <PanelHeader
            title="Cash balance"
            description="Chequing and savings. The last 60 days, then projected from paycheques, bills, card payments and your everyday spending."
          >
            <Legend
              items={[
                { label: "Actual", kind: "line", color: "var(--color-ink)" },
                { label: "Projected", kind: "dash", color: "var(--color-clay)" },
                { label: "Likely range", kind: "band", color: "var(--color-band)" },
              ]}
            />
          </PanelHeader>
          {cash.length === 0 ? (
            <Empty title="Nothing to chart yet">Connect a bank account to see its balance here.</Empty>
          ) : (
            <BalanceChart past={past} projected={projected} flows={flows} low={low} today={today} horizon={horizon} money={money} />
          )}
          <Legend
            items={[
              { label: "Paycheque", kind: "dot", color: "var(--color-moss)" },
              { label: "Rent, loan or card payment", kind: "dot", color: "var(--color-clay)" },
              { label: "Transfer", kind: "dot", color: "#8C8174" },
            ]}
          />
        </Panel>

        <Panel className="col-span-12 xl:col-span-4">
          <PanelHeader
            title="Safe to spend"
            description={safe.payday ? `Until your next paycheque on ${fmtLongDay(safe.payday.day)}` : `Over the next two weeks, through ${fmtDay(safe.until)}`}
          />
          {chequing.length === 0 ? (
            <Empty title="No chequing account">Connect the account your pay goes into.</Empty>
          ) : (
            <>
              <div className="flex flex-col gap-1.5">
                <div className={cn("figure text-[40px]", safe.safe < 0 && "text-clay")}>{money(safe.safe)}</div>
                <div className="text-[13px] text-ink-2">
                  {safe.safe > 0 ? (
                    <>
                      About <strong>{money(safe.perDay)} a day</strong> for the next {safe.days} day{safe.days === 1 ? "" : "s"}
                    </>
                  ) : (
                    "Bills due before payday are more than your chequing balance allows."
                  )}
                </div>
              </div>
              <div className="num flex flex-col text-[13px]">
                <Row label="Chequing balance" value={money(safe.balance, 2)} />
                {safe.outflows.slice(0, 4).map((f) => (
                  <Row key={`${f.stream.stream_id}-${f.day}`} label={`${f.name} · ${fmtDay(f.day)}`} value={fmt(f.amount, 2, currency)} />
                ))}
                {safe.outflows.length > 4 && (
                  <Row label={`${safe.outflows.length - 4} more bills`} value={fmt(safe.outflows.slice(4).reduce((a, f) => a + f.amount, 0), 2, currency)} />
                )}
                <Row label="Cushion you keep" value={fmt(-cushion, 2, currency)} />
                <div className="flex justify-between border-t border-ink py-2 font-semibold">
                  <span>Safe to spend</span>
                  <span>{money(safe.safe, 2)}</span>
                </div>
              </div>
              <p className="m-0 text-[12.5px] leading-[1.45] text-ink-3">
                Savings aren’t counted. New card purchases come off this number when the card is paid.
                {!hasStreams && (
                  <>
                    {" "}
                    No bills are known yet: <Link to="/recurring" className="font-semibold text-clay-ink">add them</Link> to make this exact.
                  </>
                )}
              </p>
              <Button variant="outline" size="sm" className="mt-auto self-start" onClick={() => setCushionOpen(true)}>
                Change cushion
              </Button>
            </>
          )}
        </Panel>
      </Grid>

      <Grid>
        <Panel className="col-span-12 xl:col-span-6">
          <PanelHeader title="In and out" description="Take-home pay against everything spent, rent included">
            <Legend
              items={[
                { label: "Income", kind: "square", color: "var(--color-moss)" },
                { label: "Spending", kind: "square", color: "#C06A45" },
              ]}
            />
          </PanelHeader>
          <InOutBars data={inOut} money={money} />
        </Panel>

        <Panel className="col-span-12 gap-3 xl:col-span-6">
          <PanelHeader title="Next 30 days" description="What lands on your cash accounts, and the balance after each">
            <span className="eyebrow pt-1">Cash after</span>
          </PanelHeader>
          <NextThirty flows={flows.filter((f) => f.day <= addDays(today, 30))} projected={projected} money={money} currency={currency} />
        </Panel>
      </Grid>

      <CushionDialog open={cushionOpen} onOpenChange={setCushionOpen} cushion={cushion} currency={currency} />
    </Page>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-3 border-t border-hairline py-2">
      <span className="min-w-0 truncate">{label}</span>
      <span className="shrink-0">{value}</span>
    </div>
  );
}

function BalanceChart({
  past,
  projected,
  flows,
  low,
  today,
  horizon,
  money,
}: {
  past: { day: ISODate; balance: number }[];
  projected: { day: ISODate; balance: number; low: number; high: number }[];
  flows: Flow[];
  low: { day: ISODate; balance: number } | null;
  today: ISODate;
  horizon: number;
  money: (n: number) => string;
}) {
  const start = addDays(today, -PAST_DAYS);
  const span = PAST_DAYS + horizon;
  const values = [...past.map((p) => p.balance), ...projected.flatMap((p) => [p.low, p.high])];
  // Zero is on the axis only when the balance comes near it.
  const lowest = Math.min(...values);
  const highest = Math.max(...values, 1);
  const pad = Math.max((highest - lowest) * 0.1, Math.abs(highest) * 0.02, 50);
  const rawMin = lowest - pad < 0 || lowest < highest - lowest ? Math.min(0, lowest - pad) : lowest - pad;
  const rawMax = highest + pad;
  const step = niceCeil((rawMax - rawMin) / 4);
  const min = Math.floor(rawMin / step) * step;
  const max = Math.ceil(rawMax / step) * step;
  const s = scale(250, min, max);
  const t = (day: ISODate) => diffDays(start, day) / span;

  const pastPts: Point[] = past.map((p) => [s.x(t(p.day)), s.y(p.balance)]);
  const projPts: Point[] = projected.map((p) => [s.x(t(p.day)), s.y(p.balance)]);
  const up: Point[] = projected.map((p) => [s.x(t(p.day)), s.y(p.high)]);
  const lo: Point[] = projected.map((p) => [s.x(t(p.day)), s.y(p.low)]);
  const balanceOn = new Map(projected.map((p) => [p.day, p.balance]));

  const yTicks = [];
  for (let v = min; v <= max + step / 2; v += step) yTicks.push({ value: v, label: fmtAxis(v) });
  const xTicks = [];
  for (let d = addDays(start, 4); d <= addDays(today, horizon - 4); d = addDays(d, 1)) {
    if (d.endsWith("-01")) xTicks.push({ t: t(d), label: `${fmtMonthShort(d)} 1` });
  }

  return (
    <ChartFrame
      scale={s}
      gutter={48}
      className="mt-5"
      label={`Cash balance over the last ${PAST_DAYS} days and projected ${horizon} days ahead`}
      yTicks={yTicks}
      xTicks={xTicks}
      svg={
        <>
          {projPts.length > 1 && <Area d={bandPath(up, lo)} color="var(--color-band)" />}
          {min < 0 && <Line d={`M0 ${s.y(0).toFixed(1)} H1000`} color="var(--color-stone)" width={1} />}
          <Line d={linePath(pastPts)} color="var(--color-ink)" width={2} />
          {projPts.length > 1 && <Line d={linePath(projPts)} color="var(--color-clay)" width={2} dashed />}
        </>
      }
      overlay={
        <>
          <div className="absolute -top-1.5 bottom-0 border-l border-dashed border-stone" style={{ left: s.pct(t(today)) }} />
          <div className="absolute -top-6 -translate-x-1/2 text-[11px] font-semibold text-ink" style={{ left: s.pct(t(today)) }}>
            Today
          </div>
          {flows
            .filter((f) => eventColor[f.kind] && Math.abs(f.amount) >= 100)
            .map((f) => (
              <Dot
                key={`${f.stream.stream_id}-${f.day}`}
                left={s.pct(t(f.day))}
                top={s.y(balanceOn.get(f.day) ?? 0)}
                size={9}
                color={eventColor[f.kind]!}
                title={`${fmtDay(f.day)} · ${f.name} ${fmtSigned(f.amount)}`}
              />
            ))}
          {low && (
            <>
              <Dot left={s.pct(t(low.day))} top={s.y(low.balance)} size={13} color="var(--color-clay)" hollow />
              <div
                className="num absolute rounded-[3px] bg-sheet px-1 py-px text-[11.5px] whitespace-nowrap text-ink"
                style={{ left: s.pct(t(low.day)), top: s.y(low.balance), transform: `translate(${t(low.day) > 0.85 ? "-100%" : "-50%"}, 14px)` }}
              >
                <strong>Low point</strong> {fmtDay(low.day)} · {money(low.balance)}
              </div>
            </>
          )}
        </>
      }
    />
  );
}

function InOutBars({ data, money }: { data: { month: ISODate; income: number; spending: number }[]; money: (n: number) => string }) {
  const max = Math.max(1, ...data.flatMap((d) => [d.income, d.spending]));
  if (data.every((d) => d.income === 0 && d.spending === 0)) {
    return <Empty title="No history yet">Income and spending appear here as transactions sync.</Empty>;
  }
  return (
    <>
      <div className="flex h-[190px] items-end gap-4 border-b border-line px-1">
        {data.map((d, i) => {
          const current = i === data.length - 1;
          const net = d.income - d.spending;
          return (
            <div key={d.month} className="flex flex-1 basis-0 flex-col items-center gap-1.5">
              <span className={cn("num text-[11.5px] font-semibold", Math.round(net) > 0 ? "text-moss" : Math.round(net) < 0 ? "text-clay" : "text-ink-3")}>
                {Math.round(net) > 0 ? "+" : ""}
                {money(net)}
              </span>
              <div className="flex w-full items-end gap-[3px]">
                <div
                  className={cn("flex-1 rounded-t-[2px]", current && "hatched")}
                  style={{ height: Math.round((d.income / max) * 150), background: current ? undefined : "var(--color-moss)", ["--hatch" as string]: "var(--color-moss)" }}
                  title={`Income ${money(d.income)}`}
                />
                <div
                  className={cn("flex-1 rounded-t-[2px]", current && "hatched")}
                  style={{ height: Math.round((d.spending / max) * 150), background: current ? undefined : "#C06A45", ["--hatch" as string]: "#C06A45" }}
                  title={`Spending ${money(d.spending)}`}
                />
              </div>
            </div>
          );
        })}
      </div>
      <div className="-mt-2 flex gap-4 px-1">
        {data.map((d, i) => (
          <span key={d.month} className="flex-1 basis-0 text-center text-[11.5px] text-ink-3">
            {i === data.length - 1 ? `${fmtMonthShort(d.month)} (so far)` : fmtMonthShort(d.month)}
          </span>
        ))}
      </div>
    </>
  );
}

function NextThirty({
  flows,
  projected,
  money,
  currency,
}: {
  flows: Flow[];
  projected: { day: ISODate; balance: number }[];
  money: (n: number) => string;
  currency: string;
}) {
  if (flows.length === 0) {
    return (
      <Empty title="Nothing recurring lands on your cash accounts in the next 30 days">
        Streams are found from a few months of history; anything else can be{" "}
        <Link to="/recurring" className="font-semibold text-clay-ink">
          added by hand
        </Link>
        .
      </Empty>
    );
  }
  const balanceOn = new Map(projected.map((p) => [p.day, p.balance]));
  return (
    <div className="flex flex-col">
      {flows.map((f) => (
        <div key={`${f.stream.stream_id}-${f.day}`} className="num grid grid-cols-[64px_minmax(0,1fr)_100px_96px] items-center gap-3 border-t border-hairline py-2 text-[13px]">
          <span className="text-ink-3">{fmtDay(f.day)}</span>
          <span className="truncate font-medium">{f.name}</span>
          <span className={cn("text-right font-semibold", f.amount > 0 && "text-moss")}>{fmtSigned(f.amount, 2, currency)}</span>
          <span className="text-right text-ink-2">{money(balanceOn.get(f.day) ?? 0)}</span>
        </div>
      ))}
    </div>
  );
}

function CushionDialog({ open, onOpenChange, cushion, currency }: { open: boolean; onOpenChange: (o: boolean) => void; cushion: number; currency: string }) {
  const { bump } = useDataVersion();
  const [value, setValue] = useState(String(cushion));
  const [saving, setSaving] = useState(false);
  const n = Number(value);
  const valid = value.trim() !== "" && Number.isFinite(n) && n >= 0;

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (o) setValue(String(cushion));
        onOpenChange(o);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Cushion</DialogTitle>
          <DialogDescription>The amount you always want left in chequing. It comes off Safe to spend. Currently {fmt(cushion, 0, currency)}.</DialogDescription>
        </DialogHeader>
        <Input inputMode="decimal" className="num" value={value} onChange={(e) => setValue(e.target.value.replace(/[^0-9.]/g, ""))} autoFocus />
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            disabled={!valid || saving}
            onClick={async () => {
              setSaving(true);
              try {
                await savePreference("cushion", n);
                bump();
                onOpenChange(false);
              } catch (err) {
                toast.error("Could not save the cushion", { description: (err as Error).message });
              } finally {
                setSaving(false);
              }
            }}
          >
            {saving ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
