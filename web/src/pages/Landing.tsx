import { Link } from "react-router-dom";
import {
  Zap,
  Shield,
  Sparkles,
  PlayCircle,
  MonitorPlay,
  Plus,
} from "lucide-react";
import type { SVGProps } from "react";

/* ----------------------------------------------------------------------------
 * Demo embeds
 * Source-of-truth arrays: changing a video = change one line here, and it
 * propagates through the Shorts and Long-form sections.
 * -------------------------------------------------------------------------- */

const SHORT_DEMOS: ReadonlyArray<{ id: string; title: string }> = [
  { id: "MVwXsmRLnwM", title: "YouTube Shorts demo MVwXsmRLnwM" },
  { id: "XCIWzK2BuRo", title: "YouTube Shorts demo XCIWzK2BuRo" },
];

const LONGFORM_DEMOS: ReadonlyArray<{ id: string; title: string }> = [
  { id: "fLhv7d6N_3c", title: "YouTube long-form demo fLhv7d6N_3c" },
  { id: "iA1WT69NFbw", title: "YouTube long-form demo iA1WT69NFbw" },
  { id: "R18AVWQ92fs", title: "YouTube long-form demo R18AVWQ92fs" },
  { id: "lpKX9SKqSMw", title: "YouTube long-form demo lpKX9SKqSMw" },
];

/* ----------------------------------------------------------------------------
 * Brand SVG marks — simple, color-correct shapes (no third-party brand asset
 * needed). Sized via the className prop. They are *recognizable*, not
 * verbatim reproductions of each platform's trademark art.
 * -------------------------------------------------------------------------- */

type LogoProps = SVGProps<SVGSVGElement> & { className?: string };

function InstagramLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect x="2" y="2" width="20" height="20" rx="5" fill="#E4405F" />
      <circle cx="12" cy="12" r="4.2" stroke="#fff" strokeWidth="1.6" />
      <circle cx="17.4" cy="6.6" r="0.95" fill="#fff" />
    </svg>
  );
}

function FacebookLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect x="2" y="2" width="20" height="20" rx="4" fill="#1877F2" />
      <path
        d="M13.6 21v-7.2h2.4l.36-2.8H13.6V9.05c0-.81.23-1.35 1.4-1.35h1.5V5.15c-.26-.03-1.15-.11-2.18-.11-2.16 0-3.64 1.32-3.64 3.74v2.22H8.32v2.8h2.36V21h2.92z"
        fill="#fff"
      />
    </svg>
  );
}

function YouTubeLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect x="2" y="5" width="20" height="14" rx="3.5" fill="#FF0000" />
      <path d="M10 9.2v5.6l4.4-2.8L10 9.2z" fill="#fff" />
    </svg>
  );
}

function TikTokLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect x="2" y="2" width="20" height="20" rx="4.5" fill="#000" />
      {/* Cyan stroke (back layer) */}
      <path
        d="M15.6 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45"
        stroke="#25F4EE"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
      {/* Pink stroke (front layer, slight offset for the TikTok double-stroke look) */}
      <path
        d="M15.85 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45"
        stroke="#FE2C55"
        strokeWidth="1.7"
        strokeLinecap="round"
        transform="translate(0.5 -0.4)"
      />
      {/* Music-note stem */}
      <path
        d="M15.6 4.5a4.2 4.2 0 0 0 4.2 4.2"
        stroke="#25F4EE"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
    </svg>
  );
}

function XLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect width="24" height="24" rx="4" fill="#fff" />
      <path
        d="M14.65 11l4.05-5h-1.55l-3.45 4.34L10.85 6h-4.4l4.5 7.5L6 19h1.55l3.8-4.74L14.6 19h4l-4.65-8h.7zm-2 7l-.5-.7L7.85 7h1.4l4.4 6.3 1.95 2.7.5.7-3.45 0z"
        fill="#000"
      />
    </svg>
  );
}

function LinkedInLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect x="2" y="2" width="20" height="20" rx="3" fill="#0A66C2" />
      <circle cx="7" cy="8" r="1.15" fill="#fff" />
      <rect x="6.05" y="10" width="2.1" height="6.5" rx="0.3" fill="#fff" />
      <path
        d="M10 16.5v-6.5h2v1.1c.45-.7 1.3-1.3 2.4-1.3 1.7 0 2.6 1.1 2.6 3V16.5h-2v-3.4c0-.9-.4-1.5-1.2-1.5s-1.2.5-1.2 1.5V16.5H10z"
        fill="#fff"
      />
    </svg>
  );
}

function ThreadsLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
      {...rest}
    >
      <rect width="24" height="24" rx="6" fill="#000" />
      <path
        d="M12 6.5c2.7 0 4.7 1.6 4.7 4.7s-2 4.7-4.7 4.7-4.7-1.6-4.7-4.7"
        stroke="#fff"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
      <path
        d="M12 6.5c-3 0-5 2-5 5s2 5 5 5"
        stroke="#fff"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}

/**
 * Ordered row of every supported platform. Order is intentional — short-form
 * channels first (where the product ships fastest), then long-form, then
 * LinkedIn-style text channels. Adding a platform = add one entry here AND
 * one entry in PLATFORM_REGISTRY below.
 */
