import { Check, Copy, Eye, EyeOff, FolderOpen, RotateCw } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useAppState, useSettings } from "@/hooks/use-app-state";

import type { Settings, SettingsPatch } from "@shared/api";

export function SettingsPage() {
  const [settings, refresh] = useSettings();
  if (!settings) return <Loading />;
  // Keyed on the loaded values so a save re-seeds the form from the store.
  return <SettingsForm key={JSON.stringify(settings)} settings={settings} refresh={refresh} />;
}

function SettingsForm({ settings, refresh }: { settings: Settings; refresh: () => void }) {
  const state = useAppState();
  const [form, setForm] = useState<SettingsPatch>({
    plaidEnv: settings.plaidEnv,
    plaidClientId: settings.plaidClientId,
    plaidSecret: "",
    syncInterval: settings.syncInterval,
    countryCodes: settings.countryCodes,
    products: settings.products,
    transactionsDaysRequested: settings.transactionsDaysRequested,
    linkClientName: settings.linkClientName,
  });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const field = <K extends keyof SettingsPatch>(k: K) => (v: SettingsPatch[K]) => setForm((f) => ({ ...f, [k]: v }));

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      const patch: SettingsPatch = { ...form };
      if (!patch.plaidSecret) delete patch.plaidSecret;
      await window.api.app.updateSettings(patch);
      toast.success("Settings saved", { description: "The services are restarting." });
      refresh();
    } catch (err) {
      setError((err as Error).message.replace(/^Error invoking remote method '[^']+': Error: /, ""));
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <PageHeader title="Settings" description="Plaid credentials, sync behaviour and where your data lives." />

      <div className="grid gap-6 lg:grid-cols-[2fr_1fr]">
        <form onSubmit={save}>
          <Card>
            <CardHeader>
              <CardTitle>Plaid</CardTitle>
              <CardDescription>
                Keys are under Developers → Keys in the Plaid dashboard. Saving restarts the local
                services.
              </CardDescription>
            </CardHeader>
            <CardContent className="grid gap-4">
              <div className="grid gap-1.5">
                <Label>Environment</Label>
                <Select value={form.plaidEnv} onValueChange={(v) => field("plaidEnv")(v as "sandbox" | "production")}>
                  <SelectTrigger className="w-48">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="sandbox">Sandbox</SelectItem>
                    <SelectItem value="production">Production</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="clientId">Client ID</Label>
                <Input id="clientId" value={form.plaidClientId} onChange={(e) => field("plaidClientId")(e.target.value)} className="font-mono" />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="secret">Secret</Label>
                <Input
                  id="secret"
                  type="password"
                  placeholder="Leave blank to keep the current secret"
                  value={form.plaidSecret}
                  onChange={(e) => field("plaidSecret")(e.target.value)}
                  className="font-mono"
                  autoComplete="off"
                />
              </div>
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="grid gap-1.5">
                  <Label htmlFor="interval">Sync every</Label>
                  <Input id="interval" value={form.syncInterval} onChange={(e) => field("syncInterval")(e.target.value)} placeholder="1h" />
                  <p className="text-xs text-muted-foreground">Go duration: 30m, 1h, 6h.</p>
                </div>
                <div className="grid gap-1.5">
                  <Label htmlFor="days">Days of history at link time</Label>
                  <Input
                    id="days"
                    type="number"
                    min={1}
                    max={730}
                    value={form.transactionsDaysRequested}
                    onChange={(e) => field("transactionsDaysRequested")(Number(e.target.value))}
                  />
                </div>
                <div className="grid gap-1.5">
                  <Label htmlFor="countries">Country codes</Label>
                  <Input id="countries" value={form.countryCodes} onChange={(e) => field("countryCodes")(e.target.value)} placeholder="CA" />
                </div>
                <div className="grid gap-1.5">
                  <Label htmlFor="products">Products</Label>
                  <Input id="products" value={form.products} onChange={(e) => field("products")(e.target.value)} placeholder="transactions" />
                </div>
                <div className="grid gap-1.5 sm:col-span-2">
                  <Label htmlFor="linkName">Name shown in Plaid Link</Label>
                  <Input id="linkName" value={form.linkClientName} onChange={(e) => field("linkClientName")(e.target.value)} />
                </div>
              </div>
              {error && <LoadError what="settings" message={error} />}
              <div>
                <Button type="submit" disabled={saving}>
                  {saving ? "Saving…" : "Save and restart"}
                </Button>
              </div>
            </CardContent>
          </Card>
        </form>

        <div className="space-y-6">
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
    </>
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
