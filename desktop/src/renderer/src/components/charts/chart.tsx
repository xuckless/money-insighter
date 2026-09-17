import { addDays, daysInMonth, eachDay, monthStart, weekdayMon0, type ISODate } from "@/lib/dates";
import { cn } from "@/lib/utils";

// The charts are drawn the way the design draws them: an SVG stretched to
// its box (viewBox width 1000, preserveAspectRatio none) with strokes that
// do not scale, and HTML laid over it for labels and markers so text is
// never distorted. X runs 0..1000 in the SVG and 0..100% in the overlay;
// Y is in pixels in both.

export const VIEW_W = 1000;

export type Point = [x: number, y: number];

export function linePath(points: Point[]): string {
  return points.map((p, i) => `${i ? "L" : "M"}${p[0].toFixed(1)} ${p[1].toFixed(1)}`).join(" ");
}

// bandPath closes the area between an upper and a lower edge.
export function bandPath(upper: Point[], lower: Point[]): string {
  return `${linePath([...upper, ...[...lower].reverse()])} Z`;
}

export interface Scale {
  height: number;
  // y maps a value to a pixel offset from the top.
  y: (v: number) => number;
  // x maps a 0..1 position to SVG units, pct to a CSS left.
  x: (t: number) => number;
  pct: (t: number) => string;
}

export function scale(height: number, min: number, max: number): Scale {
  const span = max - min || 1;
  return {
    height,
    y: (v) => height - ((v - min) / span) * height,
    x: (t) => t * VIEW_W,
    pct: (t) => `${(t * 100).toFixed(2)}%`,
  };
}

// ticks lists values from min to max by step.
export function ticks(min: number, max: number, step: number): number[] {
  const out: number[] = [];
  for (let v = min; v <= max + step / 1000; v += step) out.push(Math.round(v * 100) / 100);
  return out;
}

// ChartFrame draws the y labels, grid lines and x labels around a chart,
// with the chart's SVG layers and HTML overlay inside.
export function ChartFrame({
  scale: s,
  yTicks,
  xTicks,
  label,
  gutter = 44,
  svg,
  overlay,
  className,
}: {
  scale: Scale;
  yTicks: { value: number; label: string }[];
  xTicks: { t: number; label: string; align?: "start" | "center" | "end" }[];
  label: string;
  gutter?: number;
  svg: React.ReactNode;
  overlay?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col gap-2", className)} style={{ marginLeft: gutter }}>
      <div className="relative" style={{ height: s.height }}>
        {yTicks.map((t) => (
          <div
            key={t.value}
            className="num absolute text-right text-[11px] text-ink-3"
            style={{ left: -gutter, width: gutter - 10, top: s.y(t.value) - 7 }}
          >
            {t.label}
          </div>
        ))}
        <svg
          viewBox={`0 0 ${VIEW_W} ${s.height}`}
          preserveAspectRatio="none"
          className="absolute inset-x-0 top-0 w-full overflow-visible"
          style={{ height: s.height }}
          role="img"
          aria-label={label}
        >
          <path
            d={yTicks.map((t) => `M0 ${s.y(t.value).toFixed(1)} H${VIEW_W}`).join(" ")}
            fill="none"
            stroke="var(--color-grid)"
            strokeWidth={1}
            vectorEffect="non-scaling-stroke"
          />
          {svg}
        </svg>
        {overlay}
      </div>
      <div className="relative h-4 text-[11px] text-ink-3">
        {xTicks.map((t) => (
          <span
            key={`${t.t}-${t.label}`}
            className="absolute whitespace-nowrap"
            style={{
              left: t.align === "end" ? undefined : s.pct(t.t),
              right: t.align === "end" ? 0 : undefined,
              transform: t.align === "start" || t.align === "end" ? undefined : "translateX(-50%)",
            }}
          >
            {t.label}
          </span>
        ))}
      </div>
    </div>
  );
}

// Line is one stroked series.
export function Line({ d, color, width = 2.25, dashed = false }: { d: string; color: string; width?: number; dashed?: boolean }) {
  return (
    <path
      d={d}
      fill="none"
      stroke={color}
      strokeWidth={width}
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeDasharray={dashed ? "5 5" : undefined}
      vectorEffect="non-scaling-stroke"
    />
  );
}

