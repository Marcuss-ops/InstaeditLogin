import { useState, useCallback, useEffect } from "react";
import { Link } from "react-router-dom";
import { Menu, X, Zap } from "lucide-react";

type NavLink = { label: string; to?: string; href?: string };

const DEFAULT_LINKS: NavLink[] = [
  { label: "Come funziona", href: "#pipeline" },
  { label: "Workflow", href: "#workflow" },
  { label: "Features", href: "#features" },
  { label: "Agenzie", href: "#agency" },
  { label: "Programmi", to: "/programs" },
  { label: "Chi siamo", href: "#who-are-we" },
];

export function MarketingNav({ links = DEFAULT_LINKS }: { links?: NavLink[] }) {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (open) {
      document.body.style.overflow = "hidden";
    } else {
      document.body.style.overflow = "";
    }
    return () => { document.body.style.overflow = ""; };
  }, [open]);

  const close = useCallback(() => setOpen(false), []);

  return (
    <nav className="fixed top-0 left-0 right-0 z-50">
      <div className="surface-glass border-b border-white/10">
        <div className="mx-auto max-w-7xl h-16 px-6 flex items-center justify-between">
          <Link to="/" className="flex items-center gap-2 group" onClick={close}>
            <span className="inline-flex w-7 h-7 items-center justify-center rounded-md bg-white text-black shadow-[0_0_24px_-6px_rgba(255,255,255,0.4)] group-hover:shadow-[0_0_32px_-4px_rgba(255,255,255,0.6)] transition-shadow">
              <Zap className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-white text-sm">InstaEdit</span>
          </Link>

          <div className="hidden md:flex items-center gap-7 text-sm font-medium text-zinc-400">
            {links.map((l) =>
              l.to ? (
                <Link
                  key={l.label}
                  to={l.to}
                  className="hover:text-white transition-colors relative after:absolute after:bottom-[-2px] after:left-0 after:h-[2px] after:w-0 after:bg-gradient-to-r after:from-violet-400 after:to-cyan-400 after:transition-all after:duration-300 hover:after:w-full"
                >
                  {l.label}
                </Link>
              ) : (
                <a
                  key={l.label}
                  href={l.href}
                  className="hover:text-white transition-colors relative after:absolute after:bottom-[-2px] after:left-0 after:h-[2px] after:w-0 after:bg-gradient-to-r after:from-violet-400 after:to-cyan-400 after:transition-all after:duration-300 hover:after:w-full"
                >
                  {l.label}
                </a>
              ),
            )}
          </div>

          <button
            type="button"
            onClick={() => setOpen(!open)}
            className="md:hidden p-2 text-zinc-400 hover:text-white transition-colors"
            aria-label={open ? "Close menu" : "Open menu"}
          >
            {open ? <X className="w-5 h-5" /> : <Menu className="w-5 h-5" />}
          </button>
        </div>

        {open && (
          <div
            className="md:hidden border-t border-white/10 bg-[#14141c]/98 backdrop-blur-xl"
            role="dialog"
            aria-modal="true"
            aria-label="Navigation menu"
          >
            <div className="px-6 py-4 space-y-1">
              {links.map((l) =>
                l.to ? (
                  <Link
                    key={l.label}
                    to={l.to}
                    onClick={close}
                    className="block py-3 text-sm font-medium text-zinc-300 hover:text-white hover:bg-white/[0.04] rounded-lg px-3 -mx-3 transition-colors"
                  >
                    {l.label}
                  </Link>
                ) : (
                  <a
                    key={l.label}
                    href={l.href}
                    onClick={close}
                    className="block py-3 text-sm font-medium text-zinc-300 hover:text-white hover:bg-white/[0.04] rounded-lg px-3 -mx-3 transition-colors"
                  >
                    {l.label}
                  </a>
                ),
              )}
              <hr className="border-white/10 my-3" />
              <Link
                to="/login"
                onClick={close}
                className="block py-3 text-sm font-semibold text-center text-white bg-gradient-to-r from-violet-500 to-cyan-500 rounded-xl hover:opacity-90 transition-opacity"
              >
                Accedi
              </Link>
            </div>
          </div>
        )}
      </div>
    </nav>
  );
}
