// The app's fixed category set. The topper maps Plaid's personal finance
// categories onto these ids in SQL (postgres-topper migration
// 00002_insights.sql, topper.plaid_category) and its tables only accept
// these ids; keep the two lists in step.

export type CategoryKind = "spending" | "income" | "transfer";

export interface Category {
  id: CategoryId;
  label: string;
  // Swatch colour, from the design's palette.
  color: string;
  kind: CategoryKind;
  // Fixed costs arrive on a schedule rather than evenly across a month, so
  // "ahead of pace" warnings skip them.
  fixed: boolean;
}

export const categories = [
  { id: "groceries", label: "Groceries", color: "#5E7F5A", kind: "spending", fixed: false },
  { id: "dining", label: "Dining & takeout", color: "#C06A45", kind: "spending", fixed: false },
  { id: "shopping", label: "Shopping", color: "#86587C", kind: "spending", fixed: false },
  { id: "transport", label: "Transport", color: "#4F6D8A", kind: "spending", fixed: false },
  { id: "utilities", label: "Utilities & phone", color: "#A67A1F", kind: "spending", fixed: true },
  { id: "housing", label: "Rent & housing", color: "#7A5B45", kind: "spending", fixed: true },
  { id: "subscriptions", label: "Subscriptions", color: "#3F7F7A", kind: "spending", fixed: true },
  { id: "health", label: "Health & fitness", color: "#A1566B", kind: "spending", fixed: false },
  { id: "entertainment", label: "Entertainment", color: "#6B6FA3", kind: "spending", fixed: false },
  { id: "travel", label: "Travel", color: "#3E7F9C", kind: "spending", fixed: false },
  { id: "loans", label: "Loan payments", color: "#5F6B5B", kind: "spending", fixed: true },
  { id: "other", label: "Other", color: "#8C8174", kind: "spending", fixed: false },
  { id: "income", label: "Income", color: "#3F6B4E", kind: "income", fixed: false },
  { id: "transfer", label: "Transfers", color: "#9C9184", kind: "transfer", fixed: false },
] as const satisfies readonly { id: string; label: string; color: string; kind: CategoryKind; fixed: boolean }[];

export type CategoryId = (typeof categories)[number]["id"];

const byId = new Map<string, Category>(categories.map((c) => [c.id, c as Category]));

// category returns the category for an id; an unknown id (a future
// category, or bad data) falls back to Other so the UI never breaks.
export function category(id: string | null | undefined): Category {
  return byId.get(id ?? "") ?? (byId.get("other") as Category);
}

export const spendingCategories: Category[] = categories.filter((c) => c.kind === "spending") as Category[];

export function isSpending(id: string | null | undefined): boolean {
  return category(id).kind === "spending";
}
