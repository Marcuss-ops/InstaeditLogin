import { useState } from "react";
import { Link } from "react-router-dom";
import { Menu, X } from "lucide-react";
import { cn } from "../lib/utils";

const links = [
  { to: "/login", label: "Get started" },
];

export function Nav() {
  const [open, setOpen] = useState(false);

  return (
    <nav className="sticky top-0 z-50 backdrop-blur-[16px] bg-[rgba(3,3,8,0.7)] border-b border-white/12">
      <div className="max-w-[1200px] mx-auto px-5 h-16 flex items-center justify-between gap-4">
        <Link to="/" className="font-bold text-[18px] tracking-[-0.3px] text-white no-underline">
          InstaEdit
        </Link>

        {/* Desktop links */}
        <div className="hidden md:flex items-center gap-4">
          <Link
            to="/login"
            className="inline-flex items-center gap-2 px-[18px] py-[10px] rounded-xl text-sm font-semibold bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] text-white no-underline hover:-translate-y-[1px] hover:shadow-[0_0_32px_rgba(123,97,255,0.45)] transition-all shadow-[0_0_24px_rgba(123,97,255,0.3)]"
          >
            Get started
          </Link>
        </div>

        {/* Mobile toggle */}
        <button
          onClick={() => setOpen(!open)}
          className="md:hidden p-2 text-white"
          aria-label="Toggle menu"
          data-testid="mobile-menu-toggle"
        >
          {open ? <X size={24} /> : <Menu size={24} />}
        </button>
      </div>

      {/* Mobile menu */}
      <div
        data-testid="mobile-menu"
        className={cn(
          "md:hidden border-t border-white/12 bg-[rgba(3,3,8,0.95)] backdrop-blur-[16px] flex-col px-5 pb-4 pt-2 gap-0",
          open ? "flex" : "hidden"
        )}
      >
        {links.map((l) => (
          <Link
            key={l.to}
            to={l.to}
            onClick={() => setOpen(false)}
            className="py-3.5 text-sm font-semibold border-b border-white/8 last:border-b-0 no-underline text-[#e8e8ef]"
          >
            {l.label}
          </Link>
        ))}
      </div>
    </nav>
  );
}
