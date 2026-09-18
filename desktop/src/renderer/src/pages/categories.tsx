import { useState } from "react";

import { CategoryDialog, DeleteCategoryDialog, KindSwitch } from "@/components/category-dialog";
import { CategoryIcon } from "@/components/category-icon";
import { PencilIcon, PlusIcon, TrashIcon } from "@/components/icons";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { Grid, Page, PageHeader } from "@/components/page-header";
import { Chip, ListHeader, Panel, PanelHeader, Stat } from "@/components/panel";
import { Button } from "@/components/ui/button";
import { useCategories } from "@/hooks/use-categories";
import { useDataVersion } from "@/hooks/use-data-version";
import { useLoad } from "@/hooks/use-load";
import { addMonths, fmtMonth, monthStart, todayISO } from "@/lib/dates";
import { primaryCurrency } from "@/lib/insights/accounts";
import { indexMonthly } from "@/lib/insights/spending";
import { fmt } from "@/lib/money";
import { loadBudgets } from "@/lib/queries";
import { topper } from "@/lib/topper";

import { kindLabel, kindOrder, type Category, type CategoryKind } from "@shared/categories";

// CategoriesPage lists every category with what it holds this month and
// lets the user add, edit and remove them.
export function CategoriesPage() {
  const { list } = useCategories();
  const { version } = useDataVersion();
  const today = todayISO();
  const thisMonth = monthStart(today);
  const [kind, setKind] = useState<CategoryKind | "all">("all");
  const [editing, setEditing] = useState<{ open: boolean; category: Category | null }>({ open: false, category: null });
  const [removing, setRemoving] = useState<Category | null>(null);

  const [data] = useLoad(async () => {
    const [accounts, monthly, budgets, overrides, rules, entries] = await Promise.all([
      topper.accounts({ limit: 1000, select: ["account_id", "iso_currency_code", "unofficial_currency_code"] }),
      topper.categoriesMonthly({ filters: { month: [`gte.${addMonths(thisMonth, -3)}`] } }),
      loadBudgets(),
      topper.categoryOverrides(),
      topper.merchantRules(),
      topper.recurringEntries(),
    ]);
    return { accounts: accounts.data, monthly, budgets, overrides, rules, entries };
  }, [version]);

  if (data.ok === "loading") return <Loading />;
  if (!data.ok) return <LoadError what="categories" message={data.error} />;

  const d = data.data;
  const currency = primaryCurrency(d.accounts);
  const money = (n: number) => fmt(n, 0, currency);
  const monthly = indexMonthly(d.monthly, currency);
  const thisMonthTotals = monthly.get(thisMonth) ?? new Map<string, number>();
  const count = (rows: { category: string }[]) => {
    const m = new Map<string, number>();
    for (const r of rows) m.set(r.category, (m.get(r.category) ?? 0) + 1);
    return m;
  };
  const overrides = count(d.overrides);
  const rules = count(d.rules);
  const entries = count(d.entries);
  const usage = (c: Category) => ({
    overrides: overrides.get(c.id) ?? 0,
    rules: rules.get(c.id) ?? 0,
    entries: entries.get(c.id) ?? 0,
    budget: d.budgets.has(c.id),
  });
  const custom = list.filter((c) => !c.builtin).length;
  const budgeted = list.filter((c) => d.budgets.has(c.id)).length;
  const cols = "grid-cols-[minmax(0,2fr)_0.9fr_0.9fr_0.9fr_1fr_72px]";

  return (
    <Page>
      <PageHeader
        title="Categories"
        subtitle="What every transaction is filed under. Sorting a merchant once teaches a rule; a category you remove merges into another."
        actions={
          <>
            <KindSwitch value={kind} onChange={setKind} />
            <Button size="sm" onClick={() => setEditing({ open: true, category: null })}>
              <PlusIcon size={15} /> New category
            </Button>
          </>
        }
      />

      <Grid>
        <Stat className="col-span-6 xl:col-span-3" label="Categories" value={String(list.length)}>
          {custom ? `${custom} of your own` : "All built in so far"}
        </Stat>
        <Stat className="col-span-6 xl:col-span-3" label="Merchant rules" value={String(d.rules.length)}>
          Merchants you sorted once and for all
        </Stat>
        <Stat className="col-span-6 xl:col-span-3" label="Sorted by hand" value={String(d.overrides.length)}>
          Single transactions you moved
        </Stat>
        <Stat className="col-span-6 xl:col-span-3" label="With a budget" value={String(budgeted)}>
          Set on the Spending page
        </Stat>
      </Grid>

      {kindOrder
        .filter((k) => kind === "all" || kind === k)
        .map((k) => {
          const rows = list.filter((c) => c.kind === k);
          if (rows.length === 0) return null;
          return (
            <Panel key={k} className="gap-3">
              <PanelHeader
                title={kindLabel[k]}
                description={
                  k === "spending"
                    ? "Everyday money out. Paced day by day and budgeted."
                    : k === "bill"
                      ? "Fixed costs that land on a schedule. Budgeted, left out of the daily pace."
                      : k === "income"
                        ? "Money in."
                        : "Money moved between your own accounts. Never counted as spending."
                }
              />
              <ListHeader cols={cols}>
                <span className="eyebrow">Category</span>
                <span className="eyebrow text-right">{fmtMonth(thisMonth)}</span>
                <span className="eyebrow text-right">Budget</span>
                <span className="eyebrow text-right">Rules</span>
                <span className="eyebrow">Icon</span>
                <span />
              </ListHeader>
              <div className="-mt-3 flex flex-col">
                {rows.map((c) => {
                  const u = usage(c);
                  const amount = thisMonthTotals.get(c.id) ?? 0;
                  return (
                    <div key={c.id} className={`grid min-h-12 items-center gap-4 border-b border-hairline px-2 py-1.5 text-[13px] last:border-b-0 ${cols}`}>
                      <span className="flex min-w-0 items-center gap-2.5">
                        <CategoryIcon category={c} size={28} />
                        <span className="truncate font-semibold">{c.label}</span>
                        {!c.builtin && <Chip className="bg-track text-ink-3">Custom</Chip>}
                      </span>
                      <span className="num text-right">{amount !== 0 ? money(k === "income" ? -amount : amount) : <span className="text-ink-3">—</span>}</span>
                      <span className="num text-right">{d.budgets.has(c.id) ? money(d.budgets.get(c.id)!) : <span className="text-ink-3">—</span>}</span>
                      <span className="num text-right text-ink-2">{u.rules || u.overrides ? `${u.rules}${u.overrides ? ` + ${u.overrides}` : ""}` : <span className="text-ink-3">—</span>}</span>
                      <span className="font-mono text-[11.5px] text-ink-3">{c.icon}</span>
                      <span className="flex justify-end gap-0.5">
                        <button
                          type="button"
                          aria-label={`Edit ${c.label}`}
                          onClick={() => setEditing({ open: true, category: c })}
                          className="flex size-7 cursor-pointer items-center justify-center rounded-[3px] border-0 bg-transparent text-ink-3 hover:bg-row-hover hover:text-ink"
                        >
                          <PencilIcon size={15} />
                        </button>
                        <button
                          type="button"
                          aria-label={`Remove ${c.label}`}
                          disabled={c.id === "other" || c.id === "transfer" || c.id === "income"}
                          onClick={() => setRemoving(c)}
                          className="flex size-7 cursor-pointer items-center justify-center rounded-[3px] border-0 bg-transparent text-ink-3 hover:bg-row-hover hover:text-clay disabled:cursor-default disabled:opacity-30"
                        >
                          <TrashIcon size={15} />
                        </button>
                      </span>
                    </div>
                  );
                })}
              </div>
            </Panel>
          );
        })}

      <p className="m-0 px-1 text-xs text-ink-4">
        Rules count merchant rules, plus single transactions you sorted by hand. Other, Income and Transfers cannot be removed: Plaid’s mapping falls back to them.
      </p>

      <CategoryDialog open={editing.open} onOpenChange={(o) => setEditing((e) => ({ ...e, open: o }))} editing={editing.category} />
      <DeleteCategoryDialog target={removing} onOpenChange={(o) => !o && setRemoving(null)} usage={removing ? usage(removing) : null} />
    </Page>
  );
}
