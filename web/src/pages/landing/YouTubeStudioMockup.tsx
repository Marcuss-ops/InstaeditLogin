/* ----------------------------------------------------------------------------
 * YouTubeStudioMockup — True YT Studio Monetization panel for the hero.
 *
 * Replaces the previous "instaedit.app · Calendar" preview. The intent
 * is the instant "Revenue ↑" read in the first 3 seconds of scroll:
 *   1. "Partner Program Active" badge so visitors immediately know it's
 *      a monetized channel.
 *   2. Big monthly earnings number with an YoY growth chip.
 *   3. Ascending revenue chart for the last 12 months — proof of growth.
 *   4. Top videos table with watch-time bars so the dashboard reads
 *      like a real YT Studio surface, not an InstaEdit product UI.
 *
 * The frame is decorative only — there is no /youtube/studio feed in
 * the hero, the data is hardcoded so the mockup renders consistently
 * across viewports.
 * -------------------------------------------------------------------------- */

const REVENUE_BARS: ReadonlyArray<number> = [
  18, 24, 22, 30, 38, 44, 52, 58, 64, 72, 84, 96,
];

const TOP_VIDEOS: ReadonlyArray<{
  title: string;
  views: string;
  watchTime: string;
  ctr: string;
  width: number;
}> = [
  {
    title: "How I make $5,000/week with one channel",
    views: "284k views",
    watchTime: "62% avg view duration",
    ctr: "9.4% CTR",
    width: 96,
  },
  {
    title: "The exact YouTube tag formula nobody shares",
    views: "172k views",
    watchTime: "57% avg view duration",
    ctr: "7.8% CTR",
    width: 78,
  },
  {
    title: "I automated my entire content pipeline",
    views: "98k views",
    watchTime: "51% avg view duration",
    ctr: "6.2% CTR",
    width: 58,
  },
];

