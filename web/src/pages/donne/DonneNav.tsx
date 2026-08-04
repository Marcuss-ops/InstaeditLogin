import { useState, useCallback, useEffect } from "react";
import { Link } from "react-router-dom";
import { Menu, X, Calendar, Bot, Sparkles } from "lucide-react";
import { useBooking } from "../../components/booking/BookingProvider";
import { NAV } from "./content";

/**
 * Navigazione autonoma della landing "DonneTube" — tema chiaro.
 * Volutamente separata da `MarketingNav` e `Nav`: la landing è un
 * progetto indipendente con la propria identità e la propria lingua.
 *
 * Palette calda e femminile (crema / prugna / corallo) e layout pulito:
 * barra principale (logo + menu + CTA) con una sottile "trust bar"
 * sotto, dove vivono i due badge di fiducia.
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
      <div className="bg-white/85 backdrop-blur-xl border-b border-[#ECE6EE]">
        {/* Main bar: logo + links + CTA */}
        <div className="mx-auto max-w-7xl h-16 px-6 flex items-center justify-between">
          <Link to="/donnetube" className="flex items-center gap-2 group" onClick={close}>
            <span className="inline-flex w-7 h-7 items-center justify-center rounded-md bg-gradient-to-br from-[#E07A5F] to-[#E28743] text-white shadow-[0_0_18px_-4px_rgba(224,122,95,0.7)] group-hover:shadow-[0_0_26px_-4px_rgba(226,135,67,0.8)] transition-shadow">
              <Bot className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-[#4A3E56] text-sm">{NAV.brand}</span>
          </Link>

          <div className="hidden md:flex items-center gap-7 text-sm font-medium text-[#7A7280]">
            {NAV.links.map((l) => (
              <a
                key={l.label}
                href={l.href}
                onClick={close}
                className="hover:text-[#4A3E56] transition-colors relative after:absolute after:bottom-[-2px] after:left-0 after:h-[2px] after:w-0 after:bg-gradient-to-r after:from-[#E07A5F] after:to-[#E28743] after:transition-all after:duration-300 hover:after:w-full"
              >
                {l.label}
              </a>
            ))}
          </div>

          <button
            type="button"
            onClick={() => openBooking("general")}
            className="hidden md:inline-flex items-center gap-2 px-5 py-2 rounded-lg bg-gradient-to-r from-[#E07A5F] to-[#E28743] text-white text-sm font-semibold shadow-[0_6px_20px_-8px_rgba(224,122,95,0.7)] hover:shadow-[0_10px_28px_-8px_rgba(226,135,67,0.75)] hover:scale-[1.02] active:scale-100 transition-all"
          >
            <Calendar className="w-3.5 h-3.5" />
            {NAV.cta}
          </button>

          <button
            type="button"
            onClick={() => setOpen(!open)}
            className="md:hidden p-2 text-[#6B4E71] hover:text-[#4A3E56] transition-colors"
            aria-label={open ? "Chiudi menu" : "Apri menu"}
            aria-expanded={open}
          >
            {open ? <X className="w-5 h-5" /> : <Menu className="w-5 h-5" />}
          </button>
        </div>

        {/* Trust bar: badges di fiducia, centrati, toni caldi */}
        <div className="hidden md:flex items-center justify-center gap-2 border-t border-[#F1EBF2] py-2.5 px-6 bg-[#FCFAF8]">
          <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full bg-[#F6EDEE] border border-[#E8D8DB] text-[11px] font-medium text-[#9C5B63]">
            <Bot className="w-3 h-3 text-[#C4696F]" />
            <span>{NAV.badge.pipeline}</span>
          </span>
          <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full bg-[#FBF1E7] border border-[#EDDFCE] text-[11px] font-medium text-[#A06A38]">
            <Sparkles className="w-3 h-3 text-[#C78A4B]" />
            <span>{NAV.badge.limited}</span>
          </span>
        </div>

        {open && (
          <div
            className="md:hidden border-t border-[#ECE6EE] bg-white/98 backdrop-blur-xl"
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
                  className="block py-3 text-sm font-medium text-[#4A3E56] hover:text-[#6B4E71] hover:bg-[#F6F3F7] rounded-lg px-3 -mx-3 transition-colors"
                >
                  {l.label}
                </a>
              ))}
              <hr className="border-[#ECE6EE] my-3" />
              <button
                type="button"
                onClick={() => {
                  close();
                  openBooking("general");
                }}
                className="w-full block py-3 text-sm font-semibold text-center text-white bg-gradient-to-r from-[#E07A5F] to-[#E28743] rounded-xl shadow-[0_6px_20px_-8px_rgba(224,122,95,0.7)] hover:scale-[1.02] active:scale-100 transition-all"
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
