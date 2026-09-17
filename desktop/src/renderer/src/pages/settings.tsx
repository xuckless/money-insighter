import { Check, Copy, Eye, EyeOff, FolderOpen, RotateCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { toast } from "sonner";

import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { LogsPanel } from "@/components/logs-panel";
import { Page, PageIntro } from "@/components/page-intro";
import { Panel } from "@/components/panel";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAppState, useSettings } from "@/hooks/use-app-state";
import { useLoad } from "@/hooks/use-load";
import { formatRelative } from "@/lib/format";
import { topper } from "@/lib/topper";
import { cn } from "@/lib/utils";

import type { Settings } from "@shared/api";

export function SettingsPage() {
  const [settings, refresh] = useSettings();
  if (!settings) return <Loading />;
  // Keyed on the loaded values so a save re-seeds the form from the store.
  return <SettingsForm key={JSON.stringify(settings)} settings={settings} refresh={refresh} />;
}

function SettingsForm({ settings, refresh }: { settings: Settings; refresh: () => void }) {
  const state = useAppState();
  const [syncInterval, setSyncInterval] = useState(settings.syncInterval);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      await window.api.app.updateSettings({ syncInterval });
      toast.success("Settings saved", { description: "The services are restarting." });
      refresh();
    } catch (err) {
      setError((err as Error).message.replace(/^Error invoking remote method '[^']+': Error: /, ""));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Page>
      <PageIntro eyebrow="Settings">
        Sync, add-ons, and where <em>your data</em> lives.
      </PageIntro>

      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <div className="flex flex-col gap-4">
          <form onSubmit={save}>
            <Card>
              <CardHeader>
                <CardTitle>Sync</CardTitle>
                <CardDescription>
                  Every connection syncs on this schedule while the app is open, and whenever you press sync. Plaid keys and
                  Sandbox or Production mode are on your <Link to="/profile" className="font-semibold text-clay-ink">Profile</Link>.
                </CardDescription>
              </CardHeader>
              <CardContent className="grid gap-4">
                <div className="grid max-w-xs gap-1.5">
                  <Label htmlFor="interval">Sync every</Label>
                  <Input id="interval" value={syncInterval} onChange={(e) => setSyncInterval(e.target.value)} placeholder="1h" />
                  <p className="m-0 text-xs text-ink-3">A duration such as 30m, 1h or 6h.</p>
                </div>
                {error && <LoadError what="settings" message={error} />}
                <div>
                  <Button type="submit" disabled={saving || syncInterval === settings.syncInterval}>
                    {saving ? "Saving…" : "Save and restart"}
                  </Button>
                </div>
              </CardContent>
            </Card>
          </form>
          <AddOnsCard settings={settings} onChanged={refresh} />
        </div>

        <div className="flex flex-col gap-4">
          <KekCard backedUp={settings.kekBackedUp} onBackedUp={refresh} />

          <Card>
            <CardHeader>
              <CardTitle>This computer</CardTitle>
            </CardHeader>
            <CardContent className="grid gap-3 text-sm">
              <div>
                <p className="text-muted-foreground">Data directory</p>
                <p className="break-all font-mono text-xs">{state.dataDir}</p>
                <Button variant="outline" size="sm" className="mt-2" onClick={() => window.api.app.openDataDir()}>
                  <FolderOpen />
                  Open folder
                </Button>
              </div>
              <div>
                <p className="text-muted-foreground">Secrets protection</p>
                <p>{settings.secretsBackend}</p>
              </div>
              {settings.secretsBackend === "unavailable" && (
                <Alert>
                  <AlertTitle>No OS keychain available</AlertTitle>
                  <AlertDescription>
                    Secrets are stored obfuscated, not encrypted by the OS. On Linux, install and unlock
                    a keyring (GNOME Keyring or KWallet) and restart the app.
                  </AlertDescription>
                </Alert>
              )}
              <div>
                <p className="text-muted-foreground">Version</p>
                <p>
                  {state.version} · {state.platform}
                </p>
              </div>
              <div>
                <Button variant="outline" size="sm" onClick={() => window.api.app.restart()}>
                  <RotateCw />
                  Restart services
                </Button>
              </div>
            </CardContent>
          </Card>
        </div>
      </div>

      <Panel className="gap-3.5">
        <LogsPanel />
      </Panel>
    </Page>
  );
}

