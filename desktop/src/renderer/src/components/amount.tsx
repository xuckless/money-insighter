import { formatMoney, isNegative, negate } from "@/lib/format";
import { cn } from "@/lib/utils";

// TransactionAmount shows a Plaid transaction amount from the account
// holder's point of view: Plaid's positive (money out) becomes negative.
export function TransactionAmount({
  amount,
  currency,
  className,
}: {
  amount: string;
  currency: string | null;
  className?: string;
}) {
  const shown = negate(amount);
  return (
    <span
      className={cn(
        "font-mono tabular-nums",
        isNegative(shown) ? "text-red-600 dark:text-red-400" : "text-emerald-600 dark:text-emerald-400",
        className,
      )}
    >
      {formatMoney(shown, currency)}
    </span>
  );
}

export function Money({
  amount,
  currency,
  className,
}: {
  amount: string | null;
  currency: string | null;
  className?: string;
}) {
  return (
    <span className={cn("font-mono tabular-nums", className)}>{formatMoney(amount, currency)}</span>
  );
}
