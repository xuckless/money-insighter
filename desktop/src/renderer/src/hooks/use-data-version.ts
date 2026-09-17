import { createContext, useContext } from "react";

// DataVersion lets anything that changes data (a sync, a new category, a
// budget) tell every mounted page to reload. Pages add `version` to their
// useLoad dependencies.
export interface DataVersion {
  version: number;
  bump: () => void;
}

export const DataVersionContext = createContext<DataVersion>({ version: 0, bump: () => undefined });

export function useDataVersion(): DataVersion {
  return useContext(DataVersionContext);
}