const PLATFORM_REGISTRY = [
  { key: "instagram", name: "Instagram", Logo: InstagramLogo, color: "#E4405F" },
  { key: "tiktok", name: "TikTok", Logo: TikTokLogo, color: "#25F4EE" },
  { key: "youtube", name: "YouTube", Logo: YouTubeLogo, color: "#FF0000" },
  { key: "facebook", name: "Facebook", Logo: FacebookLogo, color: "#1877F2" },
  { key: "x", name: "X", Logo: XLogo, color: "#FFFFFF" },
  { key: "linkedin", name: "LinkedIn", Logo: LinkedInLogo, color: "#0A66C2" },
  { key: "threads", name: "Threads", Logo: ThreadsLogo, color: "#FFFFFF" },
] as const;

/* ----------------------------------------------------------------------------
 * Tiny inline icons — used inside the workflow steps so we don't depend on
 * lucide-react names that don't exist in this version. Stroke-based paths
 * directly to mimic the lucide line-art look.
 * -------------------------------------------------------------------------- */

function IconUpload({ className = "w-5 h-5" }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className} aria-hidden="true">
      <path
        d="M12 16V4m0 0L7 9m5-5l5 5M5 20h14"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function IconClock({ className = "w-5 h-5" }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className} aria-hidden="true">
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="1.7" />
      <path
        d="M12 7v5l3 2"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function IconSend({ className = "w-5 h-5" }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className} aria-hidden="true">
      <path
        d="M3.5 12L20 4l-3.8 16.5L11 13l-7.5-1z"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinejoin="round"
        fill="none"
      />
      <path
        d="M11 13l9-9"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
    </svg>
  );
}

function IconChart({ className = "w-5 h-5" }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className} aria-hidden="true">
      <path
        d="M3.5 20V4M3.5 20h17"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
      <rect x="7" y="13" width="3" height="6" rx="0.6" fill="currentColor" opacity="0.55" />
      <rect x="12" y="9" width="3" height="10" rx="0.6" fill="currentColor" opacity="0.75" />
      <rect x="17" y="6" width="3" height="13" rx="0.6" fill="currentColor" />
    </svg>
  );
}

/* ----------------------------------------------------------------------------
 * Dashboard mockup — the hero right column. Pure HTML/CSS, no images, sized
 * to look like an actual scheduled-posts queue: window chrome on top, four
 * mini stat tiles, four post rows with thumbnails + brand-colored chips,
 * each row showing what the product ships in a single working session.
 *
 * The composition's purpose is "this is the product" — not just decoration.
 * Editors, marketing leads, and creators who land on the page should
 * understand within ~3 seconds that InstaEdit = a unified calendar for
 * multi-platform publishing.
 * -------------------------------------------------------------------------- */

type RowPlatform = "instagram" | "tiktok" | "youtube" | "facebook" | "x" | "linkedin" | "threads";
type MockupRow = {
  thumb: string; // tailwind bg-gradient-from-to pair
  title: string;
  meta: string;
  time: string;
  badges: ReadonlyArray<RowPlatform>;
};

const MOCKUP_ROWS: ReadonlyArray<MockupRow> = [
  {
    thumb: "from-fuchsia-500 to-violet-500",
    title: "Behind the scenes: shipping our AI pipeline",
    meta: "Vertical · auto-captioned",
    time: "Tomorrow · 09:00",
    badges: ["instagram", "linkedin", "youtube"],
  },
  {
    thumb: "from-sky-500 to-indigo-500",
    title: "Why async publishing beats 10-person teams",
    meta: "Horizontal · approved by Ana",
    time: "Wed · 14:00",
    badges: ["linkedin", "facebook"],
  },
  {
    thumb: "from-pink-500 to-orange-400",
    title: "Quarterly retrospective",
    meta: "Vertical · captions live",
    time: "Fri · 10:00",
    badges: ["instagram", "tiktok", "x"],
  },
  {
    thumb: "from-emerald-500 to-teal-400",
    title: "10,000 pieces of content: how we ship",
    meta: "Horizontal · thumbnail A/B",
    time: "Mon · 08:00",
    badges: ["youtube", "instagram"],
  },
];

function BadgeLogo({ platform }: { platform: RowPlatform }) {
  const entry = PLATFORM_REGISTRY.find((p) => p.key === platform);
  if (!entry) return null;
  const Logo = entry.Logo;
  return <Logo className="w-full h-full" />;
}

function PlatformChip({ platform }: { platform: RowPlatform }) {
  const entry = PLATFORM_REGISTRY.find((p) => p.key === platform);
  if (!entry) return null;
  return (
    <span
      className="inline-flex w-5 h-5 rounded-md overflow-hidden ring-1 ring-white/15"
      title={entry.name}
      aria-label={entry.name}
    >
      <BadgeLogo platform={platform} />
    </span>
  );
}

