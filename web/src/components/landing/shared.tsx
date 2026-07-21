import type { SVGProps } from "react";
import {
  Plus
} from "lucide-react";;

/* ----------------------------------------------------------------------------
 * Demo embeds
 * -------------------------------------------------------------------------- */

export const SHORT_DEMOS: ReadonlyArray<{ id: string; title: string }> = [
  { id: "MVwXsmRLnwM", title: "YouTube Shorts demo MVwXsmRLnwM" },
  { id: "XCIWzK2BuRo", title: "YouTube Shorts demo XCIWzK2BuRo" },
];

export const LONGFORM_DEMOS: ReadonlyArray<{ id: string; title: string }> = [
  { id: "fLhv7d6N_3c", title: "YouTube long-form demo fLhv7d6N_3c" },
  { id: "iA1WT69NFbw", title: "YouTube long-form demo iA1WT69NFbw" },
  { id: "R18AVWQ92fs", title: "YouTube long-form demo R18AVWQ92fs" },
  { id: "lpKX9SKqSMw", title: "YouTube long-form demo lpKX9SKqSMw" },
];

/* ----------------------------------------------------------------------------
 * Brand SVG marks
 * -------------------------------------------------------------------------- */

export type LogoProps = SVGProps<SVGSVGElement> & { className?: string };

export function InstagramLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect x="2" y="2" width="20" height="20" rx="5" fill="#E4405F" />
      <circle cx="12" cy="12" r="4.2" stroke="#fff" strokeWidth="1.6" />
      <circle cx="17.4" cy="6.6" r="0.95" fill="#fff" />
    </svg>
  );
}

export function FacebookLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect x="2" y="2" width="20" height="20" rx="4" fill="#1877F2" />
      <path d="M13.6 21v-7.2h2.4l.36-2.8H13.6V9.05c0-.81.23-1.35 1.4-1.35h1.5V5.15c-.26-.03-1.15-.11-2.18-.11-2.16 0-3.64 1.32-3.64 3.74v2.22H8.32v2.8h2.36V21h2.92z" fill="#fff" />
    </svg>
  );
}

export function YouTubeLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect x="2" y="5" width="20" height="14" rx="3.5" fill="#FF0000" />
      <path d="M10 9.2v5.6l4.4-2.8L10 9.2z" fill="#fff" />
    </svg>
  );
}

export function TikTokLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect x="2" y="2" width="20" height="20" rx="4.5" fill="#000" />
      <path d="M15.6 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45" stroke="#25F4EE" strokeWidth="1.7" strokeLinecap="round" />
      <path d="M15.85 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45" stroke="#FE2C55" strokeWidth="1.7" strokeLinecap="round" transform="translate(0.5 -0.4)" />
      <path d="M15.6 4.5a4.2 4.2 0 0 0 4.2 4.2" stroke="#25F4EE" strokeWidth="1.7" strokeLinecap="round" />
    </svg>
  );
}

export function XLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect width="24" height="24" rx="4" fill="#fff" />
      <path d="M14.65 11l4.05-5h-1.55l-3.45 4.34L10.85 6h-4.4l4.5 7.5L6 19h1.55l3.8-4.74L14.6 19h4l-4.65-8h.7zm-2 7l-.5-.7L7.85 7h1.4l4.4 6.3 1.95 2.7.5.7-3.45 0z" fill="#000" />
    </svg>
  );
}

export function LinkedInLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect x="2" y="2" width="20" height="20" rx="3" fill="#0A66C2" />
      <circle cx="7" cy="8" r="1.15" fill="#fff" />
      <rect x="6.05" y="10" width="2.1" height="6.5" rx="0.3" fill="#fff" />
      <path d="M10 16.5v-6.5h2v1.1c.45-.7 1.3-1.3 2.4-1.3 1.7 0 2.6 1.1 2.6 3V16.5h-2v-3.4c0-.9-.4-1.5-1.2-1.5s-1.2.5-1.2 1.5V16.5H10z" fill="#fff" />
    </svg>
  );
}

export function ThreadsLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect width="24" height="24" rx="6" fill="#000" />
      <path d="M12 6.5c2.7 0 4.7 1.6 4.7 4.7s-2 4.7-4.7 4.7-4.7-1.6-4.7-4.7" stroke="#fff" strokeWidth="1.4" strokeLinecap="round" />
      <path d="M12 6.5c-3 0-5 2-5 5s2 5 5 5" stroke="#fff" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

