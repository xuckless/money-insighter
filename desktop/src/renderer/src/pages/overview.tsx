import { ArrowRight } from "lucide-react";
import { Link } from "react-router";

import { Money, TransactionAmount } from "@/components/amount";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useLoad } from "@/hooks/use-load";
import { totalsByCurrency } from "@/lib/balances";
import { formatDate, formatRelative } from "@/lib/format";
import { plaidsync } from "@/lib/plaidsync";
import { topper } from "@/lib/topper";
import { TRANSACTION_COLUMNS } from "@/lib/topper-types";

export function OverviewPage() {
  const [accounts] = useLoad(() => topper.accounts({ limit: 1000 }), []);
  const [items] = useLoad(() => plaidsync.listItems(), []);
  const [recent] = useLoad(() => topper.transactions({ select: TRANSACTION_COLUMNS, limit: 10 }), []);

  if (accounts.ok === "loading" || items.ok === "loading" || recent.ok === "loading") return <Loading />;

  const liveItems = items.ok ? items.data.items.filter((i) => i.status !== "removed") : [];
  const accountNames = new Map(accounts.ok ? accounts.data.data.map((a) => [a.account_id, a.name]) : []);

  return (
    <>
      <PageHeader title="Overview" description="Balances and recent activity across your connections." />

      {!accounts.ok ? (
        <LoadError what="balances" message={accounts.error} />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {totalsByCurrency(accounts.data.data).map((t) => (
            <Card key={t.currency}>
              <CardHeader>
                <CardDescription>
                  Net · {t.currency} · {t.accounts} account{t.accounts === 1 ? "" : "s"}
                </CardDescription>
                <CardTitle className="text-2xl">
                  <Money amount={t.net} currency={t.currency} />
                </CardTitle>
              </CardHeader>
              <CardContent className="flex justify-between text-sm text-muted-foreground">
                <span>
                  Assets <Money amount={t.assets} currency={t.currency} className="text-foreground" />
                </span>
                <span>
                  Owed <Money amount={t.liabilities} currency={t.currency} className="text-foreground" />
                </span>
              </CardContent>
            </Card>
          ))}
          {accounts.data.data.length === 0 && (
            <Card className="sm:col-span-2 lg:col-span-3">
              <CardHeader>
                <CardTitle>No accounts yet</CardTitle>
                <CardDescription>Connect a bank to start syncing balances and transactions.</CardDescription>
              </CardHeader>
              <CardContent>
                <Button asChild>
                  <Link to="/connections">Go to Connections</Link>
                </Button>
              </CardContent>
            </Card>
          )}
        </div>
      )}

      <div className="mt-6 grid gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle>Recent transactions</CardTitle>
            <Button asChild variant="ghost" size="sm">
              <Link to="/transactions">
                All <ArrowRight />
              </Link>
            </Button>
          </CardHeader>
          <CardContent>
            {!recent.ok ? (
              <LoadError what="transactions" message={recent.error} />
            ) : recent.data.data.length === 0 ? (
              <p className="text-sm text-muted-foreground">No transactions yet.</p>
            ) : (
              <ul className="divide-y">
                {recent.data.data.map((t) => (
                  <li key={t.transaction_id} className="flex items-center justify-between gap-4 py-2.5">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">{t.merchant_name ?? t.name}</p>
                      <p className="truncate text-xs text-muted-foreground">
                        {formatDate(t.date)} · {accountNames.get(t.account_id) ?? "Account"}
                        {t.pending && " · Pending"}
                      </p>
                    </div>
                    <TransactionAmount
                      amount={t.amount}
                      currency={t.iso_currency_code ?? t.unofficial_currency_code}
                      className="text-sm"
                    />
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle>Connections</CardTitle>
            <Button asChild variant="ghost" size="sm">
              <Link to="/connections">
                Manage <ArrowRight />
              </Link>
            </Button>
          </CardHeader>
          <CardContent>
            {!items.ok ? (
              <LoadError what="connections" message={items.error} />
            ) : liveItems.length === 0 ? (
              <p className="text-sm text-muted-foreground">No banks connected.</p>
            ) : (
              <ul className="space-y-3">
                {liveItems.map((i) => (
                  <li key={i.item_id} className="flex items-center justify-between gap-2">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">{i.institution_name ?? i.item_id}</p>
                      <p className="text-xs text-muted-foreground">
                        Synced {formatRelative(i.last_successful_sync_at)}
                      </p>
                    </div>
                    <StatusBadge status={i.status} />
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
    </>
  );
}
