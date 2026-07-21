import {
  PlayCircle
} from "lucide-react";
import { YouTubeEmbed, SHORT_DEMOS } from "./shared";
/* ----------------------------------------------------------------------------
 * Shorts section
 * -------------------------------------------------------------------------- */

export function Shorts() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 bg-gradient-to-br from-violet-500/15 via-transparent to-fuchsia-500/10 pointer-events-none" />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[380px] h-[380px] -top-20 -right-32 animate-drift-slow opacity-50" />
        <div className="glow-orb bg-fuchsia-500 w-[340px] h-[340px] -bottom-32 -left-24 animate-drift-rev opacity-40" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-12 items-center">
        <div className="lg:col-span-5 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-4 inline-flex items-center gap-2">
            <PlayCircle className="w-4 h-4" /> Short-form video
          </div>
          <h2 className="text-display-2 text-white mb-5">
            One video.{" "}
            <span className="text-gradient-animated">Every short-form platform.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 max-w-[52ch] mb-7">
            Record one vertical video and InstaEdit handles the rest — perfect sizing,
            captions, and formatting for YouTube Shorts, Instagram Reels, and TikTok.
            No re-editing required.
          </p>
          <ul className="space-y-3 text-sm">
            {[
              { c: "#FF0000", l: "YouTube Shorts" },
              { c: "#E4405F", l: "Instagram Reels" },
              { c: "#25F4EE", l: "TikTok" },
            ].map((p) => (
              <li key={p.l} className="flex items-center gap-3">
                <span className="w-2.5 h-2.5 rounded-full" style={{ background: p.c, boxShadow: `0 0 12px ${p.c}` }} />
                <span className="text-zinc-200 font-medium">{p.l}</span>
                <span className="text-zinc-600">·</span>
                <span className="text-zinc-500">Native format</span>
              </li>
            ))}
          </ul>
        </div>
        <div className="lg:col-span-7 grid grid-cols-1 sm:grid-cols-2 gap-5 animate-fade-up animation-delay-200">
          {SHORT_DEMOS.map((demo) => (
            <YouTubeEmbed key={demo.id} id={demo.id} title={demo.title} aspect="9/16" />
          ))}
        </div>
      </div>
    </section>
  );
}


