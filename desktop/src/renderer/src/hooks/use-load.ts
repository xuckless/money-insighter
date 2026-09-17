import { useCallback, useEffect, useRef, useState, type DependencyList } from "react";

import { describe } from "@/lib/plaidsync";

export type Loaded<T> = { ok: true; data: T } | { ok: false; error: string } | { ok: "loading" };

// useLoad runs fn whenever deps change and exposes the result as a value.
// A reload keeps the previous data on screen until the new fetch lands, so
// refreshing after a mutation does not flash a skeleton.
export function useLoad<T>(fn: () => Promise<T>, deps: DependencyList): [Loaded<T>, () => void] {
  const [state, setState] = useState<Loaded<T>>({ ok: "loading" });
  const [tick, setTick] = useState(0);
  const latest = useRef(0);

  useEffect(() => {
    const seq = ++latest.current;
    let cancelled = false;
    fn().then(
      (data) => {
        if (!cancelled && seq === latest.current) setState({ ok: true, data });
      },
      (err) => {
        if (!cancelled && seq === latest.current) setState({ ok: false, error: describe(err) });
      },
    );
    return () => {
      cancelled = true;
    };
    // fn is intentionally not a dependency: callers pass an inline closure.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return [state, reload];
}
