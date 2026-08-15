import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Outlet } from "react-router-dom";
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
  const [collapsed, setCollapsed] = useState(true);

  const handleToggle = useCallback(() => {
    setCollapsed((value) => !value);
  }, []);

  useEffect(() => {
    const tick = () => void maybeRefreshSession();
    tick();
    const id = setInterval(tick, HEARTBEAT_CHECK_MS);
    return () => clearInterval(id);
  }, []);

  return (
    <div className="h-screen w-full flex bg-[#030308] text-[#e8e8ef] overflow-hidden">
      <Sidebar collapsed={collapsed} onToggle={handleToggle} />
      <div className="flex min-h-0 min-w-0 flex-1 flex-col bg-[#030308]">
        <header className="h-16 flex-none flex items-center justify-end gap-3 px-6 border-b border-white/[0.08] bg-[#030308]/80 backdrop-blur-sm">
          <NotificationCenter />
          <AccountSwitcher />
        </header>
        <main className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain bg-[#030308]">
          {children ?? <Outlet />}
        </main>
      </div>
    </div>
  );
}
