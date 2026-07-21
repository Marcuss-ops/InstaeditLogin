import {
  CheckCircle2, Globe
} from "lucide-react";
import {
  LANGUAGES
} from "./shared";

export function EditorTranslate() {
  const previewLines = [
    {
      flag: "🇮🇹",
      lang: "Italiano",
      text: "It used to take a team of five. Now an afternoon is enough.",
    },
    {
      flag: "🇯🇵",
      lang: "日本語",
      text: "It used to take a team of five. Now one person finishes it in an afternoon.",
    },
    {
      flag: "🇧🇷",
      lang: "Português",
      text: "It used to take five. Now I do it in an afternoon.",
    },
    {
      flag: "🇩🇪",
      lang: "Deutsch",
      text: "It used to take five people. Today an afternoon is enough.",
    },
  ] as const;
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-gradient-to-br from-violet-500/[0.10] via-transparent to-cyan-500/[0.10] pointer-events-none"
      />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[400px] h-[400px] -top-20 -right-32 animate-drift-slow opacity-50" />
        <div className="glow-orb bg-cyan-400 w-[360px] h-[360px] -bottom-32 -left-24 animate-drift-rev opacity-40" />
      </div>

      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-12 items-center">
        <div className="lg:col-span-5 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3 inline-flex items-center gap-2">
            <Globe className="w-4 h-4" />
            Translate
          </div>
          <h2 className="text-display-2 text-white">
            Reach 50+ markets.{" "}
            <span className="text-gradient">In their language.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Subtitles, titles and chapters auto-localized in a single pass —
            with culturally adapted phrasing, not a simple literal translation.
            Optional AI dubbing maintains the original cadence.
          </p>

          <ul className="mt-7 space-y-3">
            {[
              {
                t: "Cultural tone, not just translation",
                d: "Market models — idioms, formality and brand voice preserved.",
              },
              {
                t: "Subtitles and chapters precisely synced",
                d: "Subtitle tracks baked into every native per-platform render.",
              },
              {
                t: "Optional AI dubbing",
                d: "Cloned or library-selected voice — synced with the edit.",
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

        <div className="lg:col-span-7 grid gap-5 animate-fade-up animation-delay-200">
          {/* Translation preview panel — window-chromed mockup */}
          <div className="surface-glass border border-white/15 rounded-2xl overflow-hidden shadow-[0_30px_100px_-40px_rgba(124,58,237,0.45)]">
            <div className="flex items-center justify-between px-4 py-3 border-b border-white/10">
              <div className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-full bg-[#ff5f57]" />
                <span className="w-2.5 h-2.5 rounded-full bg-[#febc2e]" />
                <span className="w-2.5 h-2.5 rounded-full bg-[#28c840]" />
              </div>
              <div className="text-xs text-zinc-400 font-medium tracking-tight">
                Translate · preview
              </div>
              <div className="text-[10px] inline-flex items-center gap-1.5 text-zinc-400">
                <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse-glow" />
                <span>
                  EN → <span className="tabular-nums font-semibold text-white">12</span>
                </span>
              </div>
            </div>

            <div className="p-5 sm:p-6 grid sm:grid-cols-2 gap-4">
              {/* Original */}
              <div className="surface-card-soft rounded-xl p-4">
                <div className="flex items-center justify-between mb-2">
                  <div className="text-eyebrow text-zinc-500">Original</div>
                  <span className="inline-flex items-center gap-1 text-[10px] text-zinc-400">
                    <span className="text-base leading-none">🇬🇧</span>
                    EN
                  </span>
                </div>
                <div className="text-sm text-zinc-200 leading-relaxed">
                  “Publishing content across seven platforms used to take a team of
                  five people. Now an afternoon is enough.”
                </div>
              </div>
              {/* Translated stack */}
              <div className="space-y-2.5">
                {previewLines.map((p) => (
                  <div
                    key={p.lang}
                    className="surface-card-soft rounded-lg p-3 flex gap-3"
                  >
                    <span
                      className="text-2xl leading-none flex-shrink-0 mt-0.5"
                      aria-hidden="true"
                    >
                      {p.flag}
                    </span>
                    <div className="min-w-0">
                      <div className="text-[10px] uppercase tracking-wider text-zinc-500 mb-0.5">
                        {p.lang}
                      </div>
                      <div className="text-[13px] text-zinc-200 leading-snug">
                        {p.text}
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>

          {/* Supported languages grid */}
          <div className="surface-card p-4 sm:p-5">
            <div className="flex items-center justify-between mb-3.5">
              <div className="text-eyebrow text-violet-300/90">
                Supported languages
              </div>
              <div className="text-xs text-zinc-400">
                <span className="text-white font-bold tabular-nums">50+</span>
                <span className="ml-1.5">markets covered</span>
              </div>
            </div>
            <div className="flex flex-wrap gap-1.5">
              {LANGUAGES.map((lang) => (
                <span
                  key={lang.code}
                  className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-full surface-glass border border-white/10 hover:border-white/25 transition-colors"
                  title={`${lang.name} · ${lang.code}`}
                >
                  <span className="text-base leading-none" aria-hidden="true">
                    {lang.flag}
                  </span>
                  <span className="text-[12px] text-zinc-200">{lang.name}</span>
                  <span className="text-[10px] text-zinc-500 tabular-nums">
                    {lang.code}
                  </span>
                </span>
              ))}
              <span className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-full bg-violet-500/10 border border-violet-400/20 text-[11px] text-violet-200/90 font-medium">
                + 20 more
              </span>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
