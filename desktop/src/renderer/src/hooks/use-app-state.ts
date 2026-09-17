import { createContext, useContext, useEffect, useState } from "react";

import type { AppState, Settings } from "@shared/api";

export const AppStateContext = createContext<AppState | null>(null);

export function useAppState(): AppState {
  const s = useContext(AppStateContext);
  if (!s) throw new Error("useAppState outside the provider");
  return s;
}

// useSettings loads the non-secret settings once per mount; `refresh`
// re-reads them after a save.
export function useSettings(): [Settings | null, () => void] {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let cancelled = false;
    window.api.app.getSettings().then((s) => {
      if (!cancelled) setSettings(s);
    });
    return () => {
      cancelled = true;
    };
  }, [tick]);
  return [settings, () => setTick((t) => t + 1)];
}
