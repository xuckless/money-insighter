import { useCallback, useEffect, useMemo, useState } from "react";
import { HashRouter, Navigate, Route, Routes } from "react-router";

import type { AppState } from "@shared/api";

import { AppShell } from "@/components/app-shell";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { AppStateContext } from "@/hooks/use-app-state";
import { DataVersionContext } from "@/hooks/use-data-version";
import { AccountsPage } from "@/pages/accounts";
import { CashFlowPage } from "@/pages/cashflow";
import { CategoriesPage } from "@/pages/categories";
import { OverviewPage } from "@/pages/overview";
import { ProfilePage } from "@/pages/profile";
import { RecurringPage } from "@/pages/recurring";
import { SettingsPage } from "@/pages/settings";
import { SpendingPage } from "@/pages/spending";
import { TransactionsPage } from "@/pages/transactions";
import { SetupScreen } from "@/screens/setup";
import { StartingScreen } from "@/screens/starting";

// App subscribes to the main process's state and gates everything on the
// phase: setup until the first configuration is saved, a full-screen
// status while the local services start or fail, and the pages once ready.
export function App() {
  const [state, setState] = useState<AppState | null>(null);
  const [version, setVersion] = useState(0);
  const bump = useCallback(() => setVersion((v) => v + 1), []);
  const dataVersion = useMemo(() => ({ version, bump }), [version, bump]);

  useEffect(() => {
    let cancelled = false;
    const off = window.api.app.onState((s) => setState(s));
    window.api.app.getState().then((s) => {
      if (!cancelled) setState((cur) => cur ?? s);
    });
    return () => {
      cancelled = true;
      off();
    };
  }, []);

  if (!state) return null;

  return (
    <AppStateContext.Provider value={state}>
      <DataVersionContext.Provider value={dataVersion}>
        <TooltipProvider>
          <HashRouter>
            {state.phase === "setup" ? (
              <SetupScreen />
            ) : state.phase !== "ready" ? (
              <StartingScreen />
            ) : (
              <Routes>
                <Route element={<AppShell />}>
                  <Route index element={<OverviewPage />} />
                  <Route path="spending" element={<SpendingPage />} />
                  <Route path="cash-flow" element={<CashFlowPage />} />
                  <Route path="recurring" element={<RecurringPage />} />
                  <Route path="accounts" element={<AccountsPage />} />
                  <Route path="transactions" element={<TransactionsPage />} />
                  <Route path="categories" element={<CategoriesPage />} />
                  <Route path="profile" element={<ProfilePage />} />
                  <Route path="settings" element={<SettingsPage />} />
                  {/* Screens folded into others by the redesign. */}
                  <Route path="connections" element={<Navigate to="/accounts" replace />} />
                  <Route path="logs" element={<Navigate to="/settings" replace />} />
                  <Route path="*" element={<Navigate to="/" replace />} />
                </Route>
              </Routes>
            )}
          </HashRouter>
          <Toaster position="bottom-right" />
        </TooltipProvider>
      </DataVersionContext.Provider>
    </AppStateContext.Provider>
  );
}
