import {
  CheckCircle2, Scissors
} from "lucide-react";

export function EditorShortsCut() {
  const cuts = [
    { id: "01", label: "Hook", start: 14, end: 38, color: "from-violet-500 to-fuchsia-500" },
    { id: "02", label: "Stat", start: 92, end: 124, color: "from-cyan-500 to-sky-500" },
    { id: "03", label: "Tip", start: 188, end: 222, color: "from-pink-500 to-rose-500" },
    { id: "04", label: "Demo", start: 296, end: 332, color: "from-amber-500 to-orange-500" },
    { id: "05", label: "Reveal", start: 410, end: 442, color: "from-emerald-500 to-teal-500" },
    { id: "06", label: "CTA", start: 522, end: 552, color: "from-indigo-500 to-violet-500" },
  ];
  const TOTAL_SECONDS = 24 * 60 + 13; // 24:13
  // Longest cut boundary so the inner progress fill is non-trivial
  // visually (~30-40% on the widest card, ~20% on the narrowest).
  const MAX_CUT_SECONDS = 36;
  const formatTime = (sec: number) =>
    `${Math.floor(sec / 60)}:${String(sec % 60).padStart(2, "0")}`;

  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-gradient-to-tr from-cyan-500/[0.10] via-transparent to-pink-500/[0.10] pointer-events-none"
      />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-cyan-400 w-[400px] h-[400px] -top-32 -right-20 animate-drift-rev opacity-45" />
        <div className="glow-orb bg-pink-500 w-[360px] h-[360px] -bottom-32 -left-24 animate-drift-slow opacity-40" />
      </div>

      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-12 items-center">
        <div className="lg:col-span-5 lg:order-2 animate-fade-up">
          <div className="text-eyebrow text-cyan-300/90 mb-3 inline-flex items-center gap-2">
            <Scissors className="w-4 h-4" />
            Cut for shorts
          </div>
          <h2 className="text-display-2 text-white">
            One long-form.{" "}
            <span className="text-gradient">Six shorts.</span> Automatic cutting.
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            The cutting engine finds the most tense moments — hooks, stats,
            reveals, CTAs — and extracts each as a 9:16 vertical clip, ready for
            Shorts, Reels and TikTok. Frame, caption, publish.
          </p>

          <ul className="mt-7 space-y-3">
            {[
              {
                t: "Tension-aware extraction",
                d: "Evaluates every segment for hook and payoff — keep only the best.",
              },
              {
                t: "AI reframing 16:9 → 9:16",
                d: "Reframes with face-tracking and rule of thirds that look designed.",
              },
              {
                t: "Integrated subtitles",
                d: "Localized subtitles integrated into every cut, optional b-roll.",
              },
            ].map((it) => (
              <li key={it.t} className="flex items-start gap-3">
                <span className="mt-0.5 inline-flex w-5 h-5 items-center justify-center rounded-md bg-emerald-500/15 ring-1 ring-emerald-400/25 flex-shrink-0">
                  <CheckCircle2
                    className="w-3.5 h-3.5 text-emerald-300"
                    aria-hidden="true"
                  />
                </span>
                <div>
                  <div className="text-sm font-medium text-white leading-snug">
                    {it.t}
                  </div>
                  <div className="text-[13px] text-zinc-400 mt-0.5 leading-relaxed">
                    {it.d}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        </div>

        <div className="lg:col-span-7 lg:order-1 animate-fade-up animation-delay-200">
          <div className="surface-glass border border-white/15 rounded-2xl overflow-hidden shadow-[0_30px_100px_-40px_rgba(34,211,238,0.45)]">
            <div className="flex items-center justify-between px-4 py-3 border-b border-white/10">
              <div className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-full bg-[#ff5f57]" />
                <span className="w-2.5 h-2.5 rounded-full bg-[#febc2e]" />
                <span className="w-2.5 h-2.5 rounded-full bg-[#28c840]" />
              </div>
              <div className="text-xs text-zinc-400 font-medium tracking-tight">
                how-we-ship.mov · 24:13 · 6 cuts detected
              </div>
              <div className="w-14 h-6 rounded-md surface-card-soft flex items-center justify-center">
                <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 mr-1.5 animate-pulse-glow" />
                <span className="text-[10px] text-zinc-300 tracking-wide">
                  Auto
                </span>
              </div>
            </div>

            <div className="p-5 sm:p-6 space-y-5">
              {/* Timeline */}
              <div>
                <div className="flex items-center justify-between mb-2">
                  <div className="text-eyebrow text-zinc-500">Timeline</div>
                  <div className="text-[10px] text-zinc-500 tabular-nums">
                    Source · 24:13
                  </div>
                </div>
                <div className="relative h-14 rounded-lg bg-gradient-to-r from-zinc-800/80 via-zinc-700/60 to-zinc-800/80 ring-1 ring-white/10 overflow-hidden">
                  {/* Detected cuts as gradient spans */}
                  {cuts.map((c) => {
                    const left = (c.start / TOTAL_SECONDS) * 100;
                    const width = ((c.end - c.start) / TOTAL_SECONDS) * 100;
                    return (
                      <div
                        key={c.id}
                        className={`absolute top-1.5 bottom-1.5 rounded-md bg-gradient-to-r ${c.color} opacity-85 ring-1 ring-white/20 shadow-[0_0_10px_rgba(255,255,255,0.06)]`}
                        style={{ left: `${left}%`, width: `${width}%` }}
                        aria-label={`Cut ${c.id}: ${c.label}`}
                      />
                    );
                  })}
                  {/* Playhead */}
                  <div
                    className="absolute top-0 bottom-0 w-px bg-white/90 shadow-[0_0_8px_rgba(255,255,255,0.7)]"
                    style={{ left: "32%" }}
                    aria-hidden="true"
                  />
                  {/* Playhead topper */}
                  <div
                    className="absolute -top-0.5 w-2 h-2 -translate-x-1/2 rotate-45 bg-white shadow-[0_0_8px_rgba(255,255,255,0.7)]"
                    style={{ left: "32%" }}
                    aria-hidden="true"
                  />
                </div>
                <div className="mt-2 flex justify-between text-[10px] text-zinc-500 tabular-nums">
                  <span>0:00</span>
                  <span>6:00</span>
                  <span>12:00</span>
                  <span>18:00</span>
                  <span>24:13</span>
                </div>
              </div>

              {/* Cuts queue */}
              <div>
                <div className="flex items-center justify-between mb-2.5">
                  <div className="text-eyebrow text-zinc-500">
                    Tagli queued
                    <span className="ml-2 inline-flex items-center px-1.5 py-0.5 rounded bg-white/[0.06] text-[10px] text-zinc-300 tabular-nums">
                      6
                    </span>
                  </div>
                  <div className="text-[10px] text-emerald-300/90 font-medium inline-flex items-center gap-1.5">
                    <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse-glow" />
                    Pronti · 9:16 ciascuno
                  </div>
                </div>
                <div className="grid grid-cols-2 sm:grid-cols-3 gap-2.5">
                  {cuts.map((c) => (
                    <div
                      key={c.id}
                      className="surface-card-soft rounded-lg p-3 relative overflow-hidden"
                    >
                      <div
                        aria-hidden="true"
                        className={`absolute -top-12 -right-12 w-24 h-24 rounded-full blur-2xl bg-gradient-to-br ${c.color} opacity-50`}
                      />
                      <div className="relative">
                        <div className="flex items-center justify-between mb-1.5">
                          <span className="text-eyebrow text-zinc-500 tabular-nums">
                            {c.id}
                          </span>
                          <span className="text-[10px] text-zinc-400 tabular-nums">
                            {formatTime(c.start)}–{formatTime(c.end)}
                          </span>
                        </div>
                        <div className="text-sm font-semibold text-white leading-tight">
                          {c.label}
                        </div>
                        <div className="mt-2 h-1 rounded-full bg-white/[0.06] overflow-hidden">
                          <div
                            className={`h-full bg-gradient-to-r ${c.color}`}
                            style={{
                              width: `${Math.min(
                                100,
                                ((c.end - c.start) / MAX_CUT_SECONDS) * 100,
                              )}%`,
                            }}
                          />
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
