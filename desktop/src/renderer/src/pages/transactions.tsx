import { useSearchParams } from "react-router";

import { CategoryPicker } from "@/components/category-picker";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { Page, PageIntro } from "@/components/page-intro";
import { Chip, Panel } from "@/components/panel";
import { Button } from "@/components/ui/button";
import { useDataVersion } from "@/hooks/use-data-version";
import { useLoad } from "@/hooks/use-load";
import { fmtDay, todayISO } from "@/lib/dates";
import { shortAccount } from "@/lib/insights/accounts";
import { fmtSigned, num } from "@/lib/money";
import { listSafe, topper, type Query } from "@/lib/topper";
import type { CategorizedRow } from "@/lib/topper-types";
import { cn } from "@/lib/utils";

import { TransactionFilters, type FilterValues } from "./transactions-filters";

const PAGE_SIZE = 50;
const DATE = /^\d{4}-\d{2}-\d{2}$/;
const AMOUNT = /^\$?\s*(\d+(?:\.\d{1,2})?)$/;

const COLUMNS: (keyof CategorizedRow)[] = [
  "transaction_id",
  "date",
  "name",
  "merchant_name",
  "merchant_key",
  "amount",
  "iso_currency_code",
  "pending",
  "category",
  "category_source",
  "needs_category",
  "account_id",
  "account_name",
  "account_mask",
  "institution_name",
];

// Filters live in the hash's query string, so back/forward and reloads keep
// them; the header search arrives as ?q=.
export function TransactionsPage() {
  const [params, setParams] = useSearchParams();
  const { version } = useDataVersion();
  const values: FilterValues = {
    from: params.get("from")?.trim() ?? "",
    to: params.get("to")?.trim() ?? "",
    account: params.get("account")?.trim() ?? "",
    q: params.get("q")?.trim() ?? "",
    pending: params.get("pending")?.trim() ?? "",
    category: params.get("category")?.trim() ?? "",
  };
  const page = Math.max(1, Number.parseInt(params.get("page") ?? "", 10) || 1);
  const q = listSafe(values.q).replace(/[%_]/g, "");

  const query: Query = {
    select: COLUMNS,
    limit: PAGE_SIZE,
    offset: (page - 1) * PAGE_SIZE,
    count: true,
    filters: {},
    or: [],
  };
  const dateConds: string[] = [];
  if (DATE.test(values.from)) dateConds.push(`gte.${values.from}`);
  if (DATE.test(values.to)) dateConds.push(`lte.${values.to}`);
  if (dateConds.length) query.filters!.date = dateConds;
  if (values.account) query.filters!.account_id = `eq.${values.account}`;
  if (values.pending === "yes" || values.pending === "no") query.filters!.pending = `eq.${values.pending === "yes"}`;
  if (values.category === "needs") query.filters!.needs_category = "eq.true";
  else if (values.category) query.filters!.category = `eq.${values.category}`;
  if (q) {
    const amount = AMOUNT.exec(q);
    query.or!.push(
      [`name.ilike.%${q}%`, `merchant_name.ilike.%${q}%`, ...(amount ? [`amount.eq.${amount[1]}`, `amount.eq.-${amount[1]}`] : [])].join(","),
    );
  }

  const key = params.toString();
  const [txns] = useLoad(() => topper.categorized(query), [key, version]);
  const [accounts] = useLoad(
    () => topper.accounts({ limit: 1000, toggles: { include_missing: 1 }, select: ["account_id", "name", "mask", "institution_name"] }),
    [],
  );

  const navigate = (next: FilterValues, p = 1) => {
    const out = new URLSearchParams();
    for (const [k, v] of Object.entries(next)) if (v) out.set(k, v);
    if (p > 1) out.set("page", String(p));
    setParams(out);
  };

  if (txns.ok === "loading" || accounts.ok === "loading") return <Loading />;

  const accountOptions = accounts.ok
    ? accounts.data.data.map((a) => ({
        id: a.account_id,
        label: `${a.institution_name ? `${a.institution_name} · ` : ""}${a.name}${a.mask ? ` ••${a.mask}` : ""}`,
      }))
    : [];

  const total = txns.ok ? (txns.data.total ?? txns.data.count) : 0;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const today = todayISO();
  const filtered = Object.values(values).some(Boolean);

  return (
    <Page>
      <PageIntro eyebrow="Transactions">
        {!txns.ok ? (
          "Transactions"
        ) : filtered ? (
          <>
            <em>{total.toLocaleString("en-CA")}</em> transaction{total === 1 ? "" : "s"} match{total === 1 ? "es" : ""}
            {values.q ? ` “${values.q}”` : " these filters"}.
          </>
        ) : (
          <>
            Every transaction, <em>{total.toLocaleString("en-CA")}</em> of them.
          </>
        )}
      </PageIntro>
      <TransactionFilters key={key} accounts={accountOptions} values={values} onApply={(next) => navigate(next)} />
      {!txns.ok ? (
        <LoadError what="transactions" message={txns.error} />
      ) : (
        <Panel className="gap-0 px-6 py-4">
          <div className="grid grid-cols-[92px_minmax(0,2fr)_minmax(0,1.1fr)_minmax(0,1.2fr)_120px] gap-4 border-b border-line px-2 pb-2">
            <span className="eyebrow">Date</span>
            <span className="eyebrow">Description</span>
            <span className="eyebrow">Category</span>
            <span className="eyebrow">Account</span>
            <span className="eyebrow text-right">Amount</span>
          </div>
          {txns.data.data.map((t) => (
            <div
              key={t.transaction_id}
              className="grid min-h-[52px] grid-cols-[92px_minmax(0,2fr)_minmax(0,1.1fr)_minmax(0,1.2fr)_120px] items-center gap-4 border-t border-hairline px-2 py-1.5 text-[13.5px] first:border-t-0"
            >
              <span className="num text-ink-3">{t.date === today ? "Today" : fmtDay(t.date)}{t.date.slice(0, 4) !== today.slice(0, 4) ? `, ${t.date.slice(0, 4)}` : ""}</span>
              <span className="flex min-w-0 flex-col gap-0.5">
                <span className="truncate font-semibold">{t.merchant_name ?? t.name}</span>
                <span className="flex min-w-0 items-center gap-2">
                  {t.merchant_name && t.merchant_name !== t.name && <span className="truncate font-mono text-[11.5px] text-ink-3">{t.name}</span>}
                  {t.pending && <Chip className="bg-track text-ink-3">Pending</Chip>}
                </span>
              </span>
              <span className="min-w-0">
                <CategoryPicker txn={t} />
              </span>
              <span className="truncate text-ink-2">{shortAccount(t.account_name, t.institution_name, t.account_mask)}</span>
              <span className={cn("num text-right font-semibold", num(t.amount) < 0 && "text-moss")}>{fmtSigned(-num(t.amount), 2, t.iso_currency_code ?? "CAD")}</span>
            </div>
          ))}
          {txns.data.data.length === 0 && <p className="px-2 py-10 text-center text-[13px] text-ink-3">No transactions match these filters.</p>}
        </Panel>
      )}
      {txns.ok && pages > 1 && (
        <div className="flex items-center justify-between text-[13px]">
          <span className="text-ink-3">
            Page {page} of {pages}
          </span>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => navigate(values, page - 1)}>
              Previous
            </Button>
            <Button variant="outline" size="sm" disabled={page >= pages} onClick={() => navigate(values, page + 1)}>
              Next
            </Button>
          </div>
        </div>
      )}
    </Page>
  );
}
