import { useCallback, useEffect, useState, type ReactNode } from "react";
import { ChevronRight, Moon, Search, Sun } from "lucide-react";
import { Outlet, useLocation } from "react-router-dom";
import { Sidebar } from "./Sidebar";
import { AccountSwitcher } from "./AccountSwitcher";
import { maybeRefreshSession } from "../../lib/session-refresh";
import { NotificationCenter } from "../../features/notifications/NotificationCenter";

/**
 * Keeps the session alive while the app is open: the access JWT in the
 * `session` cookie expires after ~15 minutes, so a proactive refresh
 * (throttled to once per 10 min, visible tabs only, cross-tab safe) is
 * scheduled here — the protected layout only mounts for authenticated
 * routes. Falls back to the reactive refresh-on-401 in the fetch
 * wrappers when the timer was missed (idle tab, hibernation).
 */
const HEARTBEAT_CHECK_MS = 60 * 1000;

export function InternalLayout({ children }: { children?: ReactNode }) {
  const [collapsed, setCollapsed] = useState(false);
  const [theme, setTheme] = useState<"light" | "dark">(() => {
    if (typeof window === "undefined") return "light";
    return window.localStorage.getItem("instaedit:app-theme") === "dark" ? "dark" : "light";
  });
  const location = useLocation();
  const isCalendarRoute =
    location.pathname === "/app/calendar" ||
    location.pathname === "/app/uploads/calendar";

  const pageTitle =
    location.pathname.includes("performance") ? "Performance" :
    location.pathname.includes("calendar") ? "Calendar" :
    location.pathname.includes("groups") ? "Groups" :
    location.pathname.includes("covers") ? "Copertine" :
    location.pathname.includes("livestream") ? "Live streaming" :
    location.pathname.includes("linking") ? "Connessioni" :
    location.pathname.includes("youtube") ? "YouTube Studio" :
    location.pathname.includes("upload") ? "Upload" :
    location.pathname.includes("admin") ? "Admin" :
    "Dashboard";

  const handleToggle = useCallback(() => {
    setCollapsed((value) => !value);
  }, []);

  useEffect(() => {
    const tick = () => void maybeRefreshSession();
    tick();
    const id = setInterval(tick, HEARTBEAT_CHECK_MS);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    window.localStorage.setItem("instaedit:app-theme", theme);
  }, [theme]);

  return (
    <div className="app-theme h-screen w-full flex overflow-hidden" data-theme={theme}>
      {!isCalendarRoute && <Sidebar collapsed={collapsed} onToggle={handleToggle} />}
      <div className="app-main flex min-h-0 min-w-0 flex-1 flex-col">
        <header className="app-topbar h-16 flex-none flex items-center justify-between gap-4 px-5 sm:px-7">
          <div className="flex min-w-0 items-center gap-3">
            <div className="app-window-controls hidden sm:flex" aria-hidden="true">
              <span className="app-window-dot app-window-dot-red" />
              <span className="app-window-dot app-window-dot-yellow" />
              <span className="app-window-dot app-window-dot-green" />
            </div>
            <div className="hidden min-w-0 items-center gap-1.5 text-[13px] sm:flex">
              <span className="app-topbar-muted">Workspace</span>
              <ChevronRight size={14} className="app-topbar-chevron" aria-hidden="true" />
              <span className="truncate font-semibold app-topbar-title">{pageTitle}</span>
            </div>
          </div>
          <div className="flex items-center gap-2.5">
            <button type="button" className="app-command-button hidden md:flex" aria-label="Search workspace">
              <Search size={15} aria-hidden="true" />
              <span>Quick find</span>
              <kbd>⌘ K</kbd>
            </button>
          <NotificationCenter />
          <button
            type="button"
            className="app-theme-toggle"
            onClick={() => setTheme((current) => current === "light" ? "dark" : "light")}
            aria-label={theme === "light" ? "Attiva tema scuro" : "Attiva tema chiaro"}
            title={theme === "light" ? "Tema scuro" : "Tema chiaro"}
          >
            {theme === "light" ? <Moon size={15} /> : <Sun size={15} />}
          </button>
          <AccountSwitcher />
          </div>
        </header>
        <main className="app-content min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain">
          {children ?? <Outlet />}
        </main>
      </div>
    </div>
  );
}
