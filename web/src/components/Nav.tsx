import { useState } from "react";
import { Link, useLocation } from "react-router-dom";
import { Menu, X, LogOut } from "lucide-react";
import { cn } from "../lib/utils";
import { logout } from "../lib/auth";

const links = [
  { to: "/accounts", label: "Accounts" },
  { to: "/compose", label: "Compose" },
  { to: "/posts", label: "Posts" },
  { to: "/settings/api", label: "Settings" },
];

export function Nav() {
  const [open, setOpen] = useState(false);
  const location = useLocation();

  return (
    <nav className="sticky top-0 z-50 bg-white border-b border-neutral-200">
      <div className="max-w-[1200px] mx-auto px-5 h-14 flex items-center justify-between gap-4">
        <Link to="/accounts" className="flex items-center gap-2.5 font-bold text-[17px] tracking-[-0.3px] text-black no-underline">
          <svg width="26" height="26" viewBox="0 0 28 28" fill="none" className="shrink-0" aria-hidden="true">
            <rect width="28" height="28" rx="7" fill="url(#nav-logo-grad)" />
            <path d="M14.5 5L7 15h5l-1.5 8L21 13h-5l1.5-8h-3z" fill="white" fillOpacity="0.95" />
            <defs>
              <linearGradient id="nav-logo-grad" x1="0" y1="0" x2="28" y2="28">
                <stop stopColor="#0A84FF" />
                <stop offset="1" stopColor="#7B61FF" />
              </linearGradient>
            </defs>
          </svg>
          InstaEdit
        </Link>

        {/* Desktop links */}
        <div className="hidden md:flex items-center gap-1">
          {links.map((l) => {
            const active = location.pathname === l.to || location.pathname.startsWith(l.to + "/");
            return (
              <Link
                key={l.to}
                to={l.to}
                className={cn(
                  "px-3 py-1.5 rounded-lg text-sm font-medium no-underline transition-colors",
                  active
                    ? "bg-neutral-100 text-black"
                    : "text-neutral-500 hover:text-black hover:bg-neutral-50"
                )}
              >
                {l.label}
              </Link>
            );
          })}
          <button
            type="button"
            onClick={() => logout("/login")}
            className="ml-2 inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium text-neutral-400 hover:text-red-600 hover:bg-red-50 transition-colors"
          >
            <LogOut size={14} />
            Log out
          </button>
        </div>

        {/* Mobile toggle */}
        <button
          onClick={() => setOpen(!open)}
          className="md:hidden p-2 text-neutral-600"
          aria-label="Toggle menu"
          data-testid="mobile-menu-toggle"
        >
          {open ? <X size={22} /> : <Menu size={22} />}
        </button>
      </div>

      {/* Mobile menu */}
      <div
        data-testid="mobile-menu"
        className={cn(
          "md:hidden border-t border-neutral-100 bg-white flex-col px-5 pb-4 pt-2 gap-0",
          open ? "flex" : "hidden"
        )}
      >
        {links.map((l) => (
          <Link
            key={l.to}
            to={l.to}
            onClick={() => setOpen(false)}
            className="py-3.5 text-sm font-medium border-b border-neutral-100 last:border-b-0 no-underline text-neutral-700"
          >
            {l.label}
          </Link>
        ))}
        <button
          type="button"
          onClick={() => { setOpen(false); logout("/login"); }}
          className="py-3.5 text-sm font-medium text-left text-red-500 no-underline border-b border-neutral-100"
        >
          Log out
        </button>
      </div>
    </nav>
  );
}
