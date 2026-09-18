import { ExternalLink } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { LoadError } from "@/components/load-error";
import { Loading } from "@/components/loading";
import { Grid, Page, PageHeader } from "@/components/page-header";
import { Chip } from "@/components/panel";
import { Segmented } from "@/components/segmented";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useSettings } from "@/hooks/use-app-state";
import { useLoad } from "@/hooks/use-load";
import { plaidsync } from "@/lib/plaidsync";
import { cn } from "@/lib/utils";

import type { PlaidEnv, Settings, SettingsPatch } from "@shared/api";

const modeLabel: Record<PlaidEnv, string> = { sandbox: "Sandbox", production: "Production" };

// cleanError strips Electron's IPC wrapper from a main-process error.
function cleanError(err: unknown): string {
  return (err as Error).message.replace(/^Error invoking remote method '[^']+': Error: /, "");
}

// ProfilePage is the user's Plaid identity: the keys the app signs in to
// Plaid with, the mode (Sandbox or Production) it runs in, and how Plaid
// Link presents it. Every change restarts the local services.
export function ProfilePage() {
  const [settings, refresh] = useSettings();
  if (!settings) return <Loading />;
  // Keyed on the saved values so each save re-seeds the forms.
  return <Profile key={JSON.stringify(settings)} settings={settings} refresh={refresh} />;
}

function Profile({ settings, refresh }: { settings: Settings; refresh: () => void }) {
  const [items] = useLoad(() => plaidsync.listItems(), []);
  const connections = items.ok === true ? items.data.items.filter((i) => i.status !== "removed").length : null;

  return (
    <Page>
      <PageHeader
        title="Profile"
        subtitle={`Connected to Plaid ${modeLabel[settings.plaidEnv]}${connections !== null ? ` with ${connections} connection${connections === 1 ? "" : "s"}` : ""}.`}
      />

      <Grid className="items-start">
        <div className="col-span-12 flex flex-col gap-4 xl:col-span-6">
          <KeysCard settings={settings} onSaved={refresh} />
          <LinkCard settings={settings} onSaved={refresh} />
        </div>
        <div className="col-span-12 xl:col-span-6">
          <ModeCard settings={settings} connections={connections} onSaved={refresh} />
        </div>
      </Grid>
    </Page>
  );
}