export function Area({ d, color }: { d: string; color: string }) {
  return <path d={d} fill={color} stroke="none" />;
}

// Dot is a marker on the overlay, centred on its point.
export function Dot({
  left,
  top,
  size = 12,
  color,
  ring = true,
  hollow = false,
  title,
}: {
  left: string;
  top: number;
  size?: number;
  color: string;
  ring?: boolean;
  hollow?: boolean;
  title?: string;
}) {
  return (
    <div
      title={title}
      className="absolute rounded-full"
      style={{
        left,
        top,
        width: size,
        height: size,
        margin: `${-size / 2}px 0 0 ${-size / 2}px`,
        background: hollow ? "var(--color-sheet)" : color,
        border: hollow ? `2px solid ${color}` : undefined,
        boxShadow: ring && !hollow ? "0 0 0 3px var(--color-sheet)" : undefined,
      }}
    />
  );
}

// Sparkline is a small trend line with no axes.
export function Sparkline({
  values,
  color,
  width = 84,
  height = 26,
  strokeWidth = 1.75,
}: {
  values: number[];
  color: string;
  width?: number;
  height?: number;
  strokeWidth?: number;
}) {
  if (values.length < 2) return <svg width={width} height={height} aria-hidden />;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const pad = 3;
  const pts: Point[] = values.map((v, i) => [
    pad + (i * (width - 2 * pad)) / (values.length - 1),
    height - pad - ((v - min) / (max - min || 1)) * (height - 2 * pad),
  ]);
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} aria-hidden>
      <path d={linePath(pts)} fill="none" stroke={color} strokeWidth={strokeWidth} strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

// StackedBar is a horizontal bar split into proportional segments.
export function StackedBar({
  segments,
  height = 14,
  className,
}: {
  segments: { key: string; value: number; color: string; title?: string }[];
  height?: number;
  className?: string;
}) {
  const total = segments.reduce((a, s) => a + Math.max(0, s.value), 0);
  return (
    <div className={cn("flex gap-0.5 overflow-hidden rounded-[4px]", className)} style={{ height }}>
      {total > 0 ? (
        segments
          .filter((s) => s.value > 0)
          .map((s) => <div key={s.key} title={s.title} style={{ width: `${(s.value / total) * 100}%`, background: s.color }} />)
      ) : (
        <div className="w-full bg-track" />
      )}
    </div>
  );
}

// Meter is a filled track: utilisation, budget used.
export function Meter({
  ratio,
  color = "var(--color-clay)",
  height = 6,
  tick,
  className,
}: {
  ratio: number;
  color?: string;
  height?: number;
  // Optional marker position, 0..1.
  tick?: number;
  className?: string;
}) {
  return (
    <div className={cn("relative w-full rounded-full bg-track", className)} style={{ height }}>
      <div className="absolute inset-y-0 left-0 rounded-full" style={{ width: `${Math.min(100, Math.max(0, ratio * 100))}%`, background: color }} />
      {tick !== undefined && (
        <span
          className="absolute w-0.5 rounded-[1px] bg-ink"
          style={{ left: `${Math.min(100, Math.max(0, tick * 100))}%`, top: -3, height: height + 6 }}
        />
      )}
    </div>
  );
}

export interface MonthCell {
  day: ISODate | null;
}

// monthCells lays a month out Monday-first in whole weeks, padding with
// empty cells before the 1st and after the last day.
export function monthCells(anyDay: ISODate): MonthCell[] {
  const first = monthStart(anyDay);
  const cells: MonthCell[] = Array.from({ length: weekdayMon0(first) }, () => ({ day: null }));
  for (const d of eachDay(first, addDays(first, daysInMonth(first) - 1))) cells.push({ day: d });
  while (cells.length % 7) cells.push({ day: null });
  return cells;
}

export const WEEKDAY_INITIALS = ["M", "T", "W", "T", "F", "S", "S"];
export const WEEKDAY_SHORT = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
