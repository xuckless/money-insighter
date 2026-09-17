import { useEffect, useState } from "react";
import { HashRouter, Navigate, Outlet, Route, Routes } from "react-router";

import type { AppState } from "@shared/api";

import { AppNav } from "@/components/app-nav";
import { ErrorBoundary } from "@/components/error-boundary";
import { KekBackupDialog } from "@/components/kek-backup-dialog";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { AppStateContext } from "@/hooks/use-app-state";
import { AccountsPage } from "@/pages/accounts";
import { ConnectionsPage } from "@/pages/connections";
import { LogsPage } from "@/pages/logs";
import { OverviewPage } from "@/pages/overview";
import { SettingsPage } from "@/pages/settings";
import { TransactionsPage } from "@/pages/transactions";
import { SetupScreen } from "@/screens/setup";
import { StartingScreen } from "@/screens/starting";

// App subscribes to the main process's state and gates everything on the
// phase: setup until the first configuration is saved, a full-screen
// status while the local services start or fail, and the pages once ready.
export function App() {
  const [state, setState] = useState<AppState | null>(null);

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
      <TooltipProvider>
        <HashRouter>
          {state.phase === "setup" ? (
            <SetupScreen />
          ) : state.phase !== "ready" ? (
            <StartingScreen />
          ) : (
            <Routes>
              <Route element={<Shell />}>
                <Route index element={<OverviewPage />} />
                <Route path="accounts" element={<AccountsPage />} />
                <Route path="transactions" element={<TransactionsPage />} />
                <Route path="connections" element={<ConnectionsPage />} />
                <Route path="settings" element={<SettingsPage />} />
                <Route path="logs" element={<LogsPage />} />
                <Route path="*" element={<Navigate to="/" replace />} />
              </Route>
            </Routes>
          )}
        </HashRouter>
        <Toaster richColors position="bottom-right" />
      </TooltipProvider>
    </AppStateContext.Provider>
  );
}

function Shell() {
  return (
    <div className="flex min-h-screen flex-col md:flex-row">
      <AppNav />
      <main className="flex-1 px-4 py-6 md:px-8 md:py-8">
        <div className="mx-auto w-full max-w-6xl">
          <ErrorBoundary>
            <Outlet />
          </ErrorBoundary>
        </div>
      </main>
      <KekBackupDialog />
    </div>
  );
}
