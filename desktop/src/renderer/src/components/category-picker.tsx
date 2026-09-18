import { useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { CategoryIcon, Glyph } from "@/components/category-icon";
import { ChevronDownIcon } from "@/components/icons";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useCategories } from "@/hooks/use-categories";
import { useDataVersion } from "@/hooks/use-data-version";
import { clearOverride, overrideTransaction, rememberMerchant } from "@/lib/categorize";
import type { CategorizedRow } from "@/lib/topper-types";
import { cn } from "@/lib/utils";

import { category, kindLabel, kindOrder } from "@shared/categories";

// CategoryPicker shows a transaction's category and lets the user change
// it, for this transaction only or for every transaction of the merchant.
export function CategoryPicker({ txn }: { txn: Pick<CategorizedRow, "transaction_id" | "category" | "category_source" | "needs_category" | "merchant_key" | "merchant_name" | "name"> }) {
  const { bump } = useDataVersion();
  const { list } = useCategories();
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
            "inline-flex h-7 max-w-full cursor-pointer items-center gap-1.5 rounded-[4px] border pr-1.5 pl-2 text-[12.5px] font-medium hover:bg-row-hover",
            txn.needs_category ? "border-ochre-bg bg-ochre-bg/50 text-ochre-ink" : "border-line bg-sheet text-ink",
          )}
          title={txn.category_source === "rule" ? "Set by your rule for this merchant" : txn.category_source === "override" ? "Set by you for this transaction" : "From Plaid"}
        >
          {!txn.needs_category && <CategoryIcon category={cat} size={16} className="bg-transparent!" />}
          <span className="truncate">{txn.needs_category ? "Needs a category" : cat.label}</span>
          <ChevronDownIcon size={13} className="text-ink-3" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-[70vh] w-64 overflow-y-auto">
        <DropdownMenuCheckboxItem checked={allFromMerchant} onCheckedChange={(v) => setAllFromMerchant(v === true)} onSelect={(e) => e.preventDefault()}>
          <span className="truncate">Apply to every {merchant}</span>
        </DropdownMenuCheckboxItem>
        {kindOrder.map((k) => {
          const rows = list.filter((c) => c.kind === k);
          if (rows.length === 0) return null;
          return (
            <div key={k}>
              <DropdownMenuSeparator />
              <DropdownMenuLabel className="eyebrow">{kindLabel[k]}</DropdownMenuLabel>
              {rows.map((c) => {
                return (
                  <DropdownMenuItem key={c.id} onSelect={() => void choose(c.id)} className={cn(c.id === txn.category && "font-semibold")}>
                    <Glyph name={c.icon} size={15} strokeWidth={1.75} style={{ color: c.color }} />
                    {c.label}
                  </DropdownMenuItem>
                );
              })}
            </div>
          );
        })}
        <DropdownMenuSeparator />
        {txn.category_source === "override" && (
          <DropdownMenuItem
            onSelect={async () => {
              await clearOverride(txn.transaction_id).catch((err) => toast.error("Could not reset", { description: (err as Error).message }));
              bump();
            }}
          >
            Undo my change for this transaction
          </DropdownMenuItem>
        )}
        <DropdownMenuItem asChild>
          <Link to="/categories" className="text-clay-ink no-underline">
            Manage categories…
          </Link>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
