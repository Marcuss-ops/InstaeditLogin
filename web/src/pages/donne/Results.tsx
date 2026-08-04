import { DollarSign, Clock, Users, BarChart3 } from "lucide-react";
import { ResultsGallery } from "../../components/landing/ResultsGallery";
import { RESULTS } from "./content";

/**
 * Sezione "I Risultati" — statistiche + gallery degli screenshot
 * YouTube Studio. La gallery (grid + lightbox) è condivisa con la
 * landing principale InstaEdit: riutilizza le stesse foto di risultati
 * (components/landing/ResultsGallery.tsx) con caption in italiano.
 */
export function Results() {
  const icons = [DollarSign, Clock, Users, BarChart3];

  return (
    <section id="risultati" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-15 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-pink-300/90 mb-3">{RESULTS.eyebrow}</div>
          <h2 className="text-display-2 text-white">
            {RESULTS.title}{" "}
            <span className="text-gradient-animated">Entrate reali.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[62ch] mx-auto">
            {RESULTS.subtitle}
          </p>
        </div>

        <div className="grid grid-cols-2 lg:grid-cols-4 gap-5 mb-16">
          {RESULTS.stats.map((s, i) => {
            const Icon = icons[i % icons.length];
            return (
              <div
                key={s.l}
                className={`surface-card p-6 text-center animate-fade-up hover:border-pink-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
              >
                <Icon className={`w-6 h-6 mx-auto mb-3 ${s.color}`} />
                <div className="text-3xl sm:text-4xl font-extrabold text-white tabular-nums tracking-tight">
                  {s.v}
                </div>
                <div className="text-sm font-medium text-zinc-300 mt-2">{s.l}</div>
                <div className="text-xs text-zinc-500 mt-1">{s.d}</div>
              </div>
            );
          })}
        </div>

        <ResultsGallery screenshots={RESULTS.screenshots} accent="pink" />
      </div>
    </section>
  );
}