export function YouTubeStudioMockup() {
  return (
    <div className="relative group">
      {/* Gradient outer glow ring */}
      <div
        aria-hidden="true"
        className="absolute -inset-px rounded-2xl bg-gradient-to-br from-white/30 via-white/5 to-white/10 blur-[2px] pointer-events-none transition-all duration-500 group-hover:blur-[4px] group-hover:from-white/40"
      />
      <div
        aria-hidden="true"
        className="absolute -inset-8 hero-aurora opacity-70 blur-2xl rounded-[2rem] pointer-events-none -z-10 animate-pulse-glow"
      />

      <div className="relative surface-glass rounded-2xl overflow-hidden shadow-[0_30px_120px_-30px_rgba(220,38,38,0.55)] animate-fade-up animation-delay-200 transition-all duration-500 group-hover:shadow-[0_40px_160px_-30px_rgba(220,38,38,0.7)]">
        {/* ---------- Window chrome ---------- */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-white/10">
          <div className="flex items-center gap-1.5">
            <span className="w-3 h-3 rounded-full bg-[#ff5f57]" />
            <span className="w-3 h-3 rounded-full bg-[#febc2e]" />
            <span className="w-3 h-3 rounded-full bg-[#28c840]" />
          </div>
          <div className="text-xs text-zinc-400 font-medium tracking-tight">
            studio.youtube.com · Monetization
          </div>
          <div className="w-16 h-6" />
        </div>

        {/* ---------- Channel header ---------- */}
        <div className="px-5 pt-5 pb-4 border-b border-white/10">
          <div className="flex items-center justify-between gap-3 mb-3">
            <div className="flex items-center gap-3 min-w-0">
              <div className="w-10 h-10 rounded-full bg-gradient-to-br from-red-500 to-rose-600 flex items-center justify-center text-white text-sm font-extrabold shrink-0">
                M
              </div>
              <div className="min-w-0">
                <div className="flex items-center gap-1.5 text-sm font-semibold text-white truncate">
                  <span className="truncate">Money Blueprint</span>
                  <svg
                    viewBox="0 0 24 24"
                    className="w-4 h-4 text-zinc-400 shrink-0"
                    fill="currentColor"
                    aria-hidden="true"
                  >
                    <path d="M12 2l2.39 4.84L19.78 7.6l-3.86 3.76.91 5.3L12 14.77l-4.83 1.9.91-5.31L4.22 7.6l5.39-.76L12 2z" />
                  </svg>
                </div>
                <div className="text-[11px] text-zinc-500">
                  Finance niche · US Tier-1 audience
                </div>
              </div>
            </div>
            {/* Partner Program Active badge — first-3-sec anchor */}
            <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-emerald-500/15 border border-emerald-400/40 text-[10px] font-bold uppercase tracking-wider text-emerald-300 shrink-0">
              <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse-glow" />
              Partner Program Active
            </span>
          </div>

          {/* Tag-line stat row */}
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-[10px] text-zinc-500">312k subscribers</span>
            <span className="text-[10px] text-zinc-600">·</span>
            <span className="text-[10px] text-zinc-500">18.4M lifetime views</span>
          </div>
        </div>

        {/* ---------- Big revenue tile (the money shot) ---------- */}
        <div className="px-5 pt-5 pb-4">
          <div className="flex items-center justify-between mb-2">
            <div className="text-[10px] font-bold uppercase tracking-wider text-zinc-500">
              Estimated revenue · last 28 days
            </div>
            <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-emerald-500/15 border border-emerald-400/30 text-[10px] font-bold text-emerald-300">
              ↑ 312% YoY
            </span>
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-[44px] leading-none font-extrabold tracking-tight text-white tabular-nums">
              $4,820
            </span>
            <span className="text-xs text-zinc-500">USD</span>
          </div>
          {/* Ascending revenue chart */}
          <div
            className="flex items-end gap-1.5 h-20 mt-4"
            role="img"
            aria-label="Monthly revenue chart, 12-month ascending trend"
          >
            {REVENUE_BARS.map((h, i) => (
              <div
                key={i}
                className="flex-1 rounded-t-sm bg-gradient-to-t from-red-500/30 to-red-400/80"
                style={{ height: `${h}%` }}
              />
            ))}
          </div>
          <div className="flex justify-between text-[9px] text-zinc-600 mt-2 tracking-wider">
            <span>JAN</span>
            <span>MAR</span>
            <span>MAY</span>
            <span>JUL</span>
            <span>SEP</span>
            <span>NOW</span>
          </div>
        </div>

        {/* ---------- Top videos table ---------- */}
        <div className="px-5 pb-5 pt-2 border-t border-white/5">
          <div className="flex items-center justify-between mb-2.5">
            <div className="text-[10px] font-bold uppercase tracking-wider text-zinc-500">
              Top performing · 28 days
            </div>
            <div className="text-[10px] text-zinc-500">3 of 47 videos</div>
          </div>
          <ul className="space-y-2.5">
            {TOP_VIDEOS.map((v, i) => (
              <li key={v.title} className="flex items-center gap-3">
                <span className="text-[10px] font-bold text-zinc-500 tabular-nums w-3 shrink-0">
                  {String(i + 1).padStart(2, "0")}
                </span>
                <div className="min-w-0 flex-1">
                  <div className="text-[12px] font-medium text-white truncate">
                    {v.title}
                  </div>
                  <div className="flex items-center gap-2 mt-1">
                    <span className="text-[10px] text-zinc-500">{v.views}</span>
                    <span className="text-[10px] text-zinc-600">·</span>
                    <span className="text-[10px] text-emerald-300/80">
                      {v.ctr}
                    </span>
                  </div>
                  <div className="mt-1.5 h-1 rounded-full bg-white/[0.06] overflow-hidden">
                    <div
                      className="h-full rounded-full bg-gradient-to-r from-emerald-400 to-emerald-300"
                      style={{ width: `${v.width}%` }}
                    />
                  </div>
                </div>
                <span className="text-[10px] text-emerald-300/90 font-medium tabular-nums shrink-0">
                  {v.watchTime}
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>

      {/* Floating "Result pill" — overlayed outside the main card for
          a visual proof-step that solves the scroll-stop problem. */}
      <div className="hidden lg:flex absolute -bottom-3 right-2 surface-card px-3 py-2 items-center gap-2 shadow-[0_15px_50px_-15px_rgba(0,0,0,0.7)] animate-fade-up animation-delay-400 hover:scale-105 transition-transform">
        <span className="w-7 h-7 rounded-lg bg-gradient-to-br from-emerald-500 to-emerald-700 flex items-center justify-center">
          <svg
            viewBox="0 0 24 24"
            fill="none"
            className="w-4 h-4 text-white"
            aria-hidden="true"
          >
            <path
              d="M3 17l6-6 4 4 8-8"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
            <path
              d="M14 7h7v7"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </span>
        <div className="leading-tight">
          <div className="text-xs font-semibold text-white">$0 → $4,820/mo</div>
          <div className="text-[10px] text-zinc-500">from one channel</div>
        </div>
      </div>
    </div>
  );
}
