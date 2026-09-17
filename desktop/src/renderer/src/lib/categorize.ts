import { topper } from "@/lib/topper";

// Categorising writes one of two things: a rule for every transaction of a
// merchant, or an override for one transaction. A rule replaces any
// override the user set earlier on that merchant's transactions only when
// asked; overrides always win over rules in the topper's view.

export async function rememberMerchant(merchantKey: string, category: string): Promise<void> {
  await topper.upsert("merchant_rules", { merchant_key: merchantKey, category });
}

export async function overrideTransaction(transactionId: string, category: string): Promise<void> {
  await topper.upsert("category_overrides", { transaction_id: transactionId, category });
}

export async function clearOverride(transactionId: string): Promise<void> {
  await topper.remove("category_overrides", { transaction_id: `eq.${transactionId}` });
}