// AddOnsCard turns optional Plaid features on and off. Changing one
// restarts the local services, like any other setting.
function AddOnsCard({ settings, onChanged }: { settings: Settings; onChanged: () => void }) {
  const [params] = useSearchParams();
  const ref = useRef<HTMLDivElement>(null);
  const [saving, setSaving] = useState(false);
  const [status] = useLoad(() => topper.syncStatus({ limit: 1000 }), []);
  const focused = params.get("focus") === "add-ons";

  useEffect(() => {
    if (focused) ref.current?.scrollIntoView({ block: "center", behavior: "smooth" });
  }, [focused]);

  const toggle = async () => {
    setSaving(true);
    try {
      await window.api.app.updateSettings({ recurringEnabled: !settings.recurringEnabled });
      toast.success(settings.recurringEnabled ? "Recurring turned off" : "Recurring turned on", {
        description: settings.recurringEnabled ? "The services are restarting." : "The services are restarting; recurring payments are fetched for each connection.",
      });
      onChanged();
    } catch (err) {
      toast.error("Could not change the add-on", { description: (err as Error).message.replace(/^Error invoking remote method '[^']+': Error: /, "") });
    } finally {
      setSaving(false);
    }
  };

  const rows = status.ok === true ? status.data.data.filter((s) => s.status !== "removed") : [];

  return (
    <Card ref={ref} className={cn(focused && "border-clay")}>
      <CardHeader>
        <CardTitle>Add-ons</CardTitle>
        <CardDescription>Optional Plaid features. Turning one on or off restarts the local services.</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        <div className="flex items-start justify-between gap-4">
          <div className="grid gap-1">
            <span className="text-sm font-semibold">Recurring transactions</span>
            <span className="text-[12.5px] leading-snug text-ink-3">
              Finds paycheques, bills and subscriptions and predicts the next ones. Powers Recurring, Coming up, Cash flow projections
              and price-change alerts. Free in Sandbox; in Production it must be enabled on your Plaid account and Plaid bills for it.
            </span>
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={settings.recurringEnabled}
            aria-label="Recurring transactions"
            disabled={saving}
            onClick={() => void toggle()}
            className={cn(
              "relative mt-0.5 h-6 w-11 shrink-0 cursor-pointer rounded-full border-0 transition-colors disabled:opacity-60",
              settings.recurringEnabled ? "bg-clay" : "bg-line-strong",
            )}
          >
            <span className={cn("absolute top-0.5 size-5 rounded-full bg-sheet shadow transition-all", settings.recurringEnabled ? "left-[22px]" : "left-0.5")} />
          </button>
        </div>
        {settings.recurringEnabled && rows.length > 0 && (
          <ul className="m-0 grid list-none gap-1.5 border-t border-hairline p-0 pt-3 text-[12.5px]">
            {rows.map((r) => (
              <li key={r.item_id} className="flex items-baseline justify-between gap-3">
                <span className="truncate">{r.institution_name ?? r.item_id}</span>
                <span className={cn("text-right", r.recurring_error_code ? "text-clay-ink" : "text-ink-3")} title={r.recurring_error_message ?? undefined}>
                  {r.recurring_error_code
                    ? r.recurring_error_code === "PRODUCT_NOT_ENABLED" || r.recurring_error_code === "INVALID_PRODUCT"
                      ? "Not enabled for this Plaid account"
                      : r.recurring_error_code
                    : r.recurring_refreshed_at
                      ? `Updated ${formatRelative(r.recurring_refreshed_at)}`
                      : "Waiting for the next sync"}
                </span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function KekCard({ backedUp, onBackedUp }: { backedUp: boolean; onBackedUp: () => void }) {
  const [key, setKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const reveal = async () => setKey(key ? null : await window.api.app.revealKek());
  const copy = async () => {
    const k = key ?? (await window.api.app.revealKek());
    await navigator.clipboard.writeText(k);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Encryption key</CardTitle>
        <CardDescription>
          Encrypts the bank access tokens stored here. Without it every linked bank must be
          reconnected. {backedUp ? "You confirmed a backup." : "Not backed up yet."}
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-2">
        {key && <code className="break-all rounded-md border bg-muted px-3 py-2 font-mono text-xs">{key}</code>}
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={reveal}>
            {key ? <EyeOff /> : <Eye />}
            {key ? "Hide" : "Reveal"}
          </Button>
          <Button variant="outline" size="sm" onClick={copy}>
            {copied ? <Check /> : <Copy />}
            Copy
          </Button>
          {!backedUp && (
            <Button
              size="sm"
              onClick={async () => {
                await window.api.app.markKekBackedUp();
                onBackedUp();
              }}
            >
              I saved it
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
