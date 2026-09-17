import { diffDays, fmtRelativeDay, type ISODate } from "@/lib/dates";
import { fmt } from "@/lib/money";

import { category } from "@shared/categories";

// Notes are the short observations under "Worth a look". They are plain
// data (text runs) so the rules stay testable and the page decides how a
// strong run or a link looks.

export type Run = string | { strong: string } | { link: string; to: string };

export interface Note {
  id: string;
  // Higher shows first.
  weight: number;
  runs: Run[];
}

export interface NoteInput {
  currency: string;
  today: ISODate;
  isCurrentMonth: boolean;
  // Spent so far this month and the usual amount by this day, per category.
  spent: Map<string, number>;
  usual: Map<string, number | null>;
  fixed: (category: string) => boolean;
  budgetTotal: number;
  projected: number;
  priceChanges: { name: string; from: number; to: number; next: ISODate | null; yearly: number }[];
  needsCategory: number;
  utilization: number | null;
  reconnect: string[];
}

export function notes(input: NoteInput): Note[] {
  const out: Note[] = [];
  const money = (n: number, d = 0) => fmt(n, d, input.currency);

  for (const name of input.reconnect) {
    out.push({
      id: `reconnect-${name}`,
      weight: 100,
      runs: [{ strong: name }, " needs you to sign in again before it can sync. ", { link: "Reconnect", to: "/accounts" }],
    });
  }

  if (input.isCurrentMonth) {
    // The category furthest above its usual pace, if it is meaningfully so.
    let worst: { cat: string; ratio: number; spent: number; usual: number } | null = null;
    for (const [cat, spent] of input.spent) {
      const usual = input.usual.get(cat);
      if (input.fixed(cat) || usual === null || usual === undefined || usual < 25) continue;
      const ratio = spent / usual - 1;
      if (ratio >= 0.2 && spent - usual >= 50 && (!worst || ratio > worst.ratio)) worst = { cat, ratio, spent, usual };
    }
    if (worst) {
      out.push({
        id: "pace",
        weight: 60 + Math.min(30, worst.ratio * 20),
        runs: [
          `${category(worst.cat).label} is running `,
          { strong: `${Math.round(worst.ratio * 100)}% above` },
          ` your usual pace: ${money(worst.spent)} so far, against ${money(worst.usual)} by this point in a typical month.`,
        ],
      });
    }

    if (input.budgetTotal > 0 && input.projected > input.budgetTotal * 1.02) {
      out.push({
        id: "over-budget",
        weight: 70,
        runs: ["At this pace the month ends ", { strong: `${money(input.projected - input.budgetTotal)} over` }, " your budget."],
      });
    }
  }

  for (const c of input.priceChanges) {
    const up = c.to > c.from;
    const when = c.next ? (diffDays(input.today, c.next) >= 0 ? ` at ${possessive(fmtRelativeDay(c.next, input.today))} renewal` : "") : "";
    const yearly = Math.abs(c.to - c.from) * (c.yearly / Math.max(c.to, 0.01));
    out.push({
      id: `price-${c.name}`,
      weight: up ? 55 : 30,
      runs: [
        `${c.name} ${when ? "goes" : "went"} from ${money(c.from, 2)} to `,
        { strong: money(c.to, 2) },
        `${when}. That’s ${money(yearly)} ${up ? "more" : "less"} a year.`,
      ],
    });
  }

  if (input.needsCategory > 0) {
    out.push({
      id: "uncategorized",
      weight: 40,
      runs: [
        { strong: `${input.needsCategory} transaction${input.needsCategory === 1 ? "" : "s"}` },
        ` need${input.needsCategory === 1 ? "s" : ""} a category. `,
        { link: "Sort them now", to: "/spending?focus=sort" },
      ],
    });
  }

  if (input.utilization !== null && input.utilization > 0.3) {
    out.push({
      id: "utilization",
      weight: 35,
      runs: ["Your cards are ", { strong: `${Math.round(input.utilization * 100)}% used` }, ". Keeping it under 30% helps your credit score."],
    });
  }

  return out.sort((a, b) => b.weight - a.weight);
}

function possessive(day: string): string {
  return day === "Today" || day === "Tomorrow" ? `${day.toLowerCase()}’s` : `${day}’s`;
}
