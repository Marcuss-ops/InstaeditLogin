import {
  DollarSign,
  Clock,
  Users,
  BarChart3,
} from "lucide-react";
import { ResultsGallery } from "../../components/landing/ResultsGallery";

/* ----------------------------------------------------------------------------
 * Results — income-focused stats + YouTube screenshot gallery.
 *
 * Social proof lives in two verified forms only: the student video
 * testimonials (Testimonials.tsx) and the YouTube Studio screenshot
 * gallery below. The gallery itself (grid + accessible lightbox) is
 * shared with the independent HerChannel AI landing via
 * components/landing/ResultsGallery.tsx.
 * -------------------------------------------------------------------------- */

export function ResultsSection() {
  const stats = [
    { v: "$1,940", l: "Median student income", desc: "across all active channels", icon: DollarSign, color: "text-emerald-400" },
    { v: "14 days", l: "Avg. first payout", desc: "from channel start", icon: Clock, color: "text-blue-400" },
    { v: "50+", l: "Channels monetized", desc: "and generating revenue", icon: Users, color: "text-violet-400" },
    { v: "90%", l: "AI-executed pipeline", desc: "you approve, we run", icon: BarChart3, color: "text-amber-400" },
  ];

  const screenshots = [
    { img: "/results/result-1.jpg", alt: "YouTube channel growth result — first 90 days", caption: "Day-by-day growth on a finance channel" },
    { img: "/results/result-2.jpg", alt: "Content strategy result — RPM uplift", caption: "RPM uplift after PM setup" },
    { img: "/results/result-3.jpg", alt: "Channel monetization result — Partner Program", caption: "Partner Program activation window" },
    { img: "/results/result-4.jpg", alt: "Video performance result — 28-day revenue", caption: "28-day revenue per video" },
    { img: "/results/result-5.jpg", alt: "Creator growth result — subscribers", caption: "Subscriber curve crossing 1k" },
    { img: "/results/result-6.jpg", alt: "Multi-platform result — cross-post", caption: "Cross-post earnings across 7 platforms" },
  ];

  return (
    <section id="results" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-15 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Results</div>
          <h2 className="text-display-2 text-white">
            Real people.{" "}
            <span className="text-gradient">Real income.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            Most creators spend months earning nothing. Our students hit their first
            payout in under two weeks and build a recurring monthly income on autopilot.
            Hover or tap any screenshot to verify the numbers.
          </p>
        </div>

        {/* Stats */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-5 mb-16">
          {stats.map((s, i) => (
            <div
              key={s.l}
              className={`surface-card p-6 text-center animate-fade-up hover:border-violet-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <s.icon className={`w-6 h-6 mx-auto mb-3 ${s.color}`} />
              <div className="text-3xl sm:text-4xl font-extrabold text-white tabular-nums tracking-tight">{s.v}</div>
              <div className="text-sm font-medium text-zinc-300 mt-2">{s.l}</div>
              <div className="text-xs text-zinc-500 mt-1">{s.desc}</div>
            </div>
          ))}
        </div>

        {/* YouTube Studio screenshot gallery — clickable into the lightbox. */}
        <ResultsGallery screenshots={screenshots} accent="violet" />
      </div>
    </section>
  );
}
