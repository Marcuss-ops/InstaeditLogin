import { PlayCircle } from "lucide-react";

/* ----------------------------------------------------------------------------
 * FounderStory — the founder's video, placed right before the FAQ.
 *
 * Embeds the founder's YouTube Short (https://youtube.com/shorts/mB0NHhVMrKQ)
 * in a 9:16 phone-style frame so the vertical format reads naturally on the
 * dark landing. Copy tells the "how it started" story that the video shows.
 * -------------------------------------------------------------------------- */

const FOUNDER_VIDEO_ID = "mB0NHhVMrKQ";

export function FounderStory() {
  return (
    <section id="founder" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-25 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="grid lg:grid-cols-2 gap-16 lg:gap-12 items-center">
          {/* Copy */}
          <div className="animate-fade-up max-w-[55ch]">
            <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-6">
              <PlayCircle className="w-3.5 h-3.5 text-red-400" />
              <span>From the founder</span>
            </div>
            <h2 className="text-display-2 text-white mb-6">
              How InstaEdit{" "}
              <span className="text-gradient">got started.</span>
            </h2>
            <p className="text-body-lg text-zinc-400 mb-6">
              I started exactly where you are: no studio, no team, no budget —
              just an idea and a lot of hours in front of a screen. Publishing
              everywhere meant a 14-tab workflow, re-encoding, manual subtitles…
              it felt like a full-time job just to hit &ldquo;publish&rdquo;.
            </p>
            <p className="text-body-lg text-zinc-400 mb-8">
              So I built the tool I wish I'd had from day one. InstaEdit automates
              the entire pipeline — script to render to publishing on 7 platforms —
              so anyone can earn a living creating content, without the busywork.
            </p>
            <div className="grid grid-cols-3 gap-4">
              {[{ v: "1", l: "Founder-built" }, { v: "7", l: "Platforms" }, { v: "90%", l: "AI-executed" }].map((s) => (
                <div key={s.l} className="surface-card p-4 text-center">
                  <div className="text-xl font-bold text-white tabular-nums">{s.v}</div>
                  <div className="text-eyebrow text-zinc-500 mt-1">{s.l}</div>
                </div>
              ))}
            </div>
          </div>

          {/* Video — 9:16 Shorts frame */}
          <div className="relative animate-fade-up animation-delay-200">
            <div
              aria-hidden="true"
              className="absolute -top-16 -right-10 w-64 h-64 rounded-full bg-red-500/20 blur-3xl pointer-events-none"
            />
            <div className="relative mx-auto w-full max-w-[320px] surface-glass rounded-[2rem] border border-white/15 p-2 shadow-[0_30px_100px_-40px_rgba(124,58,237,0.45)]">
              <div className="relative aspect-[9/16] rounded-[1.6rem] overflow-hidden bg-black">
                <iframe
                  src={`https://www.youtube.com/embed/${FOUNDER_VIDEO_ID}?playsinline=1`}
                  title="The founder explains how InstaEdit started — from zero to a 7-platform publishing tool"
                  className="absolute inset-0 w-full h-full"
                  loading="lazy"
                  allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
                  allowFullScreen
                />
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
