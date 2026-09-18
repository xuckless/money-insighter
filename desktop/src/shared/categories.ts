// Categories. The list lives in the database (topper.categories, seeded by
// postgres-topper migration 00003_categories.sql with the built-in set
// below) so the user can add their own. The renderer loads the table once
// per session (see hooks/use-categories) and registers it here, so the
// insight code and every screen look ids up in the same place. Until the
// table is loaded, and in tests, the built-in set is what is registered.
//
// Keep `builtin` in step with the migration: same ids, same kinds.

// What a category means to the screens:
//   spending  everyday money out, paced day by day and budgeted
//   bill      fixed costs that land on a schedule; budgeted, not paced
//   income    money in
//   transfer  money moved between the user's own accounts
export type CategoryKind = "spending" | "bill" | "income" | "transfer";

export interface Category {
  id: string;
  label: string;
  // Swatch colour, #rrggbb.
  color: string;
  // A name from components/category-icon's set.
  icon: string;
  kind: CategoryKind;
  builtin: boolean;
  sortOrder: number;
}

export const kindLabel: Record<CategoryKind, string> = {
  spending: "Spending",
  bill: "Bills",
  income: "Income",
  transfer: "Transfers",
};

export const kindOrder: CategoryKind[] = ["spending", "bill", "income", "transfer"];

const seed: Omit<Category, "builtin">[] = [
  { id: "groceries", label: "Groceries", color: "#5E7F5A", icon: "shopping-basket", kind: "spending", sortOrder: 10 },
  { id: "dining", label: "Dining & takeout", color: "#C06A45", icon: "utensils", kind: "spending", sortOrder: 20 },
  { id: "shopping", label: "Shopping", color: "#86587C", icon: "shopping-bag", kind: "spending", sortOrder: 30 },
  { id: "transport", label: "Transport", color: "#4F6D8A", icon: "car", kind: "spending", sortOrder: 40 },
  { id: "health", label: "Health & fitness", color: "#A1566B", icon: "heart-pulse", kind: "spending", sortOrder: 50 },
  { id: "medical", label: "Medical", color: "#B5443A", icon: "stethoscope", kind: "spending", sortOrder: 60 },
  { id: "personal_care", label: "Personal care", color: "#8A6A9E", icon: "sparkles", kind: "spending", sortOrder: 70 },
  { id: "entertainment", label: "Entertainment", color: "#6B6FA3", icon: "clapperboard", kind: "spending", sortOrder: 80 },
  { id: "travel", label: "Travel", color: "#3E7F9C", icon: "plane", kind: "spending", sortOrder: 90 },
  { id: "education", label: "Education", color: "#2F6F8F", icon: "graduation-cap", kind: "spending", sortOrder: 100 },
  { id: "kids_pets", label: "Kids & pets", color: "#B0782A", icon: "paw-print", kind: "spending", sortOrder: 110 },
  { id: "gifts", label: "Gifts & donations", color: "#9C4F7C", icon: "gift", kind: "spending", sortOrder: 120 },
  { id: "other", label: "Other", color: "#8C8174", icon: "circle-dashed", kind: "spending", sortOrder: 900 },
  { id: "housing", label: "Rent & housing", color: "#7A5B45", icon: "house", kind: "bill", sortOrder: 200 },
  { id: "utilities", label: "Utilities & phone", color: "#A67A1F", icon: "plug-zap", kind: "bill", sortOrder: 210 },
  { id: "subscriptions", label: "Subscriptions", color: "#3F7F7A", icon: "repeat", kind: "bill", sortOrder: 220 },
  { id: "loans", label: "Loan payments", color: "#5F6B5B", icon: "landmark", kind: "bill", sortOrder: 230 },
  { id: "bills", label: "Other bills", color: "#7D6E8E", icon: "receipt", kind: "bill", sortOrder: 240 },
  { id: "income", label: "Income", color: "#3F6B4E", icon: "banknote", kind: "income", sortOrder: 300 },
  { id: "tax_refund", label: "Tax refunds", color: "#4E8A62", icon: "badge-percent", kind: "income", sortOrder: 310 },
  { id: "transfer", label: "Transfers", color: "#9C9184", icon: "arrow-left-right", kind: "transfer", sortOrder: 400 },
];

// builtinCategories is the seed the migration inserts.
export const builtinCategories: readonly Category[] = seed.map((c) => ({ ...c, builtin: true }));

let registered: Category[] = sortCategories(builtinCategories);
let byId = new Map<string, Category>(registered.map((c) => [c.id, c]));

export function sortCategories(list: readonly Category[]): Category[] {
  return [...list].sort((a, b) => kindOrder.indexOf(a.kind) - kindOrder.indexOf(b.kind) || a.sortOrder - b.sortOrder || a.label.localeCompare(b.label));
}

// registerCategories replaces the list every lookup below reads. The
// renderer calls it with the table's rows; tests never need to.
export function registerCategories(list: readonly Category[]): void {
  registered = sortCategories(list);
  byId = new Map(registered.map((c) => [c.id, c]));
}

// allCategories is the registered list in display order: spending, bills,
// income, transfers, each by sort order.
export function allCategories(): readonly Category[] {
  return registered;
}

// category returns the category for an id; an unknown id (a category
// deleted since a screen loaded, or bad data) falls back to Other so the UI
// never breaks.
export function category(id: string | null | undefined): Category {
  return byId.get(id ?? "") ?? byId.get("other") ?? registered[0];
}

export function hasCategory(id: string | null | undefined): boolean {
  return byId.has(id ?? "");
}

export function categoriesOfKind(...kinds: CategoryKind[]): Category[] {
  return registered.filter((c) => kinds.includes(c.kind));
}

// Money out: spending and bills. Everything a budget can be set on.
export function expenseCategories(): Category[] {
  return categoriesOfKind("spending", "bill");
}

export function isExpense(id: string | null | undefined): boolean {
  const k = category(id).kind;
  return k === "spending" || k === "bill";
}

// isSpending keeps the older name: any money out, bills included.
export const isSpending = isExpense;

// isBill reports a fixed cost that lands on a schedule rather than evenly
// across a month, so pace warnings and the daily pace chart skip it.
export function isBill(id: string | null | undefined): boolean {
  return category(id).kind === "bill";
}

export function isIncome(id: string | null | undefined): boolean {
  return category(id).kind === "income";
}

export function isTransfer(id: string | null | undefined): boolean {
  return category(id).kind === "transfer";
}

// The design's palette for new categories, in the order the picker shows
// them.
export const categoryPalette = [
  "#5E7F5A", "#3F6B4E", "#4E8A62", "#3F7F7A", "#3E7F9C", "#2F6F8F", "#4F6D8A", "#6B6FA3",
  "#8A6A9E", "#86587C", "#9C4F7C", "#A1566B", "#B5443A", "#C06A45", "#A3472B", "#B0782A",
  "#A67A1F", "#7A5B45", "#5F6B5B", "#7D6E8E", "#8C8174", "#9C9184",
] as const;

// slugId makes a new category's id from its label: "c_" plus lowercase
// letters, digits and underscores, with a suffix when `taken` says the
// slug exists already.
export function slugId(label: string, taken: (id: string) => boolean): string {
  const base = "c_" + (label.toLowerCase().normalize("NFKD").replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "").slice(0, 30) || "category");
  if (!taken(base)) return base;
  for (let i = 2; i < 1000; i++) if (!taken(`${base}_${i}`)) return `${base}_${i}`;
  return `${base}_${Date.now().toString(36)}`;
}
