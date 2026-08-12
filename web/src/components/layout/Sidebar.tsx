import { useEffect, useId, useState } from "react";
import { Link, useLocation } from "react-router-dom";
import type { LucideIcon } from "lucide-react";
import { FUNCTIONAL_ICON_CATALOG } from "../icons/FunctionalIcons";
import { cn } from "../../lib/utils";
import { fetchSession, logout } from "../../lib/auth";
import { useActiveLiveCount } from "../../hooks/useActiveLiveCount";

type NavItem = {
  to: string;
  label: string;
  icon: LucideIcon;
  /** When set, the item renders a badge with the count of actually-live streams. */
  liveCountBadge?: boolean;
};

const baseNavItems: NavItem[] = [
  { to: "/app/dashboard", label: "Dashboard", icon: FUNCTIONAL_ICON_CATALOG.product.dashboard },
  { to: "/app/performance", label: "Performance", icon: FUNCTIONAL_ICON_CATALOG.product.analytics },
  { to: "/app/calendar", label: "Calendar", icon: FUNCTIONAL_ICON_CATALOG.product.calendar },
  { to: "/app/content/inbox", label: "Content Inbox", icon: FUNCTIONAL_ICON_CATALOG.product.folder },
  { to: "/app/groups", label: "Groups", icon: FUNCTIONAL_ICON_CATALOG.product.folder },
  { to: "/app/covers", label: "Copertine", icon: FUNCTIONAL_ICON_CATALOG.product.image },
  { to: "/app/livestreams", label: "Live streaming", icon: FUNCTIONAL_ICON_CATALOG.product.live, liveCountBadge: true },
  { to: "/app/linking", label: "Linking", icon: FUNCTIONAL_ICON_CATALOG.product.link },
];

const adminNavItem: NavItem = { to: "/admin/dashboard", label: "Admin", icon: FUNCTIONAL_ICON_CATALOG.product.admin };

export type SidebarProps = {
  collapsed: boolean;
  onToggle: () => void;
};

export function Sidebar({ collapsed, onToggle }: SidebarProps) {
  const location = useLocation();
  const gradientId = useId();
  const [isAdmin, setIsAdmin] = useState(false);
  const [hovered, setHovered] = useState(false);
  const visualCollapsed = collapsed && !hovered;
  const activeLives = useActiveLiveCount();

  useEffect(() => {
    let mounted = true;
    void (async () => {
      const session = await fetchSession();
      if (mounted) {
        setIsAdmin(session?.isAdmin ?? false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, []);

  const navItems = isAdmin ? [...baseNavItems, adminNavItem] : baseNavItems;

  return (
    <aside
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      className={cn(
        "h-screen flex flex-col bg-[#030308] border-r border-white/[0.08] transition-[width] duration-300 ease-in-out shrink-0",
        visualCollapsed ? "w-16" : "w-64",
      )}
    >
      <div className="h-16 flex items-center justify-between px-4 border-b border-white/[0.08]">
        <Link
          to="/app/dashboard"
          className={cn(
            "flex items-center gap-2.5 font-bold text-[17px] tracking-[-0.3px] text-white no-underline transition-opacity overflow-hidden",
            visualCollapsed && "opacity-0 pointer-events-none w-0",
          )}
        >
          <svg width="26" height="26" viewBox="0 0 28 28" fill="none" className="shrink-0" aria-hidden="true">
            <rect width="28" height="28" rx="7" fill={`url(#${gradientId})`} />
            <path d="M14.5 5L7 15h5l-1.5 8L21 13h-5l1.5-8h-3z" fill="white" fillOpacity="0.95" />
            <defs>
              <linearGradient id={gradientId} x1="0" y1="0" x2="28" y2="28">
                <stop stopColor="#0A84FF" />
                <stop offset="1" stopColor="#7B61FF" />
              </linearGradient>
            </defs>
          </svg>
          InstaEdit
        </Link>

        <button
          type="button"
          onClick={() => {
            setHovered(false);
            onToggle();
          }}
          aria-label={visualCollapsed ? "Expand sidebar" : "Collapse sidebar"}
          className="p-1.5 rounded-lg text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] transition-colors"
        >
          {visualCollapsed ? (
            <FUNCTIONAL_ICON_CATALOG.navigation.next size={18} />
          ) : (
            <FUNCTIONAL_ICON_CATALOG.navigation.collapse size={18} />
          )}
        </button>
      </div>

      <nav className="flex-1 py-4 px-2 space-y-1 overflow-y-auto">
        {navItems.map((item) => {
          const Icon = item.icon;
          const active = location.pathname === item.to || location.pathname.startsWith(`${item.to}/`);
          return (
            <Link
              key={item.to}
              to={item.to}
              className={cn(
                "flex items-center gap-3 px-3 py-2.5 rounded-xl text-sm font-medium transition-all no-underline border",
                active
                  ? "bg-white/[0.08] text-white shadow-[inset_0_1px_0_0_rgba(255,255,255,0.1)] border-white/[0.08]"
                  : "text-[#9aa0aa] hover:text-white hover:bg-white/[0.04] border-transparent",
                visualCollapsed && "justify-center",
              )}
              title={visualCollapsed ? item.label : undefined}
              aria-label={visualCollapsed ? item.label : undefined}
              aria-current={active ? "page" : undefined}
            >
              <Icon size={20} className="shrink-0" />
              {!visualCollapsed && <span className="truncate">{item.label}</span>}
              {item.liveCountBadge &&
                !visualCollapsed &&
                activeLives !== null &&
                activeLives > 0 && (
                  <span
                    className="ml-auto inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-red-500 px-1.5 text-[10px] font-bold leading-none text-white"
                    title={`${activeLives} ${activeLives === 1 ? "live attiva" : "live attive"}`}
                  >
                    {activeLives}
                  </span>
                )}
            </Link>
          );
        })}
      </nav>

      <div className="p-2 border-t border-white/[0.08]">
        <button
          type="button"
          onClick={() => logout("/login")}
          className={cn(
            "flex items-center gap-3 px-3 py-2.5 rounded-xl text-sm font-medium text-[#9aa0aa] hover:text-red-400 hover:bg-red-500/[0.08] transition-colors w-full",
            visualCollapsed && "justify-center",
          )}
          title={visualCollapsed ? "Log out" : undefined}
          aria-label={visualCollapsed ? "Log out" : undefined}
        >
          <FUNCTIONAL_ICON_CATALOG.actions.logout size={20} className="shrink-0" />
          {!visualCollapsed && <span className="truncate">Log out</span>}
        </button>
      </div>
    </aside>
  );
}
