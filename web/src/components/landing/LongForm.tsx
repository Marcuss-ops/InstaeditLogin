import {
  MonitorPlay
} from "lucide-react";
import { YouTubeEmbed, LONGFORM_DEMOS } from "./shared";
/* ----------------------------------------------------------------------------
 * Long-form section
 * -------------------------------------------------------------------------- */

export function LongForm() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 bg-gradient-to-tr from-cyan-500/15 via-transparent to-pink-500/15 pointer-events-none" />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-cyan-400 w-[400px] h-[400px] -top-32 -left-32 animate-drift-rev opacity-50" />
        <div className="glow-orb bg-pink-500 w-[360px] h-[360px] -bottom-32 -right-32 animate-drift-slow opacity-40" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-12 items-center">
        <div className="lg:col-span-7 lg:order-2 animate-fade-up">
          <div className="text-eyebrow text-cyan-300/90 mb-4 inline-flex items-center gap-2">
            <MonitorPlay className="w-4 h-4" /> Long-form video
          </div>
          <h2 className="text-display-2 text-white mb-5">
            One long video.{" "}
            <span className="text-gradient-animated">Published everywhere.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 max-w-[52ch] mb-7 lg:ml-auto">
            Upload your horizontal video once and InstaEdit publishes it to YouTube,
            Instagram, Facebook, and LinkedIn — with the right title, description,
            thumbnail, and chapters for each platform.
          </p>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 lg:justify-end">
            {[
              { c: "#FF0000", l: "YouTube" },
              { c: "#E4405F", l: "Instagram" },
              { c: "#1877F2", l: "Facebook" },
              { c: "#0A66C2", l: "LinkedIn" },
            ].map((p) => (
              <div key={p.l} className="flex items-center gap-2 px-3 py-2 rounded-lg surface-glass border border-white/10">
                <span className="w-2 h-2 rounded-full" style={{ background: p.c, boxShadow: `0 0 10px ${p.c}` }} />
                <span className="text-sm text-zinc-200 font-medium">{p.l}</span>
              </div>
            ))}
          </div>
        </div>
        <div className="lg:col-span-5 lg:order-1 grid grid-cols-1 sm:grid-cols-2 gap-5 animate-fade-up animation-delay-200">
          {LONGFORM_DEMOS.slice(0, 2).map((demo) => (
            <YouTubeEmbed key={demo.id} id={demo.id} title={demo.title} aspect="16/9" />
          ))}
        </div>
      </div>
      <div className="relative mx-auto max-w-7xl px-6 mt-10 grid grid-cols-1 sm:grid-cols-2 gap-5">
        {LONGFORM_DEMOS.slice(2).map((demo) => (
          <YouTubeEmbed key={demo.id} id={demo.id} title={demo.title} aspect="16/9" />
        ))}
      </div>
    </section>
  );
}