function DashboardMockup() {
  return (
    <div className="relative">
      {/* Outer ring + drop shadow to lift the mockup off the hero gradient */}
      <div
        aria-hidden="true"
        className="absolute -inset-px rounded-2xl bg-gradient-to-br from-white/30 via-white/5 to-white/10 blur-[2px] pointer-events-none"
      />
      <div
        aria-hidden="true"
        className="absolute -inset-8 hero-aurora opacity-60 blur-2xl rounded-[2rem] pointer-events-none -z-10 animate-pulse-glow"
      />

      <div className="relative surface-glass rounded-2xl overflow-hidden shadow-[0_30px_120px_-30px_rgba(124,58,237,0.55)] animate-fade-up animation-delay-200">
        {/* Window chrome */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-white/10">
          <div className="flex items-center gap-1.5">
            <span className="w-3 h-3 rounded-full bg-[#ff5f57]" />
            <span className="w-3 h-3 rounded-full bg-[#febc2e]" />
            <span className="w-3 h-3 rounded-full bg-[#28c840]" />
          </div>
          <div className="text-xs text-zinc-400 font-medium tracking-tight">
            instaedit.app · Calendar
          </div>
          <div className="w-12 h-6 rounded-md surface-card-soft flex items-center justify-center">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 mr-1.5" />
            <span className="text-[10px] text-zinc-300">Live</span>
          </div>
        </div>

        {/* Mini stat row */}
        <div className="grid grid-cols-4 gap-px bg-white/5 border-b border-white/10">
          {[
            { v: "12", l: "Scheduled" },
            { v: "4", l: "Platforms" },
            { v: "7d", l: "Window" },
            { v: "+", l: "New" },
          ].map((s) => (
            <div
              key={s.l}
              className="bg-[#14141c]/70 px-3 py-2.5"
            >
              <div className="text-base font-semibold text-white leading-tight">
                {s.v}
              </div>
              <div className="text-[10px] text-zinc-500 uppercase tracking-wider mt-0.5">
                {s.l}
              </div>
            </div>
          ))}
        </div>

        {/* Tabs */}
        <div className="flex items-center gap-1 px-3 py-2 border-b border-white/10 text-xs">
          <span className="px-2.5 py-1 rounded-md bg-white/10 text-white font-medium">
            Scheduled
          </span>
          <span className="px-2.5 py-1 text-zinc-500 hover:text-zinc-300 transition-colors cursor-default">
            All
          </span>
          <span className="px-2.5 py-1 text-zinc-500 hover:text-zinc-300 transition-colors cursor-default">
            Drafts
          </span>
          <span className="px-2.5 py-1 text-zinc-500 hover:text-zinc-300 transition-colors cursor-default">
            Published
          </span>
          <span className="ml-auto inline-flex items-center gap-1 text-violet-300/90 text-[11px] font-medium">
            <Plus className="w-3 h-3" />
            New post
          </span>
        </div>

        {/* Queue rows */}
        <ul className="divide-y divide-white/5">
          {MOCKUP_ROWS.map((row) => (
            <li
              key={row.title}
              className="flex items-center gap-3 px-4 py-3.5 hover:bg-white/[0.03] transition-colors"
            >
              <div
                className={`w-12 h-12 rounded-lg bg-gradient-to-br ${row.thumb} ring-1 ring-white/10 flex-shrink-0`}
              />
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium text-white truncate">
                  {row.title}
                </div>
                <div className="flex items-center gap-2 mt-1">
                  <div className="flex -space-x-1.5">
                    {row.badges.map((b) => (
                      <PlatformChip key={b} platform={b} />
                    ))}
                  </div>
                  <span className="text-[11px] text-zinc-500 truncate">
                    · {row.meta}
                  </span>
                </div>
              </div>
              <div className="text-[11px] text-zinc-400 flex-shrink-0 tabular-nums">
                {row.time}
              </div>
            </li>
          ))}
        </ul>

        {/* Footer strip */}
        <div className="flex items-center justify-between px-4 py-2.5 border-t border-white/10 bg-[#14141c]/60">
          <div className="text-[11px] text-zinc-500">
            12 of 28 posts scheduled this week
          </div>
          <div className="flex items-center gap-1.5 text-[11px] text-emerald-300/90 font-medium">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse-glow" />
            Auto-publish on
          </div>
        </div>
      </div>

      {/* Floating "+ New post" badge — peeks from the bottom-right corner */}
      <div className="hidden lg:flex absolute -bottom-3 right-2 surface-card px-3 py-2 items-center gap-2 shadow-[0_15px_50px_-15px_rgba(0,0,0,0.7)] animate-fade-up animation-delay-400">
        <span className="w-7 h-7 rounded-lg bg-gradient-to-br from-violet-500 to-fuchsia-500 flex items-center justify-center">
          <Plus className="w-4 h-4 text-white" />
        </span>
        <div className="leading-tight">
          <div className="text-xs font-semibold text-white">200 → 8 posts</div>
          <div className="text-[10px] text-zinc-500">in one click</div>
        </div>
      </div>
    </div>
  );
}

/* ----------------------------------------------------------------------------
 * YouTube embed — kept verbatim from the prior landing. `playsinline=1` and
 * the allow-list we use here match what YouTube's iframe API requires for
 * Shorts/Reels/TikTok-style previews to render correctly in modern Safari +
 * Firefox (autoplay, encrypted-media, picture-in-picture).
 * -------------------------------------------------------------------------- */
function YouTubeEmbed({
  id,
  title,
  aspect,
}: {
  id: string;
  title: string;
  aspect: "9/16" | "16/9";
}) {
  // Aspect literal is computed from a string literal so Tailwind's JIT
  // picks up `aspect-[9/16]` and `aspect-[16/9]` as static classes.
  const aspectClass =
    aspect === "9/16" ? "aspect-[9/16]" : "aspect-[16/9]";
  return (
    <div className="relative overflow-hidden rounded-2xl border border-white/15 bg-[#0a0a12] shadow-[0_25px_80px_-25px_rgba(0,0,0,0.85)]">
      <div className={aspectClass}>
        <iframe
          className="w-full h-full"
          src={`https://www.youtube.com/embed/${id}?playsinline=1`}
          title={title}
          loading="lazy"
          allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
          allowFullScreen
          referrerPolicy="strict-origin-when-cross-origin"
        />
      </div>
    </div>
  );
}

/* ----------------------------------------------------------------------------
 * Sticky top nav. max-w-7xl so it scales visually with the hero instead of
 * capping at max-w-4xl like before.
 * -------------------------------------------------------------------------- */
function Nav() {
  return (
    <nav className="fixed top-0 left-0 right-0 z-50">
      <div className="surface-glass border-b border-white/10">
        <div className="mx-auto max-w-7xl h-16 px-6 flex items-center justify-between">
          <Link to="/" className="flex items-center gap-2 group">
            <span className="inline-flex w-7 h-7 items-center justify-center rounded-md bg-white text-black shadow-[0_0_24px_-6px_rgba(255,255,255,0.4)]">
              <Zap className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-white text-sm">
              InstaEdit
            </span>
          </Link>
          <div className="hidden sm:flex items-center gap-7 text-sm font-medium text-zinc-400">
            <a href="#workflow" className="hover:text-white transition-colors">
              Workflow
            </a>
            <a href="#features" className="hover:text-white transition-colors">
              Features
            </a>
            <a href="#who-are-we" className="hover:text-white transition-colors">
              Who I am
            </a>
            <Link
              to="/editor"
              className="hover:text-white transition-colors inline-flex items-center gap-1.5"
            >
              Editor
              <span className="inline-flex items-center px-1.5 py-0.5 text-[10px] font-semibold rounded bg-violet-500/20 text-violet-200 ring-1 ring-violet-400/30">
                New
              </span>
            </Link>
          </div>

        </div>
      </div>
    </nav>
  );
}

/* ----------------------------------------------------------------------------
 * Hero — two-column at lg, stacked at smaller breakpoints. Left: eyebrow +
 * h1 (text-display-1) + body + CTAs + trust row. Right: DashboardMockup.
 * -------------------------------------------------------------------------- */
function Hero() {
  return (
    <section className="relative pt-32 pb-20 overflow-hidden">
      {/* Aurora + grid background — visible color, not opacity-20 wash */}
      <div aria-hidden="true" className="absolute inset-0 hero-aurora pointer-events-none" />
      <div aria-hidden="true" className="absolute inset-0 grid-bg pointer-events-none opacity-60" />
      {/* Floating orbs (animate-drift-*) — sized larger than before */}
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[460px] h-[460px] -top-32 -left-24 animate-drift-slow opacity-70" />
        <div className="glow-orb bg-cyan-400 w-[420px] h-[420px] -bottom-40 -right-24 animate-drift-rev opacity-60" />
      </div>

      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-10 items-center">
        <div className="lg:col-span-7 xl:col-span-6 animate-fade-up">
          {/* Eyebrow pill */}
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-7">
            <span className="relative flex h-2 w-2">
              <span className="animate-pulse-glow absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-400" />
            </span>
            <span>7 platforms · one workflow · zero reuploads</span>
          </div>

          <h1 className="text-display-1 text-white">
            Publish <span className="text-gradient">once.</span>
            <br />
            Ship to <span className="text-gradient">every channel.</span>
          </h1>

          <p className="text-body-lg text-zinc-300/90 mt-7 max-w-[60ch]">
            InstaEdit turns one render into 7 platform-native posts — scheduled,
            captioned, and published from a single calendar. Built for teams
            running 10,000+ pieces of content a month.
          </p>



          {/* Trust row — small platform chips with copy */}
          <div className="mt-10 flex items-center gap-4 flex-wrap">
            <span className="text-eyebrow text-zinc-500">Works with</span>
            <div className="flex items-center gap-2">
              {PLATFORM_REGISTRY.map(({ key, name, Logo }) => (
                <span
                  key={key}
                  className="inline-flex w-7 h-7 rounded-md overflow-hidden ring-1 ring-white/15 surface-glass"
                  title={name}
                  aria-label={name}
                >
                  <Logo className="w-full h-full" />
                </span>
              ))}
            </div>
            <span className="text-xs text-zinc-500">+ more every quarter</span>
          </div>
        </div>

        <div className="lg:col-span-5 xl:col-span-6 relative">
          <DashboardMockup />
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Stats strip — single inline row, NOT 4 identical boxes. Used as a thin
 * "between-hero-and-content" social-proof line.
 * -------------------------------------------------------------------------- */
function StatsStrip() {
  const stats: Array<{ v: string; l: string }> = [
    { v: "10,000+", l: "Posts / mo" },
    { v: "7", l: "Platforms" },
    { v: "50+", l: "Creator teams" },
    { v: "99.9%", l: "Publishing uptime" },
  ];
  return (
    <section className="relative py-10 border-y border-white/10 bg-[#0c0c14]/60">
      <div className="mx-auto max-w-7xl px-6">
        <ul className="grid grid-cols-2 sm:grid-cols-4 gap-y-6 gap-x-8 text-center sm:text-left">
          {stats.map((s, i) => (
            <li
              key={s.l}
              className={`flex items-center ${
                i < stats.length - 1
                  ? "sm:border-r sm:border-white/10 sm:pr-8"
                  : ""
              } justify-center sm:justify-start gap-4`}
            >
              <span className="text-3xl sm:text-4xl font-extrabold text-white tabular-nums tracking-tight">
                {s.v}
              </span>
              <span className="text-eyebrow text-zinc-500 max-w-[12ch]">
                {s.l}
              </span>
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Workflow — 4 cards in a row at lg, stacked on mobile. A gradient line
 * connects them so the eye reads it as a sequence, not a feature grid.
 *
 * Each step = Lucide-equivalent inline icon + number + title + description.
 * Avoids making these look identical by giving each step its own accent hue
 * (violet / cyan / magenta / amber). Hue maps to the platform cluster the
 * step primarily touches.
 * -------------------------------------------------------------------------- */
function Workflow() {
  // Each step has its own accent hue mapped to the platform cluster it
  // primarily touches — violet (input), cyan (timing), magenta (publish),
  // amber (analytics). `glow` is intentionally omitted; per-step color
  // already comes from `accent` + `iconColor` + `ring`.
  const steps = [
    {
      n: "01",
      title: "Upload once",
      copy: "Drop a vertical or horizontal render. We auto-encode for every target platform.",
      Icon: IconUpload,
      accent: "from-violet-500/30 to-violet-500/0",
      ring: "ring-violet-400/40",
      iconColor: "text-violet-300",
    },
    {
      n: "02",
      title: "Schedule once",
      copy: "Pick a time. We instantly fan out per-platform slots so audiences see it natively.",
      Icon: IconClock,
      accent: "from-cyan-500/30 to-cyan-500/0",
      ring: "ring-cyan-400/40",
      iconColor: "text-cyan-300",
    },
    {
      n: "03",
      title: "Publish everywhere",
      copy: "Captions, hashtags, thumbnails, and chapters generated per platform — in one click.",
      Icon: IconSend,
      accent: "from-pink-500/30 to-pink-500/0",
      ring: "ring-pink-400/40",
      iconColor: "text-pink-300",
    },
    {
      n: "04",
      title: "Track in one view",
      copy: "Reach, engagement, and publishing status across all 7 channels in a single dashboard.",
      Icon: IconChart,
      accent: "from-amber-500/30 to-amber-500/0",
      ring: "ring-amber-400/40",
      iconColor: "text-amber-300",
    },
  ];

  return (
    <section id="workflow" className="relative py-24 sm:py-32">
      {/* Connecting gradient line — drawn behind the cards */}
      <div
        aria-hidden="true"
        className="hidden lg:block absolute top-[58%] left-0 right-0 h-px bg-gradient-to-r from-transparent via-violet-400/40 to-transparent pointer-events-none"
      />

      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">
            The workflow
          </div>
          <h2 className="text-display-2 text-white">
            From render to <span className="text-gradient">every channel</span> in
            four steps.
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            You push one render. InstaEdit handles encoding, captions,
            thumbnails, scheduling, and publishing — without you opening
            another tab.
          </p>
        </div>

        <ol className="grid sm:grid-cols-2 lg:grid-cols-4 gap-5 relative">
          {steps.map((s, i) => (
            <li
              key={s.n}
              className={`surface-card p-6 relative overflow-hidden animate-fade-up ${
                ["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]
              }`}
            >
              {/* Accent corner gradient — gives each card personality */}
              <div
                aria-hidden="true"
                className={`absolute -top-20 -right-20 w-56 h-56 rounded-full bg-radial ${s.accent} opacity-70 blur-2xl pointer-events-none`}
              />
              <div className="relative">
                <div className="flex items-center justify-between mb-5">
                  <span
                    className={`inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ${s.ring} surface-glass ${s.iconColor}`}
                  >
                    <s.Icon className="w-5 h-5" />
                  </span>
                  <span className="text-eyebrow text-zinc-500 tabular-nums">
                    Step {s.n}
                  </span>
                </div>
                <h3 className="text-display-3 text-white mb-2">{s.title}</h3>
                <p className="text-sm text-zinc-400 leading-relaxed">
                  {s.copy}
                </p>
              </div>
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Features — 4 tiles with distinct visuals. The big tile spans 2 cols on lg
 * and acts as the visual anchor (the dashboard preview). Each small tile
 * pairs a different brand-color accent so the row doesn't read as 4 grey
 * boxes.
 * -------------------------------------------------------------------------- */
function Features() {
  return (
    <section
      id="features"
      className="relative py-24 sm:py-32 bg-elevated overflow-hidden"
    >
      <div
        aria-hidden="true"
        className="absolute inset-0 hero-aurora opacity-25 pointer-events-none"
      />

      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">
            What's inside
          </div>
          <h2 className="text-display-2 text-white">
            Everything you need to ship content at scale.
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Built for production teams. Calm interface, no per-platform tab
            juggling.
          </p>
        </div>

        <div className="grid lg:grid-cols-3 gap-5">
          {/* Banner tile — the visual anchor */}
          <div className="surface-card p-7 lg:p-8 relative overflow-hidden lg:col-span-2 lg:row-span-2 animate-fade-up">
            <div
              aria-hidden="true"
              className="absolute -top-32 -right-32 w-80 h-80 rounded-full bg-violet-500 blur-3xl opacity-50"
            />
            <div
              aria-hidden="true"
              className="absolute bottom-0 left-0 right-0 h-px bg-gradient-to-r from-transparent via-violet-400/50 to-transparent"
            />
            <div className="relative">
              <div className="inline-flex w-12 h-12 rounded-xl items-center justify-center ring-1 ring-violet-400/40 surface-glass text-violet-300 mb-5">
                <Sparkles className="w-6 h-6" />
              </div>
              <h3 className="text-display-3 text-white mb-3 max-w-[22ch]">
                One dashboard, every platform.
              </h3>
              <p className="text-sm text-zinc-400 leading-relaxed max-w-[52ch]">
                Manage Instagram, TikTok, YouTube, X, LinkedIn, Facebook, and
                Threads from a single calendar. Status indicators show what's
                published, scheduled, or needs a human.
              </p>

              {/* Mini interactive-looking dashboard preview inside the tile */}
              <div className="mt-7 surface-glass rounded-xl border border-white/10 overflow-hidden">
                <div className="flex items-center gap-1.5 px-4 py-2.5 border-b border-white/5">
                  <span className="w-2.5 h-2.5 rounded-full bg-[#ff5f57]" />
                  <span className="w-2.5 h-2.5 rounded-full bg-[#febc2e]" />
                  <span className="w-2.5 h-2.5 rounded-full bg-[#28c840]" />
                  <span className="ml-3 text-[11px] text-zinc-500">
                    Calendar · this week
                  </span>
                </div>
                <div className="grid grid-cols-7 gap-1.5 p-3 text-center text-[10px] text-zinc-500">
                  {["M", "T", "W", "T", "F", "S", "S"].map((d, idx) => (
                    <div
                      key={`${d}${idx}`}
                      className="rounded-md border border-white/5 bg-black/20 py-2.5"
                    >
                      <div className="text-eyebrow text-zinc-600 mb-1.5">{d}</div>
                      <div className="space-y-1">
                        {[1, 2].slice(0, idx % 2 === 0 ? 2 : 1).map((i) => (
                          <div
                            key={i}
                            className={`h-1.5 rounded-full mx-1 ${
                              i === 1
                                ? "bg-violet-400/70"
                                : "bg-cyan-400/70"
                            }`}
                          />
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          </div>

          {/* Tile — Schedule */}
          <div className="surface-card p-6 relative overflow-hidden animate-fade-up animation-delay-100">
            <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-cyan-400/40 surface-glass text-cyan-300 mb-4">
              <IconClock className="w-5 h-5" />
            </div>
            <h3 className="text-display-3 text-white mb-2">
              Schedule once per platform.
            </h3>
            <p className="text-sm text-zinc-400 leading-relaxed">
              Audience-tuned slots auto-distribute content so each platform
              posts at peak engagement time.
            </p>
          </div>

          {/* Tile — Approval flows */}
          <div className="surface-card p-6 relative overflow-hidden animate-fade-up animation-delay-200">
            <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-pink-400/40 surface-glass text-pink-300 mb-4">
              <Shield className="w-5 h-5" />
            </div>
            <h3 className="text-display-3 text-white mb-2">
              Approval flows built in.
            </h3>
            <p className="text-sm text-zinc-400 leading-relaxed">
              Route drafts through review, lock finals, and ship with audit
              trails on every account.
            </p>
          </div>

          {/* Tile — Analytics — full-width on its own row below the grid */}
          <div className="surface-card p-6 relative overflow-hidden lg:col-span-3 animate-fade-up animation-delay-300">
            <div
              aria-hidden="true"
              className="absolute -bottom-24 -right-24 w-72 h-72 rounded-full bg-amber-500/30 blur-3xl pointer-events-none"
            />
            <div className="relative grid lg:grid-cols-2 gap-6 items-center">
              <div>
                <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-amber-400/40 surface-glass text-amber-300 mb-4">
                  <IconChart className="w-5 h-5" />
                </div>
                <h3 className="text-display-3 text-white mb-2">
                  Analytics that match the post.
                </h3>
                <p className="text-sm text-zinc-400 leading-relaxed max-w-[52ch]">
                  Compare reach, engagement, and publishing status across all
                  channels in a unified view — no per-platform exports.
                </p>
              </div>
              {/* Inline chart sketch */}
              <div className="surface-glass rounded-xl border border-white/10 p-5">
                <div className="flex items-end justify-between gap-2 h-24">
                  {[42, 64, 38, 78, 56, 92, 70, 88, 60, 96, 74, 84].map(
                    (h, i) => (
                      <div
                        key={i}
                        className="flex-1 rounded-t-sm bg-gradient-to-t from-violet-500/60 to-cyan-400/90"
                        style={{ height: `${h}%` }}
                      />
                    ),
                  )}
                </div>
                <div className="flex justify-between text-[10px] text-zinc-500 mt-2">
                  <span>Jan</span>
                  <span>Mar</span>
                  <span>May</span>
                  <span>Jul</span>
                  <span>Sep</span>
                  <span>Nov</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Shorts section — preserves the embedded YouTube IDs. Wider / taller framing
 * than the previous sub-card design so the embeds feel like proof, not
 * decorations.
 * -------------------------------------------------------------------------- */
function ShortsSection() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-gradient-to-br from-violet-500/15 via-transparent to-fuchsia-500/10 pointer-events-none"
      />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[380px] h-[380px] -top-20 -right-32 animate-drift-slow opacity-50" />
        <div className="glow-orb bg-fuchsia-500 w-[340px] h-[340px] -bottom-32 -left-24 animate-drift-rev opacity-40" />
      </div>

      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-12 items-center">
        <div className="lg:col-span-5 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-4 inline-flex items-center gap-2">
            <PlayCircle className="w-4 h-4" />
            Short-form video
          </div>
          <h2 className="text-display-2 text-white mb-5">
            One vertical render.{" "}
            <span className="text-gradient">Three platforms.</span> Zero
            reuploads.
          </h2>
          <p className="text-body-lg text-zinc-400 max-w-[52ch] mb-7">
            InstaEdit handles the quirks of each short-form platform — aspect
            ratio, length caps, descriptions, thumbnails — so a single
            vertical render lands correctly on YouTube Shorts, Instagram
            Reels, and TikTok.
          </p>

          <ul className="space-y-3 text-sm">
            {[
              { c: "#FF0000", l: "YouTube Shorts" },
              { c: "#E4405F", l: "Instagram Reels" },
              { c: "#FFFFFF", l: "TikTok" },
            ].map((p) => (
              <li key={p.l} className="flex items-center gap-3">
                <span
                  className="w-2.5 h-2.5 rounded-full"
                  style={{
                    background: p.c,
                    boxShadow: `0 0 12px ${p.c}`,
                  }}
                />
                <span className="text-zinc-200 font-medium">{p.l}</span>
                <span className="text-zinc-600">·</span>
                <span className="text-zinc-500">Native format</span>
              </li>
            ))}
          </ul>
        </div>

        <div className="lg:col-span-7 grid grid-cols-1 sm:grid-cols-2 gap-5 animate-fade-up animation-delay-200">
          {SHORT_DEMOS.map((demo) => (
            <YouTubeEmbed
              key={demo.id}
              id={demo.id}
              title={demo.title}
              aspect="9/16"
            />
          ))}
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Long-form section — horizontal embeds + 2x2 grid so they read as a real
 * distributed-channel proof.
 * -------------------------------------------------------------------------- */
function LongFormSection() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-gradient-to-tr from-cyan-500/15 via-transparent to-pink-500/15 pointer-events-none"
      />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-cyan-400 w-[400px] h-[400px] -top-32 -left-32 animate-drift-rev opacity-50" />
        <div className="glow-orb bg-pink-500 w-[360px] h-[360px] -bottom-32 -right-32 animate-drift-slow opacity-40" />
      </div>

      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-12 items-center">
        <div className="lg:col-span-7 lg:order-2 animate-fade-up">
          <div className="text-eyebrow text-cyan-300/90 mb-4 inline-flex items-center gap-2">
            <MonitorPlay className="w-4 h-4" />
            Long-form video
          </div>
          <h2 className="text-display-2 text-white mb-5">
            Horizontal masters,{" "}
            <span className="text-gradient">shipped everywhere.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 max-w-[52ch] mb-7 lg:ml-auto">
            Resumable uploads, descriptions, thumbnails, and chapter markers —
            so a single horizontal render lands correctly on YouTube,
            Instagram, Facebook, and LinkedIn.
          </p>

          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 lg:justify-end">
            {[
              { c: "#FF0000", l: "YouTube" },
              { c: "#E4405F", l: "Instagram" },
              { c: "#1877F2", l: "Facebook" },
              { c: "#0A66C2", l: "LinkedIn" },
            ].map((p) => (
              <div
                key={p.l}
                className="flex items-center gap-2 px-3 py-2 rounded-lg surface-glass border border-white/10"
              >
                <span
                  className="w-2 h-2 rounded-full"
                  style={{
                    background: p.c,
                    boxShadow: `0 0 10px ${p.c}`,
                  }}
                />
                <span className="text-sm text-zinc-200 font-medium">
                  {p.l}
                </span>
              </div>
            ))}
          </div>
        </div>

        <div className="lg:col-span-5 lg:order-1 grid grid-cols-1 sm:grid-cols-2 gap-5 animate-fade-up animation-delay-200">
          {LONGFORM_DEMOS.slice(0, 2).map((demo) => (
            <YouTubeEmbed
              key={demo.id}
              id={demo.id}
              title={demo.title}
              aspect="16/9"
            />
          ))}
        </div>
      </div>

      {/* Below: the other two long-form demos as a quieter ribbon — proves
          the same render is on four surfaces, not just two. */}
      <div className="relative mx-auto max-w-7xl px-6 mt-10 grid grid-cols-1 sm:grid-cols-2 gap-5">
        {LONGFORM_DEMOS.slice(2).map((demo) => (
          <YouTubeEmbed
            key={demo.id}
            id={demo.id}
            title={demo.title}
            aspect="16/9"
          />
        ))}
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Footer — multi-column link grid + tagline at top. Replaces the single-row
 * footer so the page feels like a product, not a placeholder.
 * -------------------------------------------------------------------------- */
function Footer() {
  // Two-column footer. We only expose links we *actually* have a destination
  // for: the older draft shipped Company + Resources with `href="#"` entries
  // (About, Contact, Status, Docs, Changelog, Help center) which all 404'd
  // in production. Re-add those columns once their routes/pages exist —
  // dropping the broken ones is more honest than placeholder entries that
  // land on whatever top-of-page hash the URL has.
  //
  // `/data-deletion.html` resolves to web/public/data-deletion.html and is
  // served by Vercel as a static file before any React route matching
  // `web/vercel.json` rules. The static file is intentional and required by
  // Meta's data-deletion callback-URL spec; do not collapse it into a
  // React route (would break the public callback contract).
  const cols: Array<{ heading: string; links: Array<{ l: string; to?: string; href?: string }> }> = [
    {
      heading: "Product",
      links: [
        { l: "Workflow", href: "#workflow" },
        { l: "Features", href: "#features" },
      ],
    },
    {
      heading: "Legal",
      links: [
        { l: "Privacy", to: "/privacy" },
        { l: "Terms", to: "/terms" },
        { l: "Data deletion", href: "/data-deletion.html" },
      ],
    },
  ];

  return (
    <footer className="relative border-t border-white/10 bg-[#08080d]">
      <div className="mx-auto max-w-7xl px-6 py-16 grid gap-12 lg:grid-cols-12">
        <div className="lg:col-span-5">
          <Link to="/" className="flex items-center gap-2">
            <span className="inline-flex w-8 h-8 items-center justify-center rounded-lg bg-white text-black">
              <Zap className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-white text-base">
              InstaEdit
            </span>
          </Link>
          <p className="text-sm text-zinc-400 mt-4 max-w-[42ch] leading-relaxed">
            Multi-platform publishing infrastructure for teams that ship
            content every day. One render, every channel, every time.
          </p>
          <div className="flex items-center gap-2 mt-5">
            {PLATFORM_REGISTRY.map(({ key, name, Logo }) => (
              <span
                key={key}
                className="inline-flex w-7 h-7 rounded-md overflow-hidden ring-1 ring-white/10 surface-glass"
                title={name}
                aria-label={name}
              >
                <Logo className="w-full h-full" />
              </span>
            ))}
          </div>
        </div>

        {/* mobile-first: stack Product + Legal vertically until sm (640px),
            then 2-up. Each sub-col on a 390px phone would otherwise cram
            4 stacked links into ~150px and wrap "Data deletion" awkwardly. */}
        <div className="lg:col-span-7 grid grid-cols-1 sm:grid-cols-2 gap-8">
          {cols.map((col) => (
            <div key={col.heading}>
              <div className="text-eyebrow text-zinc-500 mb-4">
                {col.heading}
              </div>
              <ul className="space-y-3">
                {col.links.map((link) => {
                  const className =
                    "text-sm text-zinc-300 hover:text-white transition-colors";
                  if (link.to) {
                    return (
                      <li key={link.l}>
                        <Link to={link.to} className={className}>
                          {link.l}
                        </Link>
                      </li>
                    );
                  }
                  return (
                    <li key={link.l}>
                      <a href={link.href} className={className}>
                        {link.l}
                      </a>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </div>
      </div>

      <div className="border-t border-white/5">
        <div className="mx-auto max-w-7xl px-6 py-6 flex flex-col sm:flex-row items-center justify-between gap-3 text-xs text-zinc-500">
          <div>© {new Date().getFullYear()} InstaEdit, Inc.</div>
          <div>Built for creators and content ops teams.</div>
        </div>
      </div>
    </footer>
  );
}

/* ----------------------------------------------------------------------------
 * Who are we — short brand-story section placed right after the hero so
 * visitors immediately understand who is behind InstaEdit. Two-column at
 * lg, stacked on mobile.
 * -------------------------------------------------------------------------- */
function WhoAreWe() {
  return (
    <section
      id="who-are-we"
      className="relative py-24 sm:py-32 overflow-hidden bg-elevated"
    >
      <div
        aria-hidden="true"
        className="absolute inset-0 hero-aurora opacity-20 pointer-events-none"
      />

      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-2 gap-12 items-center">
        <div className="animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">
            Who we are
          </div>
          <h2 className="text-display-2 text-white mb-5">
            We build the infrastructure behind{" "}
            <span className="text-gradient">your content.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 max-w-[55ch] mb-6">
            InstaEdit is built by a small, focused team obsessed with the
            pain of multi-platform publishing. We lived the 14-tab,
            7-export workflow ourselves — and decided to replace it with
            a single pipeline that just works.
          </p>
          <p className="text-body-lg text-zinc-400 max-w-[55ch] mb-8">
            Our mission is simple: let creators and content teams focus on
            making great content while we handle every encoding, caption,
            thumbnail, and scheduling task across all platforms.
          </p>

          <div className="grid grid-cols-3 gap-4">
            {[
              { v: "7", l: "Platforms" },
              { v: "50+", l: "Languages" },
              { v: "24/7", l: "Uptime" },
            ].map((s) => (
              <div key={s.l} className="surface-card p-4 text-center">
                <div className="text-xl font-bold text-white tabular-nums">
                  {s.v}
                </div>
                <div className="text-eyebrow text-zinc-500 mt-1">{s.l}</div>
              </div>
            ))}
          </div>
        </div>

        <div className="relative animate-fade-up animation-delay-200">
          <div className="surface-glass border border-white/15 rounded-2xl p-8 relative overflow-hidden shadow-[0_30px_100px_-40px_rgba(124,58,237,0.4)]">
            <div
              aria-hidden="true"
              className="absolute -top-20 -right-20 w-60 h-60 rounded-full bg-violet-500/25 blur-3xl pointer-events-none"
            />
            <div className="relative">
              <div className="flex items-center gap-3 mb-6">
                <span className="inline-flex w-10 h-10 items-center justify-center rounded-xl bg-white text-black shadow-[0_0_20px_-4px_rgba(255,255,255,0.3)]">
                  <Zap className="w-5 h-5" />
                </span>
                <div>
                  <div className="text-sm font-semibold text-white">InstaEdit</div>
                  <div className="text-[11px] text-zinc-500">Multi-platform publishing</div>
                </div>
              </div>

              <blockquote className="border-l-2 border-violet-400/50 pl-4 mb-6">
                <p className="text-sm text-zinc-300 leading-relaxed italic">
                  "We started InstaEdit because publishing to 7 platforms
                  felt like a full-time job. Now it takes one afternoon."
                </p>
              </blockquote>

              <div className="space-y-3 text-sm">
                {[
                  "Built for teams shipping 10K+ posts/month",
                  "One render → 7 platform-native outputs",
                  "From idea to published — in minutes, not hours",
                  "No per-platform tab juggling required",
                ].map((line) => (
                  <div key={line} className="flex items-start gap-2.5">
                    <span className="mt-0.5 w-1.5 h-1.5 rounded-full bg-violet-400 flex-shrink-0" />
                    <span className="text-zinc-300">{line}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Main landing export.
 * -------------------------------------------------------------------------- */
export function Landing() {
  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] font-sans antialiased overflow-x-hidden selection:bg-violet-500/40 selection:text-white">
      <Nav />
      <main className="relative">
        <Hero />
        <WhoAreWe />
        <StatsStrip />
        <Workflow />
        <Features />
        <ShortsSection />
        <LongFormSection />

      </main>
      <Footer />
    </div>
  );
}