function KeysCard({ settings, onSaved }: { settings: Settings; onSaved: () => void }) {
  const [clientId, setClientId] = useState(settings.plaidClientId);
  const [secret, setSecret] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const changed = clientId.trim() !== settings.plaidClientId || secret.trim() !== "";

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      const patch: SettingsPatch = { plaidClientId: clientId };
      if (secret.trim()) patch.plaidSecret = secret;
      await window.api.app.updateSettings(patch);
      toast.success("Plaid keys saved", { description: "The services are restarting." });
      onSaved();
    } catch (err) {
      setError(cleanError(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={save}>
      <Card>
        <CardHeader>
          <CardTitle>Plaid keys</CardTitle>
          <CardDescription>
            From Developers → Keys in the Plaid dashboard. The client ID is the same in every mode; each mode has its own secret.
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          <div className="grid gap-1.5">
            <Label htmlFor="clientId">Client ID</Label>
            <Input id="clientId" value={clientId} onChange={(e) => setClientId(e.target.value)} className="font-mono" autoComplete="off" spellCheck={false} />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="secret" className="flex items-center gap-2">
              {modeLabel[settings.plaidEnv]} secret
              {settings.savedSecrets[settings.plaidEnv] && <Chip className="bg-moss-soft text-moss">Saved</Chip>}
            </Label>
            <Input
              id="secret"
              type="password"
              placeholder="Leave blank to keep the saved secret"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
              className="font-mono"
              autoComplete="off"
            />
            <p className="m-0 text-xs text-ink-3">Stored encrypted by your operating system’s keychain, never shown again.</p>
          </div>
          {error && <LoadError what="the keys" message={error} />}
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" disabled={saving || !changed}>
              {saving ? "Saving…" : "Save and restart"}
            </Button>
            <Button type="button" variant="ghost" onClick={() => void window.api.app.openExternal("https://dashboard.plaid.com/developers/keys")}>
              <ExternalLink />
              Open the Plaid dashboard
            </Button>
          </div>
        </CardContent>
      </Card>
    </form>
  );
}

function ModeCard({ settings, connections, onSaved }: { settings: Settings; connections: number | null; onSaved: () => void }) {
  const [target, setTarget] = useState<PlaidEnv>(settings.plaidEnv);
  const [secret, setSecret] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const switching = target !== settings.plaidEnv;
  const needsSecret = switching && !settings.savedSecrets[target];

  const apply = async () => {
    setSaving(true);
    setError(null);
    try {
      const patch: SettingsPatch = { plaidEnv: target };
      if (secret.trim()) patch.plaidSecret = secret;
      await window.api.app.updateSettings(patch);
      toast.success(`Switched to ${modeLabel[target]}`, { description: "The services are restarting." });
      onSaved();
    } catch (err) {
      setError(cleanError(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Mode</CardTitle>
        <CardDescription>Which Plaid environment the app talks to.</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-5">
        <Segmented
          label="Plaid mode"
          value={target}
          options={[
            { value: "sandbox", label: "Sandbox" },
            { value: "production", label: "Production" },
          ]}
          onChange={(v) => {
            setTarget(v);
            setSecret("");
            setError(null);
          }}
          className="justify-self-start"
        />
        <div className="grid gap-3 text-[13px] leading-relaxed text-ink-2">
          <ModeLine active={target === "sandbox"} title="Sandbox">
            Test banks and made-up transactions (sign in with user_good / pass_good). Free, and nothing real is touched.
          </ModeLine>
          <ModeLine active={target === "production"} title="Production">
            Your real banks. Needs Production access on your Plaid account; Plaid bills per connection, and the Recurring add-on is
            billed separately.
          </ModeLine>
        </div>

        {switching && (
          <div className="grid gap-4 rounded-[4px] border border-line bg-paper px-4 py-4">
            <p className="m-0 text-[13px] leading-relaxed text-ink-2">
              <strong className="text-ink">Connections belong to the mode they were made in.</strong>{" "}
              {connections
                ? `Your ${connections} ${modeLabel[settings.plaidEnv]} connection${connections === 1 ? "" : "s"} will stop syncing in ${modeLabel[target]}; their transactions stay here. Remove them from Accounts if you won’t switch back.`
                : `After switching, connect your accounts again in ${modeLabel[target]}.`}
            </p>
            {needsSecret ? (
              <div className="grid gap-1.5">
                <Label htmlFor="targetSecret">{modeLabel[target]} secret</Label>
                <Input
                  id="targetSecret"
                  type="password"
                  value={secret}
                  onChange={(e) => setSecret(e.target.value)}
                  className="font-mono"
                  autoComplete="off"
                  placeholder={`The ${target} secret from the Plaid dashboard`}
                />
              </div>
            ) : (
              <p className="m-0 text-xs text-ink-3">The {modeLabel[target]} secret you saved before will be used.</p>
            )}
            {error && <LoadError what={`${modeLabel[target]} mode`} message={error} />}
            <div className="flex gap-2">
              <Button onClick={() => void apply()} disabled={saving || (needsSecret && !secret.trim())}>
                {saving ? "Switching…" : `Switch to ${modeLabel[target]}`}
              </Button>
              <Button variant="outline" onClick={() => setTarget(settings.plaidEnv)} disabled={saving}>
                Cancel
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function ModeLine({ active, title, children }: { active: boolean; title: string; children: React.ReactNode }) {
  return (
    <div className={cn("grid gap-0.5 border-l-2 pl-3", active ? "border-clay" : "border-line")}>
      <span className={cn("text-[13px] font-semibold", active ? "text-ink" : "text-ink-3")}>{title}</span>
      <span className={active ? undefined : "text-ink-3"}>{children}</span>
    </div>
  );
}

function LinkCard({ settings, onSaved }: { settings: Settings; onSaved: () => void }) {
  const [form, setForm] = useState({
    linkClientName: settings.linkClientName,
    countryCodes: settings.countryCodes,
    products: settings.products,
    transactionsDaysRequested: settings.transactionsDaysRequested,
  });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const changed =
    form.linkClientName !== settings.linkClientName ||
    form.countryCodes !== settings.countryCodes ||
    form.products !== settings.products ||
    form.transactionsDaysRequested !== settings.transactionsDaysRequested;

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      await window.api.app.updateSettings(form);
      toast.success("Link options saved", { description: "They apply to the next account you connect." });
      onSaved();
    } catch (err) {
      setError(cleanError(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={save}>
      <Card>
        <CardHeader>
          <CardTitle>Connecting accounts</CardTitle>
          <CardDescription>How Plaid Link presents the app and what it asks your bank for. Existing connections keep their settings.</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2">
          <div className="grid gap-1.5 sm:col-span-2">
            <Label htmlFor="linkName">Name shown in Plaid Link</Label>
            <Input id="linkName" value={form.linkClientName} onChange={(e) => setForm((f) => ({ ...f, linkClientName: e.target.value }))} />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="countries">Country codes</Label>
            <Input id="countries" value={form.countryCodes} onChange={(e) => setForm((f) => ({ ...f, countryCodes: e.target.value.toUpperCase() }))} placeholder="CA" />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="days">Days of history</Label>
            <Input
              id="days"
              type="number"
              min={1}
              max={730}
              value={form.transactionsDaysRequested}
              onChange={(e) => setForm((f) => ({ ...f, transactionsDaysRequested: Number(e.target.value) }))}
            />
          </div>
          <div className="grid gap-1.5 sm:col-span-2">
            <Label htmlFor="products">Products</Label>
            <Input id="products" value={form.products} onChange={(e) => setForm((f) => ({ ...f, products: e.target.value }))} placeholder="transactions" />
          </div>
          {error && (
            <div className="sm:col-span-2">
              <LoadError what="the Link options" message={error} />
            </div>
          )}
          <div className="sm:col-span-2">
            <Button type="submit" disabled={saving || !changed}>
              {saving ? "Saving…" : "Save and restart"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </form>
  );
}
