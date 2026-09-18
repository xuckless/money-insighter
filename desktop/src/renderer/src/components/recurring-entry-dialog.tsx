import { useState } from "react";
import { toast } from "sonner";

import { CategoryTag } from "@/components/category-icon";
import { Segmented } from "@/components/segmented";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useCategories } from "@/hooks/use-categories";
import { useDataVersion } from "@/hooks/use-data-version";
import { todayISO } from "@/lib/dates";
import { frequencyLabel } from "@/lib/insights/recurring";
import { deleteRecurringEntry, saveRecurringEntry } from "@/lib/queries";
import type { EntryFrequency, RecurringEntryRow } from "@/lib/topper-types";

import { kindLabel, kindOrder } from "@shared/categories";

const FREQUENCIES: EntryFrequency[] = ["WEEKLY", "BIWEEKLY", "SEMI_MONTHLY", "MONTHLY", "ANNUALLY"];
const NONE = "none";

// RecurringEntryDialog adds a recurring payment by hand, or edits one.
// Choosing the account it is paid from puts it in Cash flow; a merchant
// match hides the detected stream it would otherwise double.
export function RecurringEntryDialog({
  open,
  onOpenChange,
  editing,
  accounts,
  prefill,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  editing: RecurringEntryRow | null;
  accounts: { id: string; label: string }[];
  // Starting values for a new entry (from a detected stream, say).
  prefill?: Partial<Omit<RecurringEntryRow, "id">>;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        {open && <EntryForm editing={editing} accounts={accounts} prefill={prefill} onDone={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  );
}

function EntryForm({
  editing,
  accounts,
  prefill,
  onDone,
}: {
  editing: RecurringEntryRow | null;
  accounts: { id: string; label: string }[];
  prefill?: Partial<Omit<RecurringEntryRow, "id">>;
  onDone: () => void;
}) {
  const { list } = useCategories();
  const { bump } = useDataVersion();
  const base = editing ?? prefill;
  const [name, setName] = useState(base?.name ?? "");
  const [amount, setAmount] = useState(base?.amount ? String(Math.abs(Number(base.amount))) : "");
  const [direction, setDirection] = useState<"inflow" | "outflow">(base?.direction ?? "outflow");
  const [frequency, setFrequency] = useState<EntryFrequency>(base?.frequency ?? "MONTHLY");
  const [nextDate, setNextDate] = useState(base?.next_date ?? todayISO());
  const [categoryId, setCategoryId] = useState(base?.category ?? (direction === "inflow" ? "income" : "bills"));
  const [account, setAccount] = useState(base?.account_id ?? NONE);
  const [notes, setNotes] = useState(base?.notes ?? "");
  const [saving, setSaving] = useState(false);
  const [removing, setRemoving] = useState(false);
  const n = Number(amount);
  const valid = name.trim() !== "" && Number.isFinite(n) && n > 0 && /^\d{4}-\d{2}-\d{2}$/.test(nextDate) && list.some((c) => c.id === categoryId);

  const save = async () => {
    setSaving(true);
    try {
      await saveRecurringEntry({
        id: editing?.id,
        name,
        amount: n,
        direction,
        frequency,
        next_date: nextDate,
        category: categoryId,
        account_id: account === NONE ? null : account,
        merchant_key: editing?.merchant_key ?? prefill?.merchant_key ?? null,
        notes: notes.trim() || null,
      });
      toast.success(editing ? "Recurring payment saved" : `${name.trim()} added`);
      bump();
      onDone();
    } catch (err) {
      toast.error("Could not save", { description: (err as Error).message });
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!editing) return;
    setRemoving(true);
    try {
      await deleteRecurringEntry(editing.id);
      toast.success(`${editing.name} removed`);
      bump();
      onDone();
    } catch (err) {
      toast.error("Could not remove", { description: (err as Error).message });
    } finally {
      setRemoving(false);
    }
  };

  return (
    <>
      <DialogHeader>
        <DialogTitle>{editing ? "Edit recurring payment" : "Add a recurring payment"}</DialogTitle>
        <DialogDescription>
          For anything the bank feed does not show as a stream yet: rent paid by e-transfer, a yearly renewal, a paycheque.
        </DialogDescription>
      </DialogHeader>
      <div className="grid gap-4">
        <Segmented
          label="Direction"
          value={direction}
          options={[
            { value: "outflow", label: "Money out" },
            { value: "inflow", label: "Money in" },
          ]}
          onChange={(d) => {
            setDirection(d);
            if (!editing && !prefill?.category) setCategoryId(d === "inflow" ? "income" : "bills");
          }}
          className="justify-self-start"
        />
        <div className="grid gap-1.5">
          <Label htmlFor="re-name">Name</Label>
          <Input id="re-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Rent" autoFocus />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="grid gap-1.5">
            <Label htmlFor="re-amount">Amount</Label>
            <Input id="re-amount" inputMode="decimal" className="num" value={amount} onChange={(e) => setAmount(e.target.value.replace(/[^0-9.]/g, ""))} placeholder="0.00" />
          </div>
          <div className="grid gap-1.5">
            <Label>Every</Label>
            <Select value={frequency} onValueChange={(v) => setFrequency(v as EntryFrequency)}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {FREQUENCIES.map((f) => (
                  <SelectItem key={f} value={f}>
                    {frequencyLabel[f]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="re-next">Next on</Label>
            <Input id="re-next" type="date" value={nextDate} onChange={(e) => setNextDate(e.target.value)} />
          </div>
          <div className="grid gap-1.5">
            <Label>Category</Label>
            <Select value={categoryId} onValueChange={setCategoryId}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {kindOrder.map((k) => {
                  const rows = list.filter((c) => c.kind === k);
                  return rows.length === 0 ? null : (
                    <div key={k}>
                      <div className="eyebrow px-2 pt-2 pb-1">{kindLabel[k]}</div>
                      {rows.map((c) => (
                        <SelectItem key={c.id} value={c.id}>
                          <CategoryTag category={c} />
                        </SelectItem>
                      ))}
                    </div>
                  );
                })}
              </SelectContent>
            </Select>
          </div>
        </div>
        <div className="grid gap-1.5">
          <Label>Paid from</Label>
          <Select value={account} onValueChange={setAccount}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE}>No account (left out of Cash flow)</SelectItem>
              {accounts.map((a) => (
                <SelectItem key={a.id} value={a.id}>
                  {a.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="re-notes">Note</Label>
          <Input id="re-notes" value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="Optional" />
        </div>
      </div>
      <DialogFooter className={editing ? "sm:justify-between" : undefined}>
        {editing && (
          <Button variant="destructive" onClick={() => void remove()} disabled={removing || saving}>
            {removing ? "Removing…" : "Remove"}
          </Button>
        )}
        <div className="flex gap-2">
          <Button variant="outline" onClick={onDone} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={() => void save()} disabled={!valid || saving}>
            {saving ? "Saving…" : editing ? "Save" : "Add"}
          </Button>
        </div>
      </DialogFooter>
    </>
  );
}
