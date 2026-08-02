import { useCallback, useState, type ReactNode } from "react";
import { Outlet } from "react-router-dom";
import { Sidebar } from "./Sidebar";
import { AccountSwitcher } from "./AccountSwitcher";

export function InternalLayout({ children }: { children?: ReactNode }) {
  const [collapsed, setCollapsed] = useState(true);

  const handleToggle = useCallback(() => {
    setCollapsed((value) => !value);
  }, []);

  return (
    <div className="h-screen w-full flex bg-[#030308] text-[#e8e8ef] overflow-hidden">
      <Sidebar collapsed={collapsed} onToggle={handleToggle} />
      <div className="flex min-h-0 min-w-0 flex-1 flex-col bg-[#030308]">
        <header className="h-16 flex-none flex items-center justify-end px-6 border-b border-white/[0.08] bg-[#030308]/80 backdrop-blur-sm">
          <AccountSwitcher />
        </header>
        <main className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain bg-[#030308]">
          {children ?? <Outlet />}
        </main>
      </div>
    </div>
  );
}
