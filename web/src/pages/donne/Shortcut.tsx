import { CheckCircle2 } from "lucide-react";
import { SHORTCUT } from "./content";

/**
 * Sezione "La Scorciatoia" — la soluzione in 4 punti chiave.
 * Contro-altare del problema: stesso layout, segno opposto (verde salvia).
 */
export function Shortcut() {
  return (
    <section id="scorciatoia" className="relative py-24 sm:py-32 overflow-hidden bg-[#FFFFFF]">
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-[#A4708C] mb-3">{SHORTCUT.eyebrow}</div>
          <h2 className="text-display-2 text-[#4A3E56]">{SHORTCUT.title}</h2>
          <p className="text-body-lg text-[#6E6677] mt-5 max-w-[62ch]">
            {SHORTCUT.subtitle}
          </p>
        </div>
        <div className="grid sm:grid-cols-2 gap-5">
          {SHORTCUT.items.map((item, i) => (
            <div
              key={item.title}
              className={`donne-card p-6 animate-fade-up hover:border-[#CDDCC4] transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <div className="flex items-start gap-3">
                <span className="mt-0.5 inline-flex w-7 h-7 shrink-0 items-center justify-center rounded-lg bg-[#EEF2EA] ring-1 ring-[#DCE5D4] text-[#5F7A5A]">
                  <CheckCircle2 className="w-4 h-4" />
                </span>
                <div>
                  <h3 className="text-display-3 text-[#4A3E56] mb-1.5">{item.title}</h3>
                  <p className="text-sm text-[#6E6677] leading-relaxed">{item.description}</p>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
