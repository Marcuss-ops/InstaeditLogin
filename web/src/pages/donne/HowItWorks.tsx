import { Flower2 } from "lucide-react";
import { HOW_IT_WORKS } from "./content";

/**
 * Sezione "Come Funziona" — il meccanismo in 3 passi.
 */
export function HowItWorks() {
  return (
    <section id="come-funziona" className="relative py-24 sm:py-32 overflow-hidden bg-[#FFFFFF]">
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-[#A4708C] mb-3">{HOW_IT_WORKS.eyebrow}</div>
          <h2 className="text-display-2 text-[#4A3E56]">{HOW_IT_WORKS.title}</h2>
          <p className="text-body-lg text-[#6E6677] mt-5 max-w-[62ch]">
            {HOW_IT_WORKS.subtitle}
          </p>
        </div>
        <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-5">
          {HOW_IT_WORKS.steps.map((item, i) => (
            <div
              key={item.step}
              className={`donne-card p-6 relative overflow-hidden animate-fade-up hover:border-[#E8C9CD] transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              <div className="flex items-center justify-between mb-5">
                <span className="inline-flex w-12 h-12 items-center justify-center rounded-xl bg-gradient-to-br from-[#E07A5F] to-[#E28743] text-white text-lg font-bold shadow-[0_8px_20px_-8px_rgba(224,122,95,0.7)]">
                  {item.step}
                </span>
                <Flower2 className="w-5 h-5 text-[#D8A0A5]" />
              </div>
              <h3 className="text-display-3 text-[#4A3E56] mb-2">{item.title}</h3>
              <p className="text-sm text-[#6E6677] leading-relaxed">{item.description}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
