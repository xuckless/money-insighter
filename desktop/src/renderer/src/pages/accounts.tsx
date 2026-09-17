import { Link, useSearchParams } from "react-router";

import { Money } from "@/components/amount";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/status-badge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useLoad } from "@/hooks/use-load";
import { LIABILITY_TYPES } from "@/lib/balances";
import { formatRelative, humanize } from "@/lib/format";
import { topper } from "@/lib/topper";
import type { AccountRow } from "@/lib/topper-types";

export function AccountsPage() {
  const [params] = useSearchParams();
  const includeMissing = params.get("missing") === "1";
  const [accounts] = useLoad(
    () => topper.accounts({ limit: 1000, toggles: { include_missing: includeMissing ? 1 : 0 } }),
    [includeMissing],
  );

  const toggle = (
    <Button asChild variant="outline" size="sm">
      <Link to={includeMissing ? "/accounts" : "/accounts?missing=1"}>
        {includeMissing ? "Hide closed accounts" : "Show closed accounts"}
      </Link>
    </Button>
  );

  if (accounts.ok === "loading") return <Loading />;
  if (!accounts.ok) {
    return (
      <>
        <PageHeader title="Accounts" actions={toggle} />
        <LoadError what="accounts" message={accounts.error} />
      </>
    );
  }

  const byInstitution = new Map<string, AccountRow[]>();
  for (const a of accounts.data.data) {
    byInstitution.set(a.item_id, [...(byInstitution.get(a.item_id) ?? []), a]);
  }

  return (
    <>
      <PageHeader
        title="Accounts"
        description={`${accounts.data.count} account${accounts.data.count === 1 ? "" : "s"} across ${byInstitution.size} connection${byInstitution.size === 1 ? "" : "s"}.`}
        actions={toggle}
      />
      {byInstitution.size === 0 && (
        <p className="text-sm text-muted-foreground">
          No accounts yet.{" "}
          <Link className="underline" to="/connections">
            Connect a bank
          </Link>
          .
        </p>
      )}
      <div className="space-y-6">
        {[...byInstitution.values()].map((rows) => (
          <Card key={rows[0].item_id}>
            <CardHeader className="flex flex-row items-center justify-between gap-2">
              <CardTitle>{rows[0].institution_name ?? rows[0].item_id}</CardTitle>
              {rows[0].item_status !== "active" && <StatusBadge status={rows[0].item_status} />}
            </CardHeader>
            <CardContent className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Account</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead className="text-right">Current</TableHead>
                    <TableHead className="text-right">Available</TableHead>
                    <TableHead className="text-right">Limit</TableHead>
                    <TableHead className="text-right">Updated</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((a) => {
                    const currency = a.iso_currency_code ?? a.unofficial_currency_code;
                    return (
                      <TableRow key={a.account_id} className={a.missing_since ? "opacity-60" : undefined}>
                        <TableCell>
                          <div className="font-medium">
                            {a.name}
                            {a.mask && <span className="ml-1 text-muted-foreground">••{a.mask}</span>}
                          </div>
                          {a.official_name && a.official_name !== a.name && (
                            <div className="text-xs text-muted-foreground">{a.official_name}</div>
                          )}
                          {a.missing_since && (
                            <Badge variant="outline" className="mt-1">
                              No longer reported
                            </Badge>
                          )}
                        </TableCell>
                        <TableCell className="text-muted-foreground">{humanize(a.subtype ?? a.type)}</TableCell>
                        <TableCell className="text-right">
                          <Money
                            amount={a.current_balance}
                            currency={currency}
                            className={LIABILITY_TYPES.has(a.type) ? "text-red-600 dark:text-red-400" : undefined}
                          />
                        </TableCell>
                        <TableCell className="text-right">
                          <Money amount={a.available_balance} currency={currency} />
                        </TableCell>
                        <TableCell className="text-right">
                          <Money amount={a.credit_limit} currency={currency} />
                        </TableCell>
                        <TableCell className="text-right text-xs text-muted-foreground">
                          {formatRelative(a.balance_last_updated_at ?? a.updated_at)}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        ))}
      </div>
    </>
  );
}
