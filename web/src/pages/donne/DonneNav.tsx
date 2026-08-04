import { useState, useCallback, useEffect } from "react";
import { Link } from "react-router-dom";
import { Menu, X, Calendar, Bot, Users } from "lucide-react";
import { useBooking } from "../../components/booking/BookingProvider";
import { NAV } from "./content";

/**
 * Navigazione autonoma della landing "HerChannel AI".
 * Volutamente separata da `MarketingNav` e `Nav`: la landing è un
 * progetto indipendente con la propria identità e la propria lingua.
 */
export function DonneNav() {
  const [open, setOpen] = useState(false);
  const { open: openBooking } = useBooking();

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
    return () => {
      document.body.style.overflow = "";
    };
  }, [open]);

  const close = useCallback(() => setOpen(false), []);

  return (
    <nav className="fixed top-0 left-0 right-0 z-50">
      <div className="surface-glass border-b border-white/10">
        <div className="mx-auto max-w-7xl h-16 px-6 flex items-center justify-between">
          <Link to="/donne" className="flex items-center gap-2 group" onClick={close}>
            <span className="inline-flex w-7 h-7 items-center justify-center rounded-md bg-gradient-to-br from-pink-500 to-rose-500 text-white shadow-[0_0_24px_-6px_rgba(244,63,94,0.5)] group-hover:shadow-[0_0_32px_-4px_rgba(244,63,94,0.7)] transition-shadow">
              <Bot className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-white text-sm">{NAV.brand}</span>
          </Link>

          <div className="hidden md:flex items-center gap-7 text-sm font-medium text-zinc-400">
            {NAV.links.map((l) => (
              <a
                key={l.label}
                href={l.href}
                onClick={close}
                className="hover:text-white transition-colors relative after:absolute after:bottom-[-2px] after:left-0 after:h-[2px] after:w-0 after:bg-gradient-to-r after:from-pink-400 after:to-rose-400 after:transition-all after:duration-300 hover:after:w-full"
              >
                {l.label}
              </a>
            ))}
          </div>

          <div className="hidden lg:flex items-center gap-2">
            <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full surface-glass border border-emerald-400/30 text-[11px] font-medium text-emerald-200">
              <Bot className="w-3 h-3" />
              <span>{NAV.badge.pipeline}</span>
            </span>
            <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full surface-glass border border-red-400/30 text-[11px] font-medium text-red-300">
              <Users className="w-3 h-3" />
              <span>{NAV.badge.limited}</span>
            </span>
          </div>

          <button
            type="button"
            onClick={() => openBooking("general")}
            className="hidden md:inline-flex items-center gap-2 px-5 py-2 rounded-lg bg-gradient-to-r from-pink-500 to-rose-500 text-white text-sm font-semibold shadow-[0_4px_20px_-6px_rgba(244,63,94,0.55)] hover:shadow-[0_0_40px_-6px_rgba(244,63,94,0.55)] hover:scale-[1.02] active:scale-100 transition-all"
          >
            <Calendar className="w-3.5 h-3.5" />
            {NAV.cta}
          </button>

          <button
            type="button"
            onClick={() => setOpen(!open)}
            className="md:hidden p-2 text-zinc-400 hover:text-white transition-colors"
            aria-label={open ? "Chiudi menu" : "Apri menu"}
            aria-expanded={open}
          >
            {open ? <X className="w-5 h-5" /> : <Menu className="w-5 h-5" />}
          </button>
        </div>

        {open && (
          <div
            className="md:hidden border-t border-white/10 bg-[#14141c]/98 backdrop-blur-xl"
            role="dialog"
            aria-modal="true"
            aria-label="Menu di navigazione"
          >
            <div className="px-6 py-4 space-y-1">
              {NAV.links.map((l) => (
                <a
                  key={l.label}
                  href={l.href}
                  onClick={close}
                  className="block py-3 text-sm font-medium text-zinc-300 hover:text-white hover:bg-white/[0.04] rounded-lg px-3 -mx-3 transition-colors"
                >
                  {l.label}
                </a>
              ))}
              <hr className="border-white/10 my-3" />
              <button
                type="button"
                onClick={() => {
                  close();
                  openBooking("general");
                }}
                className="w-full block py-3 text-sm font-semibold text-center text-white bg-gradient-to-r from-pink-500 to-rose-500 rounded-xl shadow-[0_4px_20px_-6px_rgba(244,63,94,0.55)] hover:scale-[1.02] active:scale-100 transition-all"
              >
                {NAV.cta}
              </button>
            </div>
          </div>
        )}
      </div>
    </nav>
  );
}