export const PLATFORM_REGISTRY = [
  { key: "instagram", name: "Instagram", Logo: InstagramLogo, color: "#E4405F" },
  { key: "tiktok", name: "TikTok", Logo: TikTokLogo, color: "#25F4EE" },
  { key: "youtube", name: "YouTube", Logo: YouTubeLogo, color: "#FF0000" },
  { key: "facebook", name: "Facebook", Logo: FacebookLogo, color: "#1877F2" },
  { key: "x", name: "X", Logo: XLogo, color: "#FFFFFF" },
  { key: "linkedin", name: "LinkedIn", Logo: LinkedInLogo, color: "#0A66C2" },
  { key: "threads", name: "Threads", Logo: ThreadsLogo, color: "#FFFFFF" },
] as const;

/* ----------------------------------------------------------------------------
 * Inline SVG icons
 * -------------------------------------------------------------------------- */

export function IconSchedule({ className = "w-5 h-5" }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className} aria-hidden="true">
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="1.7" />
      <path d="M12 7v5l3 2" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function IconAnalyze({ className = "w-5 h-5" }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className} aria-hidden="true">
      <path d="M3.5 20V4M3.5 20h17" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
      <rect x="7" y="13" width="3" height="6" rx="0.6" fill="currentColor" opacity="0.55" />
      <rect x="12" y="9" width="3" height="10" rx="0.6" fill="currentColor" opacity="0.75" />
      <rect x="17" y="6" width="3" height="13" rx="0.6" fill="currentColor" />
    </svg>
  );
}

/* ----------------------------------------------------------------------------
 * Dashboard Mockup
 * -------------------------------------------------------------------------- */

export type RowPlatform = "instagram" | "tiktok" | "youtube" | "facebook" | "x" | "linkedin" | "threads";
export type MockupRow = { thumb: string; title: string; meta: string; time: string; badges: ReadonlyArray<RowPlatform> };

const MOCKUP_ROWS: ReadonlyArray<MockupRow> = [
  { thumb: "from-fuchsia-500 to-violet-500", title: "Behind the scenes: shipping our AI pipeline", meta: "Vertical · auto-captioned", time: "Tomorrow · 09:00", badges: ["instagram", "linkedin", "youtube"] },
  { thumb: "from-sky-500 to-indigo-500", title: "Why async publishing beats 10-person teams", meta: "Horizontal · approved by Ana", time: "Wed · 14:00", badges: ["linkedin", "facebook"] },
  { thumb: "from-pink-500 to-orange-400", title: "Quarterly retrospective", meta: "Vertical · captions live", time: "Fri · 10:00", badges: ["instagram", "tiktok", "x"] },
  { thumb: "from-emerald-500 to-teal-400", title: "10,000 pieces of content: how we ship", meta: "Horizontal · thumbnail A/B", time: "Mon · 08:00", badges: ["youtube", "instagram"] },
];

export function BadgeLogo({ platform }: { platform: RowPlatform }) {
  const entry = PLATFORM_REGISTRY.find((p) => p.key === platform);
  if (!entry) return null;
  return <entry.Logo className="w-full h-full" />;
}

export function PlatformChip({ platform }: { platform: RowPlatform }) {
  const entry = PLATFORM_REGISTRY.find((p) => p.key === platform);
  if (!entry) return null;
  return (
    <span className="inline-flex w-5 h-5 rounded-md overflow-hidden ring-1 ring-white/15" title={entry.name} aria-label={entry.name}>
      <BadgeLogo platform={platform} />
    </span>
  );
}

