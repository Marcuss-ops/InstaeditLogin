import { useState } from "react";
import { ChevronDown } from "lucide-react";
import { FAQ } from "./content";

/**
 * Sezione FAQ con accordion accessibile (button + aria-expanded).
 */
export function FAQSection() {
  const [openIndex, setOpenIndex] = useState<number | null>(0);

  return (
    <section id="faq" className="relative py-24 sm:py-32 overflow-hidden bg-[#F6F3F7]">
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-[#A4708C] mb-3">{FAQ.eyebrow}</div>
          <h2 className="text-display-2 text-[#4A3E56]">{FAQ.title}</h2>
          <p className="text-body-lg text-[#6E6677] mt-5 max-w-[58ch]">{FAQ.subtitle}</p>
        </div>
        <div className="max-w-3xl mx-auto space-y-3">
          {FAQ.items.map((item, i) => {
            const isOpen = openIndex === i;
            return (
              <div
                key={item.q}
                className={`donne-card p-6 animate-fade-up transition-all duration-300 ${isOpen ? "border-[#E8C9CD] ring-1 ring-[#E8C9CD]/50" : ""}`}
              >
                <button
                  type="button"
                  onClick={() => setOpenIndex(isOpen ? null : i)}
                  aria-expanded={isOpen}
                  className="w-full flex items-start justify-between gap-4 text-left"
                >
                  <h3 className="text-base font-semibold text-[#4A3E56]">{item.q}</h3>
                  <ChevronDown
                    className={`w-5 h-5 text-[#E07A5F] shrink-0 mt-0.5 transition-transform duration-300 ${isOpen ? "rotate-180" : ""}`}
                  />
                </button>
                {isOpen && (
                  <p className="text-sm text-[#6E6677] leading-relaxed mt-4">{item.a}</p>
                )}
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
}
