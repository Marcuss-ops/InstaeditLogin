import { DollarSign, Clock, Users, BarChart3 } from "lucide-react";
import { ResultsGallery } from "../../components/landing/ResultsGallery";
import { RESULTS } from "./content";

/**
 * Sezione "I Risultati" — statistiche + gallery degli screenshot
 * YouTube Studio (tema chiaro). La gallery condivide le stesse foto
 * della landing principale InstaEdit con caption in italiano.
 */
export function Results() {
  const icons = [DollarSign, Clock, Users, BarChart3];
  const iconColors = ["text-[#6E9E68]", "text-[#7A8FB2]", "text-[#9B6EA8]", "text-[#C78A4B]"];

  return (
    <section id="risultati" className="relative py-24 sm:py-32 overflow-hidden bg-[#F6F3F7]">
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-[#A4708C] mb-3">{RESULTS.eyebrow}</div>
          <h2 className="text-display-2 text-[#4A3E56]">
            {RESULTS.title}{" "}
            <span className="text-gradient-warm">Entrate reali.</span>
          </h2>
          <p className="text-body-lg text-[#6E6677] mt-5 max-w-[62ch] mx-auto">
            {RESULTS.subtitle}
          </p>
        </div>

        <div className="grid grid-cols-2 lg:grid-cols-4 gap-5 mb-16">
          {RESULTS.stats.map((s, i) => {
            const Icon = icons[i % icons.length];
            return (
              <div
                key={s.l}
                className={`donne-card p-6 text-center animate-fade-up hover:border-[#E8C9CD] transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
              >
                <Icon className={`w-6 h-6 mx-auto mb-3 ${iconColors[i % iconColors.length]}`} />
                <div className="text-3xl sm:text-4xl font-extrabold text-[#4A3E56] tabular-nums tracking-tight">
                  {s.v}
                </div>
                <div className="text-sm font-medium text-[#5B5566] mt-2">{s.l}</div>
                <div className="text-xs text-[#7A7280] mt-1">{s.d}</div>
              </div>
            );
          })}
        </div>

        <ResultsGallery screenshots={RESULTS.screenshots} accent="pink" tone="light" />
      </div>
    </section>
  );
}
