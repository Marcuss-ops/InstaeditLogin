import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Outlet } from "react-router-dom";
import { Sidebar } from "./Sidebar";
import { AccountSwitcher } from "./AccountSwitcher";

const AUTO_COLLAPSE_MS = 5000;

export function InternalLayout({ children }: { children?: ReactNode }) {
  const [collapsed, setCollapsed] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const resetTimer = useCallback(() => {
    if (timerRef.current) clearTimeout(timerRef.current);
    timerRef.current = setTimeout(() => setCollapsed(true), AUTO_COLLAPSE_MS);
  }, []);

  // Start the auto-collapse timer on mount and reset when sidebar is expanded.
  useEffect(() => {
    if (!collapsed) resetTimer();
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [collapsed, resetTimer]);

  const handleToggle = useCallback(() => {
    setCollapsed((v) => {
      const next = !v;
      if (!next) resetTimer();
      return next;
    });
  }, [resetTimer]);

  // Reset the timer on any mouse movement inside the sidebar area
  // so it stays open while the user is interacting.
  const handleMouseMove = useCallback(() => {
    if (!collapsed) resetTimer();
  }, [collapsed, resetTimer]);

  return (
    <div className="h-screen w-full flex bg-[#030308] text-[#e8e8ef] overflow-hidden" onMouseMove={handleMouseMove}>
      <Sidebar collapsed={collapsed} onToggle={handleToggle} />
      <div className="flex-1 flex flex-col min-w-0">
        <header className="h-16 flex-none flex items-center justify-end px-6 border-b border-white/[0.08] bg-[#030308]/80 backdrop-blur-sm">
          <AccountSwitcher />
        </header>
        <main className="flex-1 min-w-0 overflow-y-auto">
          {children ?? <Outlet />}
        </main>
      </div>
    </div>
  );
}
