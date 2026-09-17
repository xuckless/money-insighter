import { ArrowLeftRight, Landmark, LayoutDashboard, Link2, ScrollText, Settings } from "lucide-react";
import { NavLink } from "react-router";

import { cn } from "@/lib/utils";

const links = [
  { to: "/", label: "Overview", icon: LayoutDashboard },
  { to: "/accounts", label: "Accounts", icon: Landmark },
  { to: "/transactions", label: "Transactions", icon: ArrowLeftRight },
  { to: "/connections", label: "Connections", icon: Link2 },
] as const;

const secondary = [
  { to: "/settings", label: "Settings", icon: Settings },
  { to: "/logs", label: "Logs", icon: ScrollText },
] as const;

function Item({ to, label, icon: Icon }: { to: string; label: string; icon: typeof Landmark }) {
  return (
    <NavLink
      to={to}
      end={to === "/"}
      className={({ isActive }) =>
        cn(
          "flex items-center gap-2 rounded-md px-3 py-2 text-sm whitespace-nowrap transition-colors",
          isActive
            ? "bg-muted font-medium text-foreground"
            : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
        )
      }
    >
      <Icon className="size-4" />
      {label}
    </NavLink>
  );
}

export function AppNav() {
  return (
    <aside className="flex border-b bg-background md:sticky md:top-0 md:h-screen md:w-56 md:shrink-0 md:flex-col md:border-r md:border-b-0">
      <div className="flex items-center gap-2 px-4 py-4 md:px-5 md:py-6">
        <div className="flex size-7 items-center justify-center rounded-md bg-primary text-xs font-semibold text-primary-foreground">
          MI
        </div>
        <span className="font-semibold tracking-tight">Money Insighter</span>
      </div>
      <nav className="flex flex-1 gap-1 overflow-x-auto px-2 pb-2 md:flex-col md:px-3">
        {links.map((l) => (
          <Item key={l.to} {...l} />
        ))}
        <div className="hidden flex-1 md:block" />
        {secondary.map((l) => (
          <Item key={l.to} {...l} />
        ))}
      </nav>
    </aside>
  );
}
