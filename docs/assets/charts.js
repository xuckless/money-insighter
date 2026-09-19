// Charts for the documentation site, drawn the way the app draws its own.
//
// The app has no chart library on purpose: its charts are plain SVG stretched
// to their box with preserveAspectRatio="none" and non-scaling strokes, with
// the labels laid over in HTML so text is never distorted. See
// desktop/src/renderer/src/components/charts/chart.tsx, which these functions
// are a plain-JavaScript port of. A charting library here would be a larger
// dependency that drew something that did not look like the product.

const VIEW_W = 1000;

// scale maps a value in [min, max] onto a y coordinate in [height, 0].
export function scale(height, min, max) {
  const span = max - min || 1;
  return (v) => height - ((v - min) / span) * height;
}

// ticks returns count + 1 evenly spaced values from min to max.
export function ticks(min, max, count) {
  return Array.from({ length: count + 1 }, (_, i) => min + ((max - min) * i) / count);
}

const px = (points) =>
  points.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(2)} ${y.toFixed(2)}`).join(" ");

// linePath draws values across the full width.
export function linePath(values, y) {
  const step = values.length > 1 ? VIEW_W / (values.length - 1) : 0;
  return px(values.map((v, i) => [i * step, y(v)]));
}

// areaPath is linePath closed down to the baseline, for a filled band.
export function areaPath(values, y, height) {
  return `${linePath(values, y)} L${VIEW_W} ${height} L0 ${height} Z`;
}

const svgEl = (name, attrs) => {
  const el = document.createElementNS("http://www.w3.org/2000/svg", name);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, String(v));
  return el;
};

// An SVG <title> is what a screen reader reads for a role="img" graphic.
// Note that append() returns undefined, so the node is built first.
const title = (text) => {
  const el = svgEl("title", {});
  el.textContent = text;
  return el;
};

const money = (v, currency) =>
  new Intl.NumberFormat("en-CA", { style: "currency", currency, maximumFractionDigits: 0 }).format(v);

// frame builds the <svg> every chart shares: gridlines, then the marks a
// caller adds. Strokes are vector-effect="non-scaling-stroke" so that the
// horizontal squash of preserveAspectRatio="none" does not thicken them.
function frame(height, gridValues, y) {
  const svg = svgEl("svg", {
    viewBox: `0 0 ${VIEW_W} ${height}`,
    preserveAspectRatio: "none",
    height,
    role: "img",
  });
  for (const v of gridValues) {
    svg.append(
      svgEl("line", {
        x1: 0, x2: VIEW_W, y1: y(v), y2: y(v),
        stroke: "var(--grid)", "stroke-width": 1, "vector-effect": "non-scaling-stroke",
      }),
    );
  }
  return svg;
}

// netWorthChart draws a filled line of daily net worth.
export function netWorthChart(host, series, currency) {
  if (!series.length) return;
  const height = 220;
  const values = series.map(([, v]) => v);
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  // A little headroom so the line never touches the frame.
  const pad = (hi - lo) * 0.12 || 1;
  const min = Math.min(0, lo - pad);
  const max = hi + pad;
  const y = scale(height, min, max);

  const svg = frame(height, ticks(min, max, 4), y);
  svg.append(svgEl("path", { d: areaPath(values, y, height), fill: "var(--moss-soft)", opacity: 0.75 }));
  svg.append(
    svgEl("path", {
      d: linePath(values, y),
      fill: "none", stroke: "var(--moss)", "stroke-width": 1.5,
      "stroke-linejoin": "round", "vector-effect": "non-scaling-stroke",
    }),
  );
  svg.append(
    title(
      `Net worth from ${series[0][0]} to ${series[series.length - 1][0]}, ` +
        `${money(values[0], currency)} to ${money(values[values.length - 1], currency)}.`,
    ),
  );

  const plot = host.querySelector(".plot");
  plot.replaceChildren(svg);

  const axis = host.querySelector(".axis");
  if (axis) {
    const month = (d) => new Date(`${d}T00:00:00Z`).toLocaleDateString("en-CA", { month: "short", timeZone: "UTC" });
    axis.replaceChildren(
      ...[0, Math.floor(series.length / 3), Math.floor((series.length * 2) / 3), series.length - 1].map((i) => {
        const s = document.createElement("span");
        s.textContent = month(series[i][0]);
        return s;
      }),
    );
  }
  const value = host.querySelector(".value");
  if (value) value.textContent = money(values[values.length - 1], currency);
}

// categoryChart draws one stacked bar per month, coloured by category, using
// the same category colours the app stores in topper.categories.
export function categoryChart(host, months, categories, currency) {
  const spending = new Set(categories.filter((c) => c.kind === "spending").map((c) => c.id));
  const byMonth = new Map();
  for (const row of months) {
    if (!spending.has(row.category) || row.amount <= 0) continue;
    if (!byMonth.has(row.month)) byMonth.set(row.month, new Map());
    byMonth.get(row.month).set(row.category, row.amount);
  }
  const keys = [...byMonth.keys()].sort();
  if (!keys.length) return;

  // The six biggest categories keep their identity; the rest become "Other",
  // because a stack of nineteen colours tells nobody anything.
  const totals = new Map();
  for (const m of byMonth.values()) {
    for (const [cat, amt] of m) totals.set(cat, (totals.get(cat) ?? 0) + amt);
  }
  const top = [...totals.entries()].sort((a, b) => b[1] - a[1]).slice(0, 6).map(([c]) => c);
  const label = new Map(categories.map((c) => [c.id, c]));
  const shown = [...top, "__rest"];
  const colourOf = (id) => (id === "__rest" ? "var(--stone)" : (label.get(id)?.color ?? "var(--stone)"));
  const labelOf = (id) => (id === "__rest" ? "Everything else" : (label.get(id)?.label ?? id));

  const stacks = keys.map((month) => {
    const row = byMonth.get(month);
    const parts = top.map((c) => [c, row.get(c) ?? 0]);
    const rest = [...row].filter(([c]) => !top.includes(c)).reduce((n, [, v]) => n + v, 0);
    return { month, parts: [...parts, ["__rest", rest]] };
  });

  const height = 220;
  const max = Math.max(...stacks.map((s) => s.parts.reduce((n, [, v]) => n + v, 0))) * 1.08 || 1;
  const y = scale(height, 0, max);
  const svg = frame(height, ticks(0, max, 4), y);

  const slot = VIEW_W / stacks.length;
  const barW = slot * 0.62;
  stacks.forEach((stack, i) => {
    let acc = 0;
    for (const [cat, amount] of stack.parts) {
      if (amount <= 0) continue;
      const top0 = y(acc + amount);
      const bottom = y(acc);
      svg.append(
        svgEl("rect", {
          x: i * slot + (slot - barW) / 2,
          y: top0,
          width: barW,
          height: Math.max(1, bottom - top0),
          fill: colourOf(cat),
        }),
      );
      acc += amount;
    }
  });
  svg.append(title(`Spending by category for each of the last ${stacks.length} months.`));

  host.querySelector(".plot").replaceChildren(svg);

  const axis = host.querySelector(".axis");
  if (axis) {
    const short = (m) => new Date(`${m}-01T00:00:00Z`).toLocaleDateString("en-CA", { month: "short", timeZone: "UTC" });
    axis.replaceChildren(
      ...stacks.map((s) => {
        const el = document.createElement("span");
        el.textContent = short(s.month);
        return el;
      }),
    );
  }
  const legend = host.querySelector(".legend");
  if (legend) {
    legend.replaceChildren(
      ...shown.map((id) => {
        const span = document.createElement("span");
        const swatch = document.createElement("i");
        swatch.style.background = colourOf(id);
        span.append(swatch, document.createTextNode(labelOf(id)));
        return span;
      }),
    );
  }

  // A table alternative, so the chart is not a wall of nothing to a screen
  // reader. The app's own charts carry an aria-label summary; here there is
  // room to give the numbers.
  const table = host.querySelector("table tbody");
  if (table) {
    table.replaceChildren(
      ...stacks.slice(-6).map((s) => {
        const tr = document.createElement("tr");
        const total = s.parts.reduce((n, [, v]) => n + v, 0);
        for (const text of [s.month, money(total, currency)]) {
          const td = document.createElement("td");
          td.textContent = text;
          tr.append(td);
        }
        return tr;
      }),
    );
  }
}
