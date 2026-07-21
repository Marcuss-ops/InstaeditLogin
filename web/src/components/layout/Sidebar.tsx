import { useId } from "react";
import { Link, useLocation } from "react-router-dom";
import {
  LayoutDashboard,
  Link2,
  FileText,
  PenSquare,
  FolderInput,
  Calendar,
  FolderTree,
  BarChart3,
  LogOut,
  ChevronLeft,
  ChevronRight,
} from "lucide-react";
import { cn } from "../../lib/utils";
import { logout } from "../../lib/auth";

const navItems = [
  { to: "/app/dashboard", label: "Dashboard", icon: LayoutDashboard },
  { to: "/app/performance", label: "Performance", icon: BarChart3 },
  { to: "/app/uploads", label: "Imports", icon: FolderInput },
  { to: "/app/calendar", label: "Calendar", icon: Calendar },
  { to: "/app/groups", label: "Groups", icon: FolderTree },
  { to: "/app/linking", label: "Linking", icon: Link2 },
  { to: "/app/posts", label: "Posts", icon: FileText },
  { to: "/app/compose", label: "Editor", icon: PenSquare },
];

export type SidebarProps = {
  collapsed: boolean;
  onToggle: () => void;
};

export function Sidebar({ collapsed, onToggle }: SidebarProps) {
  const location = useLocation();
  const gradientId = useId();

  return (
    <aside
      className={cn(
        "h-screen flex flex-col bg-[#030308] border-r border-white/[0.08] transition-[width] duration-300 ease-in-out shrink-0",
        collapsed ? "w-16" : "w-64",
      )}
    >
      <div className="h-16 flex items-center justify-between px-4 border-b border-white/[0.08]">
        <Link
          to="/app/dashboard"
          className={cn(
            "flex items-center gap-2.5 font-bold text-[17px] tracking-[-0.3px] text-white no-underline transition-opacity overflow-hidden",
            collapsed && "opacity-0 pointer-events-none w-0",
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
          onClick={onToggle}
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          className="p-1.5 rounded-lg text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] transition-colors"
        >
          {collapsed ? <ChevronRight size={18} /> : <ChevronLeft size={18} />}
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
                collapsed && "justify-center",
              )}
              title={collapsed ? item.label : undefined}
            >
              <Icon size={20} className="shrink-0" />
              {!collapsed && <span className="truncate">{item.label}</span>}
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
            collapsed && "justify-center",
          )}
          title={collapsed ? "Log out" : undefined}
        >
          <LogOut size={20} className="shrink-0" />
          {!collapsed && <span className="truncate">Log out</span>}
        </button>
      </div>
    </aside>
  );
}
