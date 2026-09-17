import { useSearchParams } from "react-router";

import { TransactionAmount } from "@/components/amount";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useLoad } from "@/hooks/use-load";
import { formatDate, humanize } from "@/lib/format";
import { listSafe, topper, type Query } from "@/lib/topper";
import { TRANSACTION_COLUMNS } from "@/lib/topper-types";

import { TransactionFilters, type FilterValues } from "./transactions-filters";

const PAGE_SIZE = 50;
const DATE = /^\d{4}-\d{2}-\d{2}$/;

// Filters live in the hash's query string, so back/forward and reloads keep
// them, exactly as the web client did with the URL.
export function TransactionsPage() {
  const [params, setParams] = useSearchParams();
  const values: FilterValues = {
    from: params.get("from")?.trim() ?? "",
    to: params.get("to")?.trim() ?? "",
    account: params.get("account")?.trim() ?? "",
    q: params.get("q")?.trim() ?? "",
    pending: params.get("pending")?.trim() ?? "",
  };
  const page = Math.max(1, Number.parseInt(params.get("page") ?? "", 10) || 1);
  const q = listSafe(values.q).replace(/[%_]/g, "");

  const query: Query = {
    select: TRANSACTION_COLUMNS,
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
  if (q) query.or!.push(`name.ilike.%${q}%,merchant_name.ilike.%${q}%`);

  const key = params.toString();
  const [txns] = useLoad(() => topper.transactions(query), [key]);
  const [accounts] = useLoad(
    () =>
      topper.accounts({
        limit: 1000,
        toggles: { include_missing: 1 },
        select: ["account_id", "name", "mask", "institution_name"],
      }),
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
  const accountNames = new Map(accountOptions.map((a) => [a.id, a.label]));

  const total = txns.ok ? (txns.data.total ?? txns.data.count) : 0;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <>
      <PageHeader
        title="Transactions"
        description={txns.ok ? `${total.toLocaleString("en-CA")} matching transactions.` : undefined}
      />
      <TransactionFilters key={key} accounts={accountOptions} values={values} onApply={(next) => navigate(next)} />
      {!txns.ok ? (
        <LoadError what="transactions" message={txns.error} />
      ) : (
        <Card className="py-0">
          <CardContent className="overflow-x-auto px-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-4">Date</TableHead>
                  <TableHead>Description</TableHead>
                  <TableHead>Category</TableHead>
                  <TableHead>Account</TableHead>
                  <TableHead className="pr-4 text-right">Amount</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {txns.data.data.map((t) => (
                  <TableRow key={t.transaction_id}>
                    <TableCell className="pl-4 whitespace-nowrap text-muted-foreground">{formatDate(t.date)}</TableCell>
                    <TableCell className="max-w-72">
                      <div className="truncate font-medium">{t.merchant_name ?? t.name}</div>
                      {t.merchant_name && t.merchant_name !== t.name && (
                        <div className="truncate text-xs text-muted-foreground">{t.name}</div>
                      )}
                      {t.pending && (
                        <Badge variant="outline" className="mt-1">
                          Pending
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-muted-foreground">{humanize(t.pfc_primary)}</TableCell>
                    <TableCell className="max-w-56 truncate text-muted-foreground">
                      {accountNames.get(t.account_id) ?? t.account_id}
                    </TableCell>
                    <TableCell className="pr-4 text-right whitespace-nowrap">
                      <TransactionAmount amount={t.amount} currency={t.iso_currency_code ?? t.unofficial_currency_code} />
                    </TableCell>
                  </TableRow>
                ))}
                {txns.data.data.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={5} className="py-10 text-center text-muted-foreground">
                      No transactions match these filters.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
      {txns.ok && pages > 1 && (
        <div className="mt-4 flex items-center justify-between text-sm">
          <span className="text-muted-foreground">
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
    </>
  );
}