export function DashboardMockup() {
  return (
    <div className="relative group">
      <div aria-hidden="true" className="absolute -inset-px rounded-2xl bg-gradient-to-br from-white/30 via-white/5 to-white/10 blur-[2px] pointer-events-none transition-all duration-500 group-hover:blur-[4px] group-hover:from-white/40" />
      <div aria-hidden="true" className="absolute -inset-8 hero-aurora opacity-60 blur-2xl rounded-[2rem] pointer-events-none -z-10 animate-pulse-glow" />
      <div className="relative surface-glass rounded-2xl overflow-hidden shadow-[0_30px_120px_-30px_rgba(124,58,237,0.55)] animate-fade-up animation-delay-200 transition-all duration-500 group-hover:shadow-[0_40px_160px_-30px_rgba(124,58,237,0.7)]">
        <div className="flex items-center justify-between px-4 py-3 border-b border-white/10">
          <div className="flex items-center gap-1.5">
            <span className="w-3 h-3 rounded-full bg-[#ff5f57]" />
            <span className="w-3 h-3 rounded-full bg-[#febc2e]" />
            <span className="w-3 h-3 rounded-full bg-[#28c840]" />
          </div>
          <div className="text-xs text-zinc-400 font-medium tracking-tight">instaedit.app · Calendar</div>
          <div className="w-12 h-6 rounded-md surface-card-soft flex items-center justify-center">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 mr-1.5 animate-pulse-glow" />
            <span className="text-[10px] text-zinc-300">Live</span>
          </div>
        </div>
        <div className="grid grid-cols-4 gap-px bg-white/5 border-b border-white/10">            {[{ v: "12", l: "Scheduled" }, { v: "4", l: "Platforms" }, { v: "7d", l: "Window" }, { v: "+", l: "New" }].map((s) => (
            <div key={s.l} className="bg-[#14141c]/70 px-3 py-2.5">
              <div className="text-base font-semibold text-white leading-tight">{s.v}</div>
              <div className="text-[10px] text-zinc-500 uppercase tracking-wider mt-0.5">{s.l}</div>
            </div>
          ))}
        </div>
        <div className="flex items-center gap-1 px-3 py-2 border-b border-white/10 text-xs overflow-x-auto">
          <span className="px-2.5 py-1 rounded-md bg-white/10 text-white font-medium whitespace-nowrap">Scheduled</span>
          <span className="px-2.5 py-1 text-zinc-500 hover:text-zinc-300 transition-colors cursor-default whitespace-nowrap">All</span>
          <span className="px-2.5 py-1 text-zinc-500 hover:text-zinc-300 transition-colors cursor-default whitespace-nowrap">Drafts</span>
          <span className="px-2.5 py-1 text-zinc-500 hover:text-zinc-300 transition-colors cursor-default whitespace-nowrap">Published</span>
          <span className="ml-auto inline-flex items-center gap-1 text-violet-300/90 text-[11px] font-medium whitespace-nowrap">
            <Plus className="w-3 h-3" /> New post
          </span>
        </div>
        <ul className="divide-y divide-white/5">
          {MOCKUP_ROWS.map((row) => (
            <li key={row.title} className="flex items-center gap-3 px-4 py-3.5 hover:bg-white/[0.03] transition-colors">
              <div className={`w-12 h-12 rounded-lg bg-gradient-to-br ${row.thumb} ring-1 ring-white/10 flex-shrink-0`} />
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium text-white truncate">{row.title}</div>
                <div className="flex items-center gap-2 mt-1">
                  <div className="flex -space-x-1.5">
                    {row.badges.map((b) => (<PlatformChip key={b} platform={b} />))}
                  </div>
                  <span className="text-[11px] text-zinc-500 truncate">· {row.meta}</span>
                </div>
              </div>
              <div className="text-[11px] text-zinc-400 flex-shrink-0 tabular-nums">{row.time}</div>
            </li>
          ))}
        </ul>
        <div className="flex items-center justify-between px-4 py-2.5 border-t border-white/10 bg-[#14141c]/60">
          <div className="text-[11px] text-zinc-500">12 of 28 posts scheduled this week</div>
          <div className="flex items-center gap-1.5 text-[11px] text-emerald-300/90 font-medium">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse-glow" /> Auto-publishing active
          </div>
        </div>
      </div>
      <div className="hidden lg:flex absolute -bottom-3 right-2 surface-card px-3 py-2 items-center gap-2 shadow-[0_15px_50px_-15px_rgba(0,0,0,0.7)] animate-fade-up animation-delay-400 hover:scale-105 transition-transform">
        <span className="w-7 h-7 rounded-lg bg-gradient-to-br from-violet-500 to-fuchsia-500 flex items-center justify-center">
          <Plus className="w-4 h-4 text-white" />
        </span>
        <div className="leading-tight">            <div className="text-xs font-semibold text-white">200 → 8 posts</div>
          <div className="text-[10px] text-zinc-500">in one click</div>
        </div>
      </div>
    </div>
  );
}

/* ----------------------------------------------------------------------------
 * YouTube embed
 * -------------------------------------------------------------------------- */

export function YouTubeEmbed({ id, title, aspect }: { id: string; title: string; aspect: "9/16" | "16/9" }) {
  const aspectClass = aspect === "9/16" ? "aspect-[9/16]" : "aspect-[16/9]";
  return (
    <div className="relative overflow-hidden rounded-2xl border border-white/15 bg-[#0a0a12] shadow-[0_25px_80px_-25px_rgba(0,0,0,0.85)] transition-all duration-500 hover:shadow-[0_30px_100px_-20px_rgba(139,92,246,0.3)] hover:border-violet-400/30">
      <div className={aspectClass}>
        {/* `web-share` Permissions-Policy token removed from the YouTube
            embed's `allow` attribute below — Chromium 120+ deprecated it
            (was emitting `[warn] Unrecognized feature: 'web-share'` in
            DevTools). YouTube's third-party embed never calls
            navigator.share() from inside the iframe, so the token was a
            no-op for our usage. See commit 2902c76. */}
        <iframe
          className="w-full h-full"
          src={`https://www.youtube.com/embed/${id}?playsinline=1`}
          title={title}
          loading="lazy"
          allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture"
          allowFullScreen
          referrerPolicy="strict-origin-when-cross-origin"
        />
      </div>
    </div>
  );
}

