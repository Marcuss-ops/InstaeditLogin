import { useState, type ReactNode } from "react";
import { Outlet } from "react-router-dom";
import { Sidebar } from "./Sidebar";
import { AccountSwitcher } from "./AccountSwitcher";

export function InternalLayout({ children }: { children?: ReactNode }) {
  const [collapsed, setCollapsed] = useState(false);

  return (
    <div className="h-screen w-full flex bg-[#030308] text-[#e8e8ef] overflow-hidden">
      <Sidebar collapsed={collapsed} onToggle={() => setCollapsed((v) => !v)} />
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
