import { useState } from "react";
import { Link, useLocation } from "react-router-dom";
import { Menu, X } from "lucide-react";
import { cn } from "../lib/utils";

const links = [
  { href: "/#funzioni", label: "Funzioni" },
  { href: "/#come-funziona", label: "Come funziona" },
  { href: "/#prezzi", label: "Prezzi" },
];

export function Nav() {
  const [open, setOpen] = useState(false);
  const location = useLocation();
  const isHome = location.pathname === "/";

  const scrollTo = (e: React.MouseEvent<HTMLAnchorElement>, href: string) => {
    const id = href.split("#")[1];
    if (!id || !isHome) return;
    e.preventDefault();
    setOpen(false);
    document.querySelector(`#${id}`)?.scrollIntoView({ behavior: "smooth" });
  };

  return (
    <nav className="sticky top-0 z-50 bg-white/85 backdrop-blur-xl border-b border-neutral-100">
      <div className="max-w-[1100px] mx-auto px-6 h-16 flex items-center justify-between gap-4">
        <Link to="/" className="font-extrabold text-[19px] tracking-tight no-underline text-black">
          SocialSync
        </Link>

        {/* Desktop links */}
        <div className="hidden md:flex items-center gap-7">
          {links.map((l) => (
            <Link
              key={l.href}
              to={l.href}
              onClick={(e) => scrollTo(e, l.href)}
              className="text-sm font-medium text-black/70 hover:text-black transition-colors no-underline"
            >
              {l.label}
            </Link>
          ))}
          <Link
            to="/login"
            className="text-sm font-medium text-black/70 hover:text-black transition-colors no-underline"
          >
            Accedi
          </Link>
        </div>

        {/* Desktop CTA */}
        <div className="hidden md:block">
          <Link
            to="/login"
            className="inline-flex items-center gap-2 px-[18px] py-[10px] rounded-xl text-sm font-semibold bg-black text-white no-underline hover:-translate-y-[1px] hover:bg-neutral-900 transition-all"
          >
            Inizia gratis
          </Link>
        </div>

        {/* Mobile toggle */}
        <button
          onClick={() => setOpen(!open)}
          className="md:hidden p-2"
          aria-label="Menu"
        >
          {open ? <X size={24} /> : <Menu size={24} />}
        </button>
      </div>

      {/* Mobile menu */}
      <div
        className={cn(
          "md:hidden border-b border-neutral-100 bg-white flex-col px-6 pb-4 pt-2 gap-0",
          open ? "flex" : "hidden"
        )}
      >
        {links.map((l) => (
          <Link
            key={l.href}
            to={l.href}
            onClick={(e) => { scrollTo(e, l.href); setOpen(false); }}
            className="py-3.5 text-sm font-medium border-b border-neutral-100 last:border-b-0 no-underline text-black"
          >
            {l.label}
          </Link>
        ))}
        <Link
          to="/login"
          onClick={() => setOpen(false)}
          className="py-3.5 text-sm font-medium border-b border-neutral-100 last:border-b-0 no-underline text-black"
        >
          Accedi
        </Link>
        <Link
          to="/login"
          onClick={() => setOpen(false)}
          className="mt-3 inline-flex items-center justify-center gap-2 px-[18px] py-[10px] rounded-xl text-sm font-semibold bg-black text-white no-underline"
        >
          Inizia gratis
        </Link>
      </div>
    </nav>
  );
}
