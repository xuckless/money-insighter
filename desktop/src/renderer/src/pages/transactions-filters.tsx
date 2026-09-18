import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { useCategories } from "@/hooks/use-categories";

export interface FilterValues {
  from: string;
  to: string;
  account: string;
  q: string;
  pending: string;
  category: string;
}

const ALL = "all";

export const emptyFilters: FilterValues = { from: "", to: "", account: "", q: "", pending: "", category: "" };

export function TransactionFilters({
  accounts,
  values,
  onApply,
}: {
  accounts: { id: string; label: string }[];
  values: FilterValues;
  onApply: (next: FilterValues) => void;
}) {
  const { list: categories } = useCategories();
  const [v, setV] = useState<FilterValues>(values);
  const set = (k: keyof FilterValues) => (val: string) => setV((cur) => ({ ...cur, [k]: val === ALL ? "" : val }));

  return (
    <form
      className="grid gap-3 rounded-[4px] border border-line bg-sheet px-5 py-4 sm:grid-cols-2 xl:grid-cols-[1.2fr_auto_auto_1fr_1fr_auto_auto]"
      onSubmit={(e) => {
        e.preventDefault();
        onApply(v);
      }}
    >
      <div className="grid gap-1.5">
        <Label htmlFor="q">Search</Label>
        <Input id="q" placeholder="Merchant or description" value={v.q} onChange={(e) => set("q")(e.target.value)} />
      </div>
      <div className="grid gap-1.5">
        <Label htmlFor="from">From</Label>
        <Input id="from" type="date" value={v.from} onChange={(e) => set("from")(e.target.value)} />
      </div>
      <div className="grid gap-1.5">
        <Label htmlFor="to">To</Label>
        <Input id="to" type="date" value={v.to} onChange={(e) => set("to")(e.target.value)} />
      </div>
      <div className="grid gap-1.5">
        <Label>Account</Label>
        <Select value={v.account || ALL} onValueChange={set("account")}>
          <SelectTrigger className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All accounts</SelectItem>
            {accounts.map((a) => (
              <SelectItem key={a.id} value={a.id}>
                {a.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="grid gap-1.5">
        <Label>Category</Label>
        <Select value={v.category || ALL} onValueChange={set("category")}>
          <SelectTrigger className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All categories</SelectItem>
            <SelectItem value="needs">Needs a category</SelectItem>
            {categories.map((c) => (
              <SelectItem key={c.id} value={c.id}>
                {c.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="grid gap-1.5">
        <Label>Status</Label>
        <Select value={v.pending || ALL} onValueChange={set("pending")}>
          <SelectTrigger className="w-full xl:w-28">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>Any</SelectItem>
            <SelectItem value="no">Posted</SelectItem>
            <SelectItem value="yes">Pending</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div className="flex items-end gap-2">
        <Button type="submit">Apply</Button>
        <Button
          type="button"
          variant="ghost"
          onClick={() => {
            setV(emptyFilters);
            onApply(emptyFilters);
          }}
        >
          Reset
        </Button>
      </div>
    </form>
  );
}
