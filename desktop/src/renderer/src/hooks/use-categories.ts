import { createContext, useContext } from "react";

import type { Category } from "@shared/categories";

// The category list, loaded once by the shell from topper.categories and
// registered with @shared/categories for lookups. Screens read it here so
// they re-render when a category is added or changed; `reload` refetches
// after a save.
export interface Categories {
  list: Category[];
  reload: () => void;
}

export const CategoriesContext = createContext<Categories | null>(null);

export function useCategories(): Categories {
  const c = useContext(CategoriesContext);
  if (!c) throw new Error("useCategories outside the provider");
  return c;
}
