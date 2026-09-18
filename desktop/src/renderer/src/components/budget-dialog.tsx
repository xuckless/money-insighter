import { useState } from "react";
import { toast } from "sonner";

import { CategoryTag } from "@/components/category-icon";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { useCategories } from "@/hooks/use-categories";
import { useDataVersion } from "@/hooks/use-data-version";
import { fmt } from "@/lib/money";
import { saveBudgets } from "@/lib/queries";

import { kindLabel, type Category } from "@shared/categories";

// BudgetDialog edits the monthly budget of every spending and bill
// category. Empty fields start from the suggestion (the recent monthly
// average, rounded up); clearing a field removes that category's budget.
export function BudgetDialog({
  open,
  onOpenChange,
  budgets,
  suggestions,
  currency,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  budgets: Map<string, number>;
  suggestions: Map<string, number>;
  currency: string;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        {open && <BudgetForm budgets={budgets} suggestions={suggestions} currency={currency} onDone={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  );
}

// BudgetForm is mounted each time the dialog opens, so it starts from the
// saved budgets (or the suggestions, the first time).
function BudgetForm({
  budgets,
  suggestions,
  currency,
  onDone,
}: {
  budgets: Map<string, number>;
  suggestions: Map<string, number>;
  currency: string;
  onDone: () => void;
}) {
  const { bump } = useDataVersion();
  const { list } = useCategories();
  const expense = list.filter((c) => c.kind === "spending" || c.kind === "bill");
  const [values, setValues] = useState<Record<string, string>>(() => {
    const next: Record<string, string> = {};
    for (const c of expense) {
      const v = budgets.get(c.id) ?? (budgets.size === 0 ? suggestions.get(c.id) : undefined);
      next[c.id] = v ? String(v) : "";
    }
    return next;
  });
  const [saving, setSaving] = useState(false);

  const total = Object.values(values).reduce((a, v) => a + (Number(v) || 0), 0);
  const invalid = Object.values(values).some((v) => v !== "" && !(Number(v) >= 0));

  const save = async () => {
    setSaving(true);
    try {
      await saveBudgets(new Map(expense.map((c) => [c.id, Number(values[c.id]) || 0])));
      toast.success("Budgets saved");
      bump();
      onDone();
    } catch (err) {
      toast.error("Could not save budgets", { description: (err as Error).message });
    } finally {
      setSaving(false);
    }
  };

  const group = (kind: Category["kind"]) => expense.filter((c) => c.kind === kind);

  return (
    <>
      <DialogHeader>
        <DialogTitle>Monthly budgets</DialogTitle>
        <DialogDescription>
          {budgets.size === 0
            ? "Filled in from what you usually spend over the last three months. Adjust anything, then save."
            : "Suggestions show what you usually spend. Leave a category empty for no budget."}
        </DialogDescription>
      </DialogHeader>
      <div className="grid max-h-[55vh] gap-1 overflow-y-auto pr-1">
        {(["spending", "bill"] as const).map((kind) => (
          <div key={kind} className="grid gap-1">
            <div className="eyebrow pt-2 pb-1">{kindLabel[kind]}</div>
            {group(kind).map((c) => (
              <label key={c.id} className="grid grid-cols-[1fr_auto_112px] items-center gap-3 text-[13.5px]">
                <CategoryTag category={c} />
                <span className="num text-xs text-ink-3">{suggestions.has(c.id) ? `usually ${fmt(suggestions.get(c.id)!, 0, currency)}` : ""}</span>
                <Input
                  inputMode="decimal"
                  className="num h-8 text-right"
                  placeholder="—"
                  value={values[c.id] ?? ""}
                  onChange={(e) => setValues((v) => ({ ...v, [c.id]: e.target.value.replace(/[^0-9.]/g, "") }))}
                />
              </label>
            ))}
          </div>
        ))}
      </div>
      <DialogFooter className="items-center sm:justify-between">
        <span className="num text-[13px] text-ink-3">
          Total <strong className="text-ink">{fmt(total, 0, currency)}</strong> a month
        </span>
        <div className="flex gap-2">
          <Button variant="outline" onClick={onDone}>
            Cancel
          </Button>
          <Button onClick={save} disabled={saving || invalid}>
            {saving ? "Saving…" : "Save budgets"}
          </Button>
        </div>
      </DialogFooter>
    </>
  );
}
