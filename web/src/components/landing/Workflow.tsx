import {
  Lightbulb,
  Film,
  Cpu,
  CalendarClock,
  Globe,
  BarChart3
} from "lucide-react";;
/* ----------------------------------------------------------------------------
 * Workflow — 6 steps expanded
 * -------------------------------------------------------------------------- */

export function Workflow() {
  const steps = [
    {
      n: "01", title: "Pick your niche",
      copy: "Choose a topic that makes money — finance, tech, history, true crime, whatever pays. We help you find what works.",
      Icon: Lightbulb,
      accent: "from-violet-500/30 to-violet-500/0", ring: "ring-violet-400/40", iconColor: "text-violet-300",
    },
    {
      n: "02", title: "Create or generate",
      copy: "Record yourself or let ChronoN create professional videos from a text brief. No camera? No problem.",
      Icon: Film,
      accent: "from-blue-500/30 to-blue-500/0", ring: "ring-blue-400/40", iconColor: "text-blue-300",
    },
    {
      n: "03", title: "AI optimizes for views",
      copy: "Thumbnails, subtitles, hooks, captions — all designed to maximize watch time and clicks. Each one tailored to its platform.",
      Icon: Cpu,
      accent: "from-emerald-500/30 to-emerald-500/0", ring: "ring-emerald-400/40", iconColor: "text-emerald-300",
    },
    {
      n: "04", title: "Post at peak times",
      copy: "We publish when your audience is most active. More views = more ad revenue = faster monetization.",
      Icon: CalendarClock,
      accent: "from-cyan-500/30 to-cyan-500/0", ring: "ring-cyan-400/40", iconColor: "text-cyan-300",
    },
    {
      n: "05", title: "Go live everywhere",
      copy: "One video → YouTube, TikTok, Instagram, Facebook, LinkedIn, X, and Threads. Maximum eyeballs, minimum effort.",
      Icon: Globe,
      accent: "from-pink-500/30 to-pink-500/0", ring: "ring-pink-400/40", iconColor: "text-pink-300",
    },
    {
      n: "06", title: "Track your earnings",
      copy: "See views, subscribers, and revenue in one dashboard. Double down on what's working and cut what's not.",
      Icon: BarChart3,
      accent: "from-amber-500/30 to-amber-500/0", ring: "ring-amber-400/40", iconColor: "text-amber-300",
    },
  ];

  return (
    <section id="workflow" className="relative py-24 sm:py-32">
      <div aria-hidden="true" className="hidden lg:block absolute top-[58%] left-0 right-0 h-px bg-gradient-to-r from-transparent via-violet-400/40 to-transparent pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">The complete workflow</div>
          <h2 className="text-display-2 text-white">
            Your path to{" "}
            <span className="text-gradient-animated">online income.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            From your first video to your first paycheck — we handle the
            technical work so you can focus on growing your audience and earning.
          </p>
        </div>
        <ol className="grid sm:grid-cols-2 lg:grid-cols-3 gap-5 relative">
          {steps.map((s, i) => (
            <li key={s.n} className={`surface-card p-6 relative overflow-hidden animate-fade-up hover:border-violet-400/30 hover:shadow-[0_8px_32px_rgba(139,92,246,0.12)] transition-all duration-300 group ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400", "animation-delay-500"][i]}`}>
              <div aria-hidden="true" className={`absolute -top-20 -right-20 w-56 h-56 rounded-full bg-radial ${s.accent} opacity-70 blur-2xl pointer-events-none transition-all duration-500 group-hover:opacity-100 group-hover:scale-110`} />
              <div className="relative">
                <div className="flex items-center justify-between mb-5">
                  <span className={`inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ${s.ring} surface-glass ${s.iconColor} group-hover:scale-110 transition-transform duration-300`}>
                    <s.Icon className="w-5 h-5" />
                  </span>
                  <span className="text-eyebrow text-zinc-500 tabular-nums">Step {s.n}</span>
                </div>
                <h3 className="text-display-3 text-white mb-2">{s.title}</h3>
                <p className="text-sm text-zinc-400 leading-relaxed">{s.copy}</p>
              </div>
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}


