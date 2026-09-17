import { Pause, Play } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { PanelHeader } from "@/components/panel";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import type { LogService } from "@shared/api";

const services: { id: LogService; label: string }[] = [
  { id: "app", label: "App" },
  { id: "plaidsync", label: "Sync service (plaidsync)" },
  { id: "topper", label: "Data service (topper)" },
  { id: "postgres", label: "Database (Postgres)" },
];

// LogsPanel tails one service's log, refreshing every two seconds until
// paused. It lives at the bottom of Settings.
export function LogsPanel() {
  const [service, setService] = useState<LogService>("app");
  const [paused, setPaused] = useState(false);
  const [lines, setLines] = useState<string[]>([]);
  const pre = useRef<HTMLPreElement>(null);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      const next = await window.api.app.getLogs(service, 300);
      if (!cancelled) setLines(next);
    };
    void load();
    if (paused) return;
    const id = setInterval(() => void load(), 2000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [service, paused]);

  // Keep the newest line in view unless the user scrolled up.
  useEffect(() => {
    const el = pre.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    if (nearBottom) el.scrollTop = el.scrollHeight;
  }, [lines]);

  return (
    <>
      <PanelHeader title="Logs" description="The last 300 lines of each local service. Full logs are in the data directory.">
        <div className="flex shrink-0 gap-2">
            <Select value={service} onValueChange={(v) => setService(v as LogService)}>
              <SelectTrigger className="w-56">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {services.map((s) => (
                  <SelectItem key={s.id} value={s.id}>
                    {s.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button variant="outline" onClick={() => setPaused((p) => !p)}>
              {paused ? <Play /> : <Pause />}
              {paused ? "Resume" : "Pause"}
            </Button>
        </div>
      </PanelHeader>
      <pre
        ref={pre}
        className="h-[50vh] overflow-auto rounded-[10px] border border-line bg-paper p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap"
      >
        {lines.length === 0 ? <span className="text-ink-3">No output yet.</span> : lines.join("\n")}
      </pre>
    </>
  );
}
