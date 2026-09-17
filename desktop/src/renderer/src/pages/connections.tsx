import { Link, useSearchParams } from "react-router";

import { HostedLinkButton } from "@/components/hosted-link-button";
import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { PageHeader } from "@/components/page-header";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useSettings } from "@/hooks/use-app-state";
import { useLoad } from "@/hooks/use-load";
import { plaidsync } from "@/lib/plaidsync";
import { topper } from "@/lib/topper";

import { ConnectionCard } from "./connection-card";
import { SandboxButton } from "./sandbox-button";

export function ConnectionsPage() {
  const [params] = useSearchParams();
  const showRemoved = params.get("removed") === "1";
  const [settings] = useSettings();

  // Items come straight from plaidsync so a just-linked bank shows at once;
  // account counts come from the topper and may lag by its cache TTL.
  const [items, reloadItems] = useLoad(async () => {
    const { items } = await plaidsync.listItems();
    return Promise.all(items.map((i) => plaidsync.getItem(i.item_id)));
  }, []);
  const [accounts, reloadAccounts] = useLoad(
    () => topper.accounts({ limit: 1000, select: ["account_id", "item_id"] }),
    [],
  );
  const reload = () => {
    reloadItems();
    reloadAccounts();
  };

  if (items.ok === "loading" || accounts.ok === "loading") return <Loading />;

  const accountCounts = new Map<string, number>();
  if (accounts.ok) {
    for (const a of accounts.data.data) {
      accountCounts.set(a.item_id, (accountCounts.get(a.item_id) ?? 0) + 1);
    }
  }

  const sandbox = settings?.plaidEnv === "sandbox";
  const all = items.ok ? items.data : [];
  const live = all.filter((d) => d.item.status !== "removed");
  const removed = all.filter((d) => d.item.status === "removed");
  const shown = showRemoved ? all : live;

  return (
    <>
      <PageHeader
        title="Connections"
        description={`Banks linked through Plaid. Syncs run every ${settings?.syncInterval ?? "hour"} in the background; you can also sync on demand.`}
        actions={
          <>
            {sandbox && <SandboxButton onAdded={reload} />}
            <HostedLinkButton onLinked={reload}>Connect a bank</HostedLinkButton>
          </>
        }
      />
      {!items.ok ? (
        <LoadError what="connections" message={items.error} />
      ) : shown.length === 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>No banks connected</CardTitle>
            <CardDescription>
              Use “Connect a bank” to open Plaid Link in your browser.
              {sandbox && " In Sandbox, sign in with user_good / pass_good."}
            </CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {shown.map((d) => (
            <ConnectionCard
              key={d.item.item_id}
              item={d.item}
              latestJob={d.jobs[0] ?? null}
              latestRun={d.runs[0] ?? null}
              accountCount={accountCounts.get(d.item.item_id) ?? null}
              onChanged={reload}
            />
          ))}
        </div>
      )}
      {removed.length > 0 && (
        <p className="mt-6 text-sm text-muted-foreground">
          {removed.length} removed connection{removed.length === 1 ? "" : "s"}.{" "}
          <Link className="underline" to={showRemoved ? "/connections" : "/connections?removed=1"}>
            {showRemoved ? "Hide" : "Show"}
          </Link>
        </p>
      )}
    </>
  );
}
