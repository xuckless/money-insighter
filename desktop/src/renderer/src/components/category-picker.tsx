import { useState } from "react";
import { toast } from "sonner";

import { Swatch } from "@/components/panel";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useDataVersion } from "@/hooks/use-data-version";
import { clearOverride, overrideTransaction, rememberMerchant } from "@/lib/categorize";
import type { CategorizedRow } from "@/lib/topper-types";
import { cn } from "@/lib/utils";

import { categories, category } from "@shared/categories";

// CategoryPicker shows a transaction's category and lets the user change
// it, for this transaction only or for every transaction of the merchant.
export function CategoryPicker({ txn }: { txn: Pick<CategorizedRow, "transaction_id" | "category" | "category_source" | "needs_category" | "merchant_key" | "merchant_name" | "name"> }) {
  const { bump } = useDataVersion();
  const [allFromMerchant, setAllFromMerchant] = useState(true);
  const [busy, setBusy] = useState(false);
  const cat = category(txn.category);
  const merchant = txn.merchant_name ?? txn.name;

  const choose = async (id: string) => {
    setBusy(true);
    try {
      if (allFromMerchant) {
        await rememberMerchant(txn.merchant_key, id);
        if (txn.category_source === "override") await clearOverride(txn.transaction_id);
      } else {
        await overrideTransaction(txn.transaction_id, id);
      }
      bump();
    } catch (err) {
      toast.error("Could not change the category", { description: (err as Error).message });
    } finally {
      setBusy(false);
    }
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          disabled={busy}
          className={cn(
            "inline-flex max-w-full cursor-pointer items-center gap-2 rounded-full border px-2.5 py-1 text-[12.5px] font-medium hover:bg-row-hover",
            txn.needs_category ? "border-ochre-bg bg-ochre-bg/50 text-ochre-ink" : "border-line bg-sheet text-ink",
          )}
          title={txn.category_source === "rule" ? "Set by your rule for this merchant" : txn.category_source === "override" ? "Set by you for this transaction" : "From Plaid"}
        >
          <Swatch color={cat.color} className="size-2 rounded-[2px]" />
          <span className="truncate">{txn.needs_category ? "Needs a category" : cat.label}</span>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-64">
        <DropdownMenuCheckboxItem checked={allFromMerchant} onCheckedChange={(v) => setAllFromMerchant(v === true)} onSelect={(e) => e.preventDefault()}>
          <span className="truncate">Apply to every {merchant}</span>
        </DropdownMenuCheckboxItem>
        <DropdownMenuSeparator />
        <DropdownMenuLabel className="eyebrow">Category</DropdownMenuLabel>
        {categories.map((c) => (
          <DropdownMenuItem key={c.id} onSelect={() => void choose(c.id)} className={cn(c.id === txn.category && "font-semibold")}>
            <Swatch color={c.color} className="size-2.5" />
            {c.label}
          </DropdownMenuItem>
        ))}
        {txn.category_source === "override" && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onSelect={async () => {
                await clearOverride(txn.transaction_id).catch((err) => toast.error("Could not reset", { description: (err as Error).message }));
                bump();
              }}
            >
              Undo my change for this transaction
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
