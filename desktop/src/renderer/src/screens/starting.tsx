import { CircleAlert, CircleCheck, Circle, Loader2 } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useAppState } from "@/hooks/use-app-state";

import type { ServiceState } from "@shared/api";

const labels = {
  postgres: "Database",
  plaidsync: "Sync service",
  topper: "Data service",
} as const;

// StartingScreen covers every phase between setup and ready: services
// coming up, going down, or having failed.
export function StartingScreen() {
  const state = useAppState();
  const failed = state.phase === "error";
  const [logs, setLogs] = useState<string[]>([]);

  useEffect(() => {
    if (!failed) return;
    let cancelled = false;
    window.api.app.getLogs("app", 40).then((l) => {
      if (!cancelled) setLogs(l);
    });
    return () => {
      cancelled = true;
    };
  }, [failed, state.message]);

  const title =
    state.phase === "stopping"
      ? "Stopping"
      : state.phase === "stopped"
        ? "Stopped"
        : failed
          ? "Could not start"
          : "Starting Money Insighter";

  return (
    <div className="flex min-h-screen items-center justify-center px-4 py-10">
      <Card className="w-full max-w-xl">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            {failed ? <CircleAlert className="size-5 text-destructive" /> : <Loader2 className="size-5 animate-spin" />}
            {title}
          </CardTitle>
          <CardDescription>{state.message}</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          <ul className="grid gap-2 text-sm">
            {(Object.keys(labels) as (keyof typeof labels)[]).map((name) => (
              <ServiceRow key={name} label={labels[name]} state={state.services[name]} />
            ))}
          </ul>
          {failed && (
            <>
              <pre className="max-h-64 overflow-auto rounded-md border bg-muted/60 p-3 font-mono text-xs whitespace-pre-wrap">
                {logs.join("\n") || "No log output."}
              </pre>
              <div className="flex justify-end">
                <Button onClick={() => window.api.app.restart()}>Retry</Button>
              </div>
            </>
          )}
          {state.phase === "stopped" && (
            <div className="flex justify-end">
              <Button onClick={() => window.api.app.restart()}>Start</Button>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function ServiceRow({ label, state }: { label: string; state: ServiceState }) {
  const icon =
    state.phase === "ready" ? (
      <CircleCheck className="size-4 text-emerald-600" />
    ) : state.phase === "starting" ? (
      <Loader2 className="size-4 animate-spin" />
    ) : state.phase === "error" ? (
      <CircleAlert className="size-4 text-destructive" />
    ) : (
      <Circle className="size-4 text-muted-foreground" />
    );
  return (
    <li className="flex items-center gap-3">
      {icon}
      <span className="w-28">{label}</span>
      <span className="text-muted-foreground">
        {state.phase}
        {state.port ? ` · port ${state.port}` : ""}
        {state.detail ? ` · ${state.detail}` : ""}
      </span>
    </li>
  );
}
