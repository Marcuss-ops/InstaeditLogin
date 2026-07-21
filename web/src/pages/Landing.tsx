import { useState, useCallback, useEffect } from "react";
import { Link } from "react-router-dom";
import { Seo } from "../components/seo/Seo";
import {
  Zap,
  Shield,
  Sparkles,
  PlayCircle,
  MonitorPlay,
  Plus,
  Lightbulb,
  Film,
  Cpu,
  CalendarClock,
  Globe,
  BarChart3,
  Users,
  Building2,
  Languages,
  RefreshCw,
  Menu,
  X,
  ArrowRight,
  CheckCircle2,
  Target,
  Palette,
  Headphones,
} from "lucide-react";
import type { SVGProps } from "react";

/* ----------------------------------------------------------------------------
 * Demo embeds
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
 * Brand SVG marks
 * -------------------------------------------------------------------------- */

type LogoProps = SVGProps<SVGSVGElement> & { className?: string };

function InstagramLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect x="2" y="2" width="20" height="20" rx="5" fill="#E4405F" />
      <circle cx="12" cy="12" r="4.2" stroke="#fff" strokeWidth="1.6" />
      <circle cx="17.4" cy="6.6" r="0.95" fill="#fff" />
    </svg>
  );
}

function FacebookLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect x="2" y="2" width="20" height="20" rx="4" fill="#1877F2" />
      <path d="M13.6 21v-7.2h2.4l.36-2.8H13.6V9.05c0-.81.23-1.35 1.4-1.35h1.5V5.15c-.26-.03-1.15-.11-2.18-.11-2.16 0-3.64 1.32-3.64 3.74v2.22H8.32v2.8h2.36V21h2.92z" fill="#fff" />
    </svg>
  );
}

function YouTubeLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect x="2" y="5" width="20" height="14" rx="3.5" fill="#FF0000" />
      <path d="M10 9.2v5.6l4.4-2.8L10 9.2z" fill="#fff" />
    </svg>
  );
}

function TikTokLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect x="2" y="2" width="20" height="20" rx="4.5" fill="#000" />
      <path d="M15.6 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45" stroke="#25F4EE" strokeWidth="1.7" strokeLinecap="round" />
      <path d="M15.85 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45" stroke="#FE2C55" strokeWidth="1.7" strokeLinecap="round" transform="translate(0.5 -0.4)" />
      <path d="M15.6 4.5a4.2 4.2 0 0 0 4.2 4.2" stroke="#25F4EE" strokeWidth="1.7" strokeLinecap="round" />
    </svg>
  );
}

function XLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect width="24" height="24" rx="4" fill="#fff" />
      <path d="M14.65 11l4.05-5h-1.55l-3.45 4.34L10.85 6h-4.4l4.5 7.5L6 19h1.55l3.8-4.74L14.6 19h4l-4.65-8h.7zm-2 7l-.5-.7L7.85 7h1.4l4.4 6.3 1.95 2.7.5.7-3.45 0z" fill="#000" />
    </svg>
  );
}

function LinkedInLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect x="2" y="2" width="20" height="20" rx="3" fill="#0A66C2" />
      <circle cx="7" cy="8" r="1.15" fill="#fff" />
      <rect x="6.05" y="10" width="2.1" height="6.5" rx="0.3" fill="#fff" />
      <path d="M10 16.5v-6.5h2v1.1c.45-.7 1.3-1.3 2.4-1.3 1.7 0 2.6 1.1 2.6 3V16.5h-2v-3.4c0-.9-.4-1.5-1.2-1.5s-1.2.5-1.2 1.5V16.5H10z" fill="#fff" />
    </svg>
  );
}

function ThreadsLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
      <rect width="24" height="24" rx="6" fill="#000" />
      <path d="M12 6.5c2.7 0 4.7 1.6 4.7 4.7s-2 4.7-4.7 4.7-4.7-1.6-4.7-4.7" stroke="#fff" strokeWidth="1.4" strokeLinecap="round" />
      <path d="M12 6.5c-3 0-5 2-5 5s2 5 5 5" stroke="#fff" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

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
 * Inline SVG icons
 * -------------------------------------------------------------------------- */

function IconSchedule({ className = "w-5 h-5" }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className} aria-hidden="true">
      <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="1.7" />
      <path d="M12 7v5l3 2" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function IconAnalyze({ className = "w-5 h-5" }: LogoProps) {
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

type RowPlatform = "instagram" | "tiktok" | "youtube" | "facebook" | "x" | "linkedin" | "threads";
type MockupRow = { thumb: string; title: string; meta: string; time: string; badges: ReadonlyArray<RowPlatform> };

const MOCKUP_ROWS: ReadonlyArray<MockupRow> = [
  { thumb: "from-fuchsia-500 to-violet-500", title: "Behind the scenes: shipping our AI pipeline", meta: "Vertical · auto-captioned", time: "Tomorrow · 09:00", badges: ["instagram", "linkedin", "youtube"] },
  { thumb: "from-sky-500 to-indigo-500", title: "Why async publishing beats 10-person teams", meta: "Horizontal · approved by Ana", time: "Wed · 14:00", badges: ["linkedin", "facebook"] },
  { thumb: "from-pink-500 to-orange-400", title: "Quarterly retrospective", meta: "Vertical · captions live", time: "Fri · 10:00", badges: ["instagram", "tiktok", "x"] },
  { thumb: "from-emerald-500 to-teal-400", title: "10,000 pieces of content: how we ship", meta: "Horizontal · thumbnail A/B", time: "Mon · 08:00", badges: ["youtube", "instagram"] },
];

function BadgeLogo({ platform }: { platform: RowPlatform }) {
  const entry = PLATFORM_REGISTRY.find((p) => p.key === platform);
  if (!entry) return null;
  return <entry.Logo className="w-full h-full" />;
}

function PlatformChip({ platform }: { platform: RowPlatform }) {
  const entry = PLATFORM_REGISTRY.find((p) => p.key === platform);
  if (!entry) return null;
  return (
    <span className="inline-flex w-5 h-5 rounded-md overflow-hidden ring-1 ring-white/15" title={entry.name} aria-label={entry.name}>
      <BadgeLogo platform={platform} />
    </span>
  );
}

function DashboardMockup() {
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

function YouTubeEmbed({ id, title, aspect }: { id: string; title: string; aspect: "9/16" | "16/9" }) {
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

/* ----------------------------------------------------------------------------
 * Sticky Nav with mobile hamburger
 * -------------------------------------------------------------------------- */

function Nav() {
  const [open, setOpen] = useState(false);

  // Close on escape
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  // Lock body scroll when mobile menu is open
  useEffect(() => {
    if (open) {
      document.body.style.overflow = "hidden";
    } else {
      document.body.style.overflow = "";
    }
    return () => { document.body.style.overflow = ""; };
  }, [open]);

  const links: Array<{ label: string; to?: string; href?: string }> = [
    { label: "How it works", href: "#pipeline" },
    { label: "Programs", href: "#programs" },
    { label: "Results", href: "#results" },
    { label: "FAQ", href: "#faq" },
    { label: "Contact", href: "#contact" },
  ];

  const close = useCallback(() => setOpen(false), []);

  return (
    <nav className="fixed top-0 left-0 right-0 z-50">
      <div className="surface-glass border-b border-white/10">
        <div className="mx-auto max-w-7xl h-16 px-6 flex items-center justify-between">
          <Link to="/" className="flex items-center gap-2 group" onClick={close}>
            <span className="inline-flex w-7 h-7 items-center justify-center rounded-md bg-white text-black shadow-[0_0_24px_-6px_rgba(255,255,255,0.4)] group-hover:shadow-[0_0_32px_-4px_rgba(255,255,255,0.6)] transition-shadow">
              <Zap className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-white text-sm">InstaEdit</span>
          </Link>

          {/* Desktop nav */}
          <div className="hidden md:flex items-center gap-7 text-sm font-medium text-zinc-400">
            {links.map((l) =>
              l.to ? (
                <Link
                  key={l.label}
                  to={l.to}
                  className="hover:text-white transition-colors relative after:absolute after:bottom-[-2px] after:left-0 after:h-[2px] after:w-0 after:bg-gradient-to-r after:from-violet-400 after:to-cyan-400 after:transition-all after:duration-300 hover:after:w-full"
                >
                  {l.label}
                </Link>
              ) : (
                <a key={l.label} href={l.href} className="hover:text-white transition-colors relative after:absolute after:bottom-[-2px] after:left-0 after:h-[2px] after:w-0 after:bg-gradient-to-r after:from-violet-400 after:to-cyan-400 after:transition-all after:duration-300 hover:after:w-full">
                  {l.label}
                </a>
              ),
            )}
          </div>

          {/* Mobile hamburger */}
          <button type="button" onClick={() => setOpen(!open)} className="md:hidden p-2 text-zinc-400 hover:text-white transition-colors" aria-label={open ? "Close menu" : "Open menu"}>
            {open ? <X className="w-5 h-5" /> : <Menu className="w-5 h-5" />}
          </button>
        </div>          {/* Mobile drawer — accessible dialog */}
        {open && (
          <div className="md:hidden border-t border-white/10 bg-[#14141c]/98 backdrop-blur-xl" role="dialog" aria-modal="true" aria-label="Navigation menu">
            <div className="px-6 py-4 space-y-1">
              {links.map((l) =>
                l.to ? (
                  <Link key={l.label} to={l.to} onClick={close} className="block py-3 text-sm font-medium text-zinc-300 hover:text-white hover:bg-white/[0.04] rounded-lg px-3 -mx-3 transition-colors">
                    {l.label}
                  </Link>
                ) : (
                  <a key={l.label} href={l.href} onClick={close} className="block py-3 text-sm font-medium text-zinc-300 hover:text-white hover:bg-white/[0.04] rounded-lg px-3 -mx-3 transition-colors">
                    {l.label}
                  </a>
                ),
              )}
              <hr className="border-white/10 my-3" />
              <a href="https://discord.com/users/1201477873719050332" target="_blank" rel="noopener noreferrer" onClick={close} className="block py-3 text-sm font-semibold text-center text-white bg-gradient-to-r from-violet-500 to-cyan-500 rounded-xl hover:opacity-90 transition-opacity">
                Write us on Discord
              </a>
            </div>
          </div>
        )}
      </div>
    </nav>
  );
}

/* ----------------------------------------------------------------------------
 * Hero
 * -------------------------------------------------------------------------- */

function Hero() {
  return (
    <section className="relative pt-32 pb-20 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora pointer-events-none" />
      <div aria-hidden="true" className="absolute inset-0 grid-bg pointer-events-none opacity-60" />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[460px] h-[460px] -top-32 -left-24 animate-drift-slow opacity-70" />
        <div className="glow-orb bg-cyan-400 w-[420px] h-[420px] -bottom-40 -right-24 animate-drift-rev opacity-60" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-10 items-center">
        <div className="lg:col-span-7 xl:col-span-6 animate-fade-up">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-7">
            <span className="relative flex h-2 w-2">
              <span className="animate-pulse-glow absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-400" />
            </span>
            <span>7 platforms · one workflow · zero re-uploads</span>
          </div>
          <h1 className="text-display-1 text-white">
            Your creativity.{" "}
            <span className="text-gradient-animated">Our distribution.</span>
          </h1>
          <p className="text-body-lg text-zinc-300/90 mt-7 max-w-[60ch]">
            You bring the great idea and the quality content. InstaEdit makes sure the
            world sees it everywhere — without you wasting hours uploading files to
            every platform. Start your YouTube channel, reach millions on TikTok, or
            grow your brand across all platforms at once.
          </p>
          <div className="flex flex-col sm:flex-row items-start sm:items-center gap-4 mt-8">
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
            >
              Write us
              <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
            </a>
          </div>
          <div className="mt-10 flex items-center gap-4 flex-wrap">
            <span className="text-eyebrow text-zinc-500">Works with</span>
            <div className="flex items-center gap-2">
              {PLATFORM_REGISTRY.map(({ key, name, Logo }) => (
                <span key={key} className="inline-flex w-7 h-7 rounded-md overflow-hidden ring-1 ring-white/15 surface-glass" title={name} aria-label={name}>
                  <Logo className="w-full h-full" />
                </span>
              ))}
            </div>
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
 * AI Pipeline Visualization — "From idea to publication"
 * -------------------------------------------------------------------------- */

function PipelineSection() {
  const steps = [
    { icon: Lightbulb, label: "Idea", desc: "Tell us what you want to create — a YouTube video, a TikTok trend, anything", color: "from-violet-500 to-purple-500" },
    { icon: Film, label: "Create", desc: "Record your video or let our AI generate it for you with ChronoN", color: "from-blue-500 to-cyan-500" },
    { icon: Cpu, label: "AI Polishes", desc: "Subtitles, thumbnails, captions — everything made for you automatically", color: "from-emerald-500 to-teal-500" },
    { icon: CalendarClock, label: "Schedule", desc: "Pick when you want to post — we handle the timing for each platform", color: "from-amber-500 to-orange-500" },
    { icon: Globe, label: "Publish", desc: "One click and your video goes live on YouTube, TikTok, Instagram and more", color: "from-pink-500 to-rose-500" },
    { icon: BarChart3, label: "Analyze", desc: "See how your content performs across all platforms in one place", color: "from-indigo-500 to-violet-500" },
  ];

  return (
    <section id="pipeline" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">How it works</div>
          <h2 className="text-display-2 text-white">
            Less production.{" "}
            <span className="text-gradient-animated">More publishing.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            From one idea to videos published everywhere — in minutes, not hours.
            No editing skills needed. No tabs to juggle. Just create and let InstaEdit
            handle the rest.
          </p>
        </div>

        {/* Flow visualization — horizontal on lg, vertical on mobile */}
        <div className="relative">
          {/* Connecting line */}
          <div aria-hidden="true" className="hidden lg:block absolute top-[72px] left-0 right-0 h-0.5 bg-gradient-to-r from-violet-500/40 via-cyan-400/40 to-pink-500/40 pointer-events-none" />

          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-6 gap-4 lg:gap-3">
            {steps.map((s, i) => (
              <div key={s.label} className={`relative animate-fade-up ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400", "animation-delay-500"][i]}`}>
                {/* Step number badge */}
                <div className="flex lg:flex-col items-center lg:items-center gap-4 lg:gap-3 p-4 lg:p-5 surface-card hover:border-violet-400/30 hover:shadow-[0_8px_32px_rgba(139,92,246,0.15)] transition-all duration-300 group">
                  <div className={`relative w-12 h-12 lg:w-14 lg:h-14 rounded-xl bg-gradient-to-br ${s.color} flex items-center justify-center shrink-0 group-hover:scale-110 transition-transform duration-300 shadow-lg`}>
                    <s.icon className="w-5 h-5 lg:w-6 h-6 text-white" />
                    {/* Pulse ring */}
                    <div className="absolute inset-0 rounded-xl ring-2 ring-white/20 animate-pulse-glow opacity-0 group-hover:opacity-100 transition-opacity" />
                  </div>
                  <div className="lg:text-center">
                    <div className="flex items-center gap-2 lg:justify-center">
                      <span className="text-[10px] font-bold text-zinc-500 tabular-nums">0{i + 1}</span>
                      <h3 className="text-sm font-bold text-white">{s.label}</h3>
                    </div>
                    <p className="text-[11px] text-zinc-400 mt-1 leading-relaxed lg:text-center">{s.desc}</p>
                  </div>
                </div>
                {/* Arrow between steps (mobile) */}
                {i < steps.length - 1 && (
                  <div aria-hidden="true" className="lg:hidden flex justify-center py-1">
                    <ArrowRight className="w-4 h-4 text-zinc-600 rotate-90" />
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>

        {/* AI automation highlights */}
        <div className="mt-16 grid grid-cols-1 sm:grid-cols-3 gap-4 animate-fade-up animation-delay-600">
          {[
            { icon: Languages, title: "50+ languages", desc: "Reach audiences worldwide with automatic subtitle translation" },
            { icon: Palette, title: "AI Thumbnails", desc: "Eye-catching thumbnails generated for every platform" },
            { icon: RefreshCw, title: "Auto repurposing", desc: "Turn one long video into Shorts, Reels, and TikToks instantly" },
          ].map((h) => (
            <div key={h.title} className="surface-card-soft p-4 flex items-center gap-3 hover:border-white/20 transition-colors">
              <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-violet-500/20 to-cyan-500/20 flex items-center justify-center text-violet-300 shrink-0">
                <h.icon className="w-5 h-5" />
              </div>
              <div>
                <div className="text-sm font-semibold text-white">{h.title}</div>
                <div className="text-[11px] text-zinc-500">{h.desc}</div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Stats strip
 * -------------------------------------------------------------------------- */

function StatsStrip() {
  const stats: Array<{ v: string; l: string }> = [
    { v: "10,000+", l: "Posts/month" },
    { v: "7", l: "Platforms" },
    { v: "50+", l: "Creator teams" },
    { v: "99.9%", l: "Publishing uptime" },
  ];
  return (
    <section className="relative py-10 border-y border-white/10 bg-[#0c0c14]/60">
      <div className="mx-auto max-w-7xl px-6">
        <ul className="grid grid-cols-2 sm:grid-cols-4 gap-y-6 gap-x-8 text-center sm:text-left">          {stats.map((s, idx) => (
            <li
              key={s.l}
              className={`flex items-center ${              idx < stats.length - 1 ? "sm:border-r sm:border-white/10 sm:pr-8" : ""} justify-center sm:justify-start gap-4`}>
              <span className="text-3xl sm:text-4xl font-extrabold text-white tabular-nums tracking-tight">{s.v}</span>
              <span className="text-eyebrow text-zinc-500 max-w-[12ch]">{s.l}</span>
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Workflow — 6 steps expanded
 * -------------------------------------------------------------------------- */

function WorkflowSection() {
  const steps = [
    {
      n: "01", title: "Tell us your idea",
      copy: "Write a paragraph, paste a script, or just describe what you want. InstaEdit figures out the best format, platforms, and timing for your content.",
      Icon: Lightbulb,
      accent: "from-violet-500/30 to-violet-500/0", ring: "ring-violet-400/40", iconColor: "text-violet-300",
    },
    {
      n: "02", title: "Record or generate",
      copy: "Upload your own video, or let ChronoN create it for you. No camera? No problem — AI generates professional videos from your brief.",
      Icon: Film,
      accent: "from-blue-500/30 to-blue-500/0", ring: "ring-blue-400/40", iconColor: "text-blue-300",
    },
    {
      n: "03", title: "AI polishes everything",
      copy: "Subtitles, thumbnails, captions, hashtags — all generated automatically. Each one tailored to the platform it's going on.",
      Icon: Cpu,
      accent: "from-emerald-500/30 to-emerald-500/0", ring: "ring-emerald-400/40", iconColor: "text-emerald-300",
    },
    {
      n: "04", title: "Schedule or publish now",
      copy: "Pick a time or go live instantly. InstaEdit publishes at the best time for each platform and timezone.",
      Icon: CalendarClock,
      accent: "from-cyan-500/30 to-cyan-500/0", ring: "ring-cyan-400/40", iconColor: "text-cyan-300",
    },
    {
      n: "05", title: "Go live everywhere",
      copy: "One click. Your video is on YouTube, TikTok, Instagram, Facebook, LinkedIn, X, and Threads — with the right format for each.",
      Icon: Globe,
      accent: "from-pink-500/30 to-pink-500/0", ring: "ring-pink-400/40", iconColor: "text-pink-300",
    },
    {
      n: "06", title: "See what works",
      copy: "Track views, engagement, and growth across all platforms in one simple dashboard. Know what to do more of.",
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
            From idea to everywhere in{" "}
            <span className="text-gradient-animated">6 simple steps.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Not just publishing. The full journey: from your first idea to a video
            that reaches millions — all from one place. No editing skills required.
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

/* ----------------------------------------------------------------------------
 * Features
 * -------------------------------------------------------------------------- */

function Features() {
  return (
    <section id="features" className="relative py-24 sm:py-32 bg-elevated overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-25 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">What's included</div>
          <h2 className="text-display-2 text-white">Everything you need to publish content everywhere.</h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">One place for all your platforms. Simple, clean, and powerful.</p>
        </div>
        <div className="grid lg:grid-cols-3 gap-5">
          <div className="surface-card p-7 lg:p-8 relative overflow-hidden lg:col-span-2 lg:row-span-2 animate-fade-up hover:border-violet-400/30 transition-all duration-300">
            <div aria-hidden="true" className="absolute -top-32 -right-32 w-80 h-80 rounded-full bg-violet-500 blur-3xl opacity-50" />
            <div aria-hidden="true" className="absolute bottom-0 left-0 right-0 h-px bg-gradient-to-r from-transparent via-violet-400/50 to-transparent" />
            <div className="relative">
              <div className="inline-flex w-12 h-12 rounded-xl items-center justify-center ring-1 ring-violet-400/40 surface-glass text-violet-300 mb-5">
                <Sparkles className="w-6 h-6" />
              </div>
              <h3 className="text-display-3 text-white mb-3 max-w-[22ch]">One dashboard, every platform.</h3>
              <p className="text-sm text-zinc-400 leading-relaxed max-w-[52ch]">
                Manage Instagram, TikTok, YouTube, X, LinkedIn, Facebook and Threads from a single calendar.
                See what's published, what's scheduled, and what needs your attention.
              </p>
              <div className="mt-7 surface-glass rounded-xl border border-white/10 overflow-hidden">
                <div className="flex items-center gap-1.5 px-4 py-2.5 border-b border-white/5">
                  <span className="w-2.5 h-2.5 rounded-full bg-[#ff5f57]" />
                  <span className="w-2.5 h-2.5 rounded-full bg-[#febc2e]" />
                  <span className="w-2.5 h-2.5 rounded-full bg-[#28c840]" />
                  <span className="ml-3 text-[11px] text-zinc-500">Calendar · this week</span>
                </div>
                <div className="grid grid-cols-7 gap-1.5 p-3 text-center text-[10px] text-zinc-500">
                  {["M", "T", "W", "T", "F", "S", "S"].map((d, idx) => (
                    <div key={`${d}${idx}`} className="rounded-md border border-white/5 bg-black/20 py-2.5">
                      <div className="text-eyebrow text-zinc-600 mb-1.5">{d}</div>
                      <div className="space-y-1">
                        {[1, 2].slice(0, idx % 2 === 0 ? 2 : 1).map((i) => (
                          <div key={i} className={`h-1.5 rounded-full mx-1 ${i === 1 ? "bg-violet-400/70" : "bg-cyan-400/70"}`} />
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          </div>
          <div className="surface-card p-6 relative overflow-hidden animate-fade-up animation-delay-100 hover:border-cyan-400/30 transition-all duration-300">
            <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-cyan-400/40 surface-glass text-cyan-300 mb-4">
              <IconSchedule className="w-5 h-5" />
            </div>
            <h3 className="text-display-3 text-white mb-2">Smart scheduling.</h3>
            <p className="text-sm text-zinc-400 leading-relaxed">
              Posts go live when your audience is most active. One click, zero guesswork.
            </p>
          </div>
          <div className="surface-card p-6 relative overflow-hidden animate-fade-up animation-delay-200 hover:border-pink-400/30 transition-all duration-300">
            <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-pink-400/40 surface-glass text-pink-300 mb-4">
              <Shield className="w-5 h-5" />
            </div>
            <h3 className="text-display-3 text-white mb-2">Built-in approval flows.</h3>
            <p className="text-sm text-zinc-400 leading-relaxed">
              Review drafts, lock final versions, and publish with confidence. Full control over what goes live.
            </p>
          </div>
          <div className="surface-card p-6 relative overflow-hidden lg:col-span-3 animate-fade-up animation-delay-300 hover:border-amber-400/30 transition-all duration-300">
            <div aria-hidden="true" className="absolute -bottom-24 -right-24 w-72 h-72 rounded-full bg-amber-500/30 blur-3xl pointer-events-none" />
            <div className="relative grid lg:grid-cols-2 gap-6 items-center">
              <div>
                <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-amber-400/40 surface-glass text-amber-300 mb-4">
                  <IconAnalyze className="w-5 h-5" />
                </div>
                <h3 className="text-display-3 text-white mb-2">Analytics that make sense.</h3>
                <p className="text-sm text-zinc-400 leading-relaxed max-w-[52ch]">
                  See views, engagement, and growth across all your channels in one simple view. Know what's working and what to do next.
                </p>
              </div>
              <div className="surface-glass rounded-xl border border-white/10 p-5">
                <div className="flex items-end justify-between gap-2 h-24">
                  {[42, 64, 38, 78, 56, 92, 70, 88, 60, 96, 74, 84].map((h, i) => (
                    <div key={i} className="flex-1 rounded-t-sm bg-gradient-to-t from-violet-500/60 to-cyan-400/90" style={{ height: `${h}%` }} />
                  ))}
                </div>
                <div className="flex justify-between text-[10px] text-zinc-500 mt-2">
                  <span>Jan</span><span>Mar</span><span>May</span><span>Jul</span><span>Sep</span><span>Nov</span>
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
 * Results — Numbers + Testimonials
 * -------------------------------------------------------------------------- */

function ResultsSection() {
  const stats = [
    { v: "500+", l: "Videos created", desc: "for students and creators" },
    { v: "50+", l: "Channels grown", desc: "from zero to monetization" },
    { v: "20+", l: "Languages", desc: "automatic translation" },
    { v: "7", l: "Platforms", desc: "publish everywhere at once" },
  ];

  const testimonials = [
    {
      quote: "I started with zero subscribers and no editing skills. In 3 months I hit 1,000 subscribers and my first monetization check.",
      author: "Alex M.",
      role: "YouTube Creator",
      result: "0 → 1K subscribers in 3 months",
    },
    {
      quote: "The aged channel they gave me was a game-changer. My videos started getting recommended by the algorithm within the first week.",
      author: "Sara K.",
      role: "Education Channel",
      result: "First video: 12K views",
    },
    {
      quote: "I was spending 10 hours a week on editing and uploading. Now I record one video and it goes everywhere automatically.",
      author: "Marco L.",
      role: "Tech Creator",
      result: "10h → 2h per week",
    },
  ];

  return (
    <section id="results" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-15 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Results</div>
          <h2 className="text-display-2 text-white">
            Real creators.{" "}
            <span className="text-gradient-animated">Real results.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            Our students don't just learn — they grow. Here's what happens
            when strategy meets automation.
          </p>
        </div>

        {/* Stats */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-5 mb-16">
          {stats.map((s, i) => (
            <div
              key={s.l}
              className={`surface-card p-6 text-center animate-fade-up hover:border-violet-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <div className="text-3xl sm:text-4xl font-extrabold text-white tabular-nums tracking-tight">{s.v}</div>
              <div className="text-sm font-medium text-zinc-300 mt-2">{s.l}</div>
              <div className="text-xs text-zinc-500 mt-1">{s.desc}</div>
            </div>
          ))}
        </div>

        {/* Testimonials */}
        <div className="grid md:grid-cols-3 gap-5">
          {testimonials.map((t, i) => (
            <div
              key={t.author}
              className={`surface-card p-6 relative overflow-hidden animate-fade-up hover:border-violet-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              <div className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-emerald-500/10 ring-1 ring-emerald-400/25 text-[11px] font-medium text-emerald-300 mb-4">
                <CheckCircle2 className="w-3 h-3" />
                {t.result}
              </div>
              <p className="text-sm text-zinc-300 leading-relaxed mb-5">"{t.quote}"</p>
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-full bg-gradient-to-br from-violet-500 to-cyan-500 flex items-center justify-center text-white text-sm font-semibold">
                  {t.author.charAt(0)}
                </div>
                <div>
                  <div className="text-sm font-semibold text-white">{t.author}</div>
                  <div className="text-xs text-zinc-500">{t.role}</div>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Agency section
 * -------------------------------------------------------------------------- */

function AgencySection() {
  const benefits = [
    { icon: Building2, title: "Multi-workspace", desc: "Manage dozens of clients from a single platform. Each workspace has its own accounts, calendar and team members." },
    { icon: Users, title: "Granular permissions", desc: "Assign specific roles: admin, editor, reviewer, viewer. Each client sees only their own content." },
    { icon: Target, title: "Bulk operations", desc: "Publish the same content to different client accounts. Schedule batches of 200+ posts in one click." },
    { icon: Headphones, title: "Priority support", desc: "Dedicated agency support, guaranteed SLAs, assisted onboarding and team training." },
  ];

  return (
    <section id="agency" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-emerald-500 w-[400px] h-[400px] -top-32 -right-32 animate-drift-rev opacity-30" />
        <div className="glow-orb bg-violet-500 w-[360px] h-[360px] -bottom-32 -left-24 animate-drift-slow opacity-25" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-6">
            <Building2 className="w-3.5 h-3.5 text-emerald-400" />
            <span>For digital agencies</span>
          </div>
          <h2 className="text-display-2 text-white">
            Built for agencies that{" "}
            <span className="text-gradient-animated">publish for dozens of clients.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Manage your clients' entire publishing workflow from a single platform.
            Separate workspaces, granular permissions, bulk operations and unified reports.
          </p>
        </div>
        <div className="grid sm:grid-cols-2 gap-5">
          {benefits.map((b, i) => (
            <div key={b.title} className={`surface-card p-6 relative overflow-hidden animate-fade-up hover:border-emerald-400/30 hover:shadow-[0_8px_32px_rgba(16,185,129,0.12)] transition-all duration-300 group ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}>
              <div aria-hidden="true" className="absolute -top-16 -right-16 w-40 h-40 rounded-full bg-emerald-500/10 blur-3xl pointer-events-none group-hover:bg-emerald-500/20 transition-all duration-500" />
              <div className="relative">
                <div className="w-11 h-11 rounded-xl bg-gradient-to-br from-emerald-500/20 to-teal-500/20 flex items-center justify-center text-emerald-300 mb-4 ring-1 ring-emerald-400/20 group-hover:scale-110 transition-transform duration-300">
                  <b.icon className="w-5 h-5" />
                </div>
                <h3 className="text-display-3 text-white mb-2">{b.title}</h3>
                <p className="text-sm text-zinc-400 leading-relaxed">{b.desc}</p>
              </div>
            </div>
          ))}
        </div>
        <div className="mt-12 surface-glass border border-white/15 rounded-2xl p-8 relative overflow-hidden text-center animate-fade-up animation-delay-400">
          <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-30 pointer-events-none" />
          <div className="relative">
            <h3 className="text-display-3 text-white mb-3">Ready to scale your agency?</h3>
            <p className="text-sm text-zinc-400 mb-6 max-w-[48ch] mx-auto">
              Unite all your clients on one platform. Reduce publishing time by 80%
              and offer a service your competitors can't match.
            </p>
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
            >
              Start now
              <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
            </a>
          </div>
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Problem & Solution
 * -------------------------------------------------------------------------- */

function ProblemSolution() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-red-500 w-[360px] h-[360px] -top-32 -right-32 animate-drift-slow opacity-20" />
        <div className="glow-orb bg-emerald-500 w-[400px] h-[400px] -bottom-40 -left-24 animate-drift-rev opacity-25" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="grid lg:grid-cols-2 gap-16 items-center">
          <div className="animate-fade-up">
            <div className="text-eyebrow text-red-300/90 mb-3">The problem</div>
            <h2 className="text-display-2 text-white mb-6">
              Communities leave you with{" "}
              <span className="text-gradient-animated">advice you can't apply.</span>
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[52ch] mb-6">
              You watch 100 YouTube tutorials but still don't know what to post, when to
              post, or how to make it look professional. And automation without strategy
              just spams mediocrity across 7 channels at once.
            </p>
            <div className="space-y-4">
              {[
                "You have ideas but no system to turn them into content",
                "You spend hours re-uploading the same video to every platform",
                "You don't know what format works on YouTube vs TikTok vs Instagram",
                "You see others growing but can't figure out what you're doing wrong",
              ].map((item) => (
                <div key={item} className="flex items-start gap-3">
                  <span className="w-5 h-5 rounded-full bg-red-500/20 flex items-center justify-center shrink-0 mt-0.5">
                    <span className="w-2 h-2 rounded-full bg-red-400" />
                  </span>
                  <span className="text-sm text-zinc-300">{item}</span>
                </div>
              ))}
            </div>
          </div>
          <div className="animate-fade-up animation-delay-200">
            <div className="text-eyebrow text-emerald-300/90 mb-3">Our solution</div>
            <h2 className="text-display-2 text-white mb-6">
              Strategy + Automation{" "}
              <span className="text-gradient-animated">= Market domination.</span>
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[52ch] mb-6">
              We combine Content Strategy (so you create videos people actually want to
              watch) with Bulletproof Automation (so they reach millions across every
              platform at zero time cost).
            </p>
            <div className="space-y-4">
              {[
                "AI-powered ideation that suggests what to create based on your niche",
                "ChronoN generates professional videos from a simple text brief",
                "One upload → 7 native posts, each optimized for its platform",
                "Smart scheduling posts at the exact time your audience is online",
              ].map((item) => (
                <div key={item} className="flex items-start gap-3">
                  <span className="w-5 h-5 rounded-full bg-emerald-500/20 flex items-center justify-center shrink-0 mt-0.5">
                    <CheckCircle2 className="w-3 h-3 text-emerald-400" />
                  </span>
                  <span className="text-sm text-zinc-300">{item}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Find Your Path — 3 Tiers
 * -------------------------------------------------------------------------- */

function FindYourPath() {
  const tiers = [
    {
      level: "Level 1",
      tagline: "I want to learn and grow",
      title: "Mentorship Program",
      color: "emerald",
      ringColor: "ring-emerald-400/40",
      iconColor: "text-emerald-300",
      bgGradient: "from-emerald-500/20 to-teal-500/20",
      hoverBorder: "hover:border-emerald-400/30",
      shadowColor: "rgba(16,185,129,0.12)",
      desc: "For creators and small teams who want to master YouTube/Shorts strategy, monetize, and build a scalable workflow.",
      features: [
        "1-on-1 mentoring (channel audit, content strategy, weekly review)",
        "Full access to InstaEdit Pro included",
        "Bonus: Aged YouTube channel to accelerate monetization",
        "Learn what content converts and why",
      ],
      cta: "Start mentoring",
      ctaLink: "/mentoring",
    },
    {
      level: "Level 2",
      tagline: "Do everything for me",
      title: "Content Automation",
      color: "blue",
      ringColor: "ring-blue-400/40",
      iconColor: "text-blue-300",
      bgGradient: "from-blue-500/20 to-cyan-500/20",
      hoverBorder: "hover:border-blue-400/30",
      shadowColor: "rgba(59,130,246,0.12)",
      desc: "For brands, entrepreneurs, or creators who have budget but zero time.",
      features: [
        "Turnkey content creation + strategy",
        "Full channel management and automation on InstaEdit",
        "Zero-effort publishing across all platforms",
        "Dedicated content team working for you",
      ],
      cta: "See programs",
      ctaLink: "/programs",
      featured: true,
    },
    {
      level: "Level 3",
      tagline: "Enterprise & scaling",
      title: "Multi-Channel Global",
      color: "violet",
      ringColor: "ring-violet-400/40",
      iconColor: "text-violet-300",
      bgGradient: "from-violet-500/20 to-purple-500/20",
      hoverBorder: "hover:border-violet-400/30",
      shadowColor: "rgba(139,92,246,0.12)",
      desc: "For large clients, agencies, and structured brands.",
      features: [
        "Multi-channel automation at global scale",
        "Translation, dubbing, and subtitles in 20+ languages",
        "Dedicated infrastructure and servers",
        "Priority support with guaranteed SLAs",
      ],
      cta: "Contact us",
      ctaLink: "https://discord.com/users/1201477873719050332",
    },
  ];

  return (
    <section id="programs" className="relative py-24 sm:py-32 bg-elevated overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-15 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Find your path</div>
          <h2 className="text-display-2 text-white">
            Which level are you{" "}
            <span className="text-gradient-animated">ready for?</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            Whether you want to learn the game, have someone play it for you, or scale
            to millions — there's a path built for where you are right now.
          </p>
        </div>
        <div className="grid md:grid-cols-3 gap-6">
          {tiers.map((t, i) => (
            <div
              key={t.level}
              className={`surface-card p-7 relative overflow-hidden animate-fade-up ${t.hoverBorder} transition-all duration-300 group ${
                t.featured ? "ring-1 ring-blue-400/30 shadow-[0_8px_40px_rgba(59,130,246,0.15)]" : ""
              } ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              {t.featured && (
                <div className="absolute top-0 left-0 right-0 h-0.5 bg-gradient-to-r from-blue-500 to-cyan-400" />
              )}
              <div aria-hidden="true" className={`absolute -top-20 -right-20 w-48 h-48 rounded-full bg-radial ${t.bgGradient} opacity-60 blur-3xl pointer-events-none group-hover:opacity-100 transition-all duration-500`} />
              <div className="relative">
                <div className="flex items-center gap-3 mb-4">
                  <div className={`inline-flex w-10 h-10 rounded-xl items-center justify-center ring-1 ${t.ringColor} surface-glass ${t.iconColor}`}>
                    {i === 0 ? <Target className="w-5 h-5" /> : i === 1 ? <Zap className="w-5 h-5" /> : <Globe className="w-5 h-5" />}
                  </div>
                  <div>
                    <div className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">{t.level}</div>
                    <div className="text-xs text-zinc-400">{t.tagline}</div>
                  </div>
                </div>
                <h3 className="text-display-3 text-white mb-3">{t.title}</h3>
                <p className="text-sm text-zinc-400 leading-relaxed mb-5">{t.desc}</p>
                <ul className="space-y-3 mb-6">
                  {t.features.map((f) => (
                    <li key={f} className="flex items-start gap-2.5">
                      <CheckCircle2 className={`w-4 h-4 shrink-0 mt-0.5 ${t.iconColor}`} />
                      <span className="text-sm text-zinc-300">{f}</span>
                    </li>
                  ))}
                </ul>
                <Link
                  to={t.ctaLink}
                  className={`inline-flex items-center gap-2 px-6 py-3 rounded-xl font-semibold text-sm transition-all group/btn ${
                    t.featured
                      ? "bg-white text-black hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)]"
                      : "surface-glass border border-white/15 text-zinc-200 hover:border-white/30 hover:text-white"
                  }`}
                >
                  {t.cta}
                  <ArrowRight className="w-4 h-4 group-hover/btn:translate-x-1 transition-transform" />
                </Link>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Shorts section
 * -------------------------------------------------------------------------- */

function ShortsSection() {
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

/* ----------------------------------------------------------------------------
 * Long-form section
 * -------------------------------------------------------------------------- */

function LongFormSection() {
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

/* ----------------------------------------------------------------------------
 * Footer
 * -------------------------------------------------------------------------- */

const SEO = {
  title: "InstaEdit — Create great content. We handle the distribution.",
  description:
    "You bring the idea, we make sure the world sees it. AI-powered video creation with ChronoN, multi-platform publishing to YouTube, TikTok, Instagram and more. Built for creators, teams, and agencies.",
  canonical: "https://app.instaedit.org/",
} as const;

function Footer() {
  const cols: Array<{ heading: string; links: Array<{ l: string; to?: string; href?: string }> }> = [
    {
      heading: "Product",
      links: [
        { l: "Pipeline AI", href: "#pipeline" },
        { l: "Workflow", href: "#workflow" },
        { l: "Features", href: "#features" },
        { l: "For agencies", href: "#agency" },
        { l: "Programs", to: "/programs" },
        { l: "Mentoring", to: "/mentoring" },
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
            <span className="font-bold tracking-tight text-white text-base">InstaEdit</span>
          </Link>
          <p className="text-sm text-zinc-400 mt-4 max-w-[42ch] leading-relaxed">
            Your creativity. Our distribution. One place to create, schedule, and
            publish professional content across every platform.
          </p>
          <div className="flex items-center gap-2 mt-5">
            {PLATFORM_REGISTRY.map(({ key, name, Logo }) => (
              <span key={key} className="inline-flex w-7 h-7 rounded-md overflow-hidden ring-1 ring-white/10 surface-glass" title={name} aria-label={name}>
                <Logo className="w-full h-full" />
              </span>
            ))}
          </div>
        </div>
        <div className="lg:col-span-7 grid grid-cols-1 sm:grid-cols-2 gap-8">
          {cols.map((col) => (
            <div key={col.heading}>
              <div className="text-eyebrow text-zinc-500 mb-4">{col.heading}</div>
              <ul className="space-y-3">
                {col.links.map((link) => {
                  const className = "text-sm text-zinc-300 hover:text-white transition-colors";
                  if (link.to) {
                    return (<li key={link.l}><Link to={link.to} className={className}>{link.l}</Link></li>);
                  }
                  return (<li key={link.l}><a href={link.href} className={className}>{link.l}</a></li>);
                })}
              </ul>
            </div>
          ))}
        </div>
      </div>
      <div className="border-t border-white/5">
        <div className="mx-auto max-w-7xl px-6 py-6 flex flex-col sm:flex-row items-center justify-between gap-3 text-xs text-zinc-500">
          <div>© {new Date().getFullYear()} InstaEdit, Inc.</div>
          <div>Built for creators who want to grow.</div>
        </div>
      </div>
    </footer>
  );
}

/* ----------------------------------------------------------------------------
 * Who Are We
 * -------------------------------------------------------------------------- */

function WhoAreWe() {
  return (
    <section id="who-are-we" className="relative overflow-hidden">
      <div className="relative h-[70vh] min-h-[500px] flex items-center justify-center overflow-hidden">
        <img src="/founder.jpg" alt="InstaEdit team working on video automation" className="absolute inset-0 w-full h-full object-cover" />
        <div className="absolute inset-0 bg-black/65" />
        <div className="absolute inset-0 bg-gradient-to-t from-[#030308] via-transparent to-transparent" />
        <div className="relative z-10 text-center px-6 max-w-4xl mx-auto animate-fade-up">
          <h2 className="text-display-1 text-white mb-6">
            We make video accessible{" "}
            <span className="text-gradient-animated">for everyone.</span>
          </h2>
          <p className="text-body-lg text-zinc-300/90 max-w-[55ch] mx-auto">
            Our mission is to remove every barrier between a creator and their audience —
            so anyone, anywhere, can publish professional content without a studio, team or budget.
          </p>
        </div>
      </div>
      <div className="relative py-24 sm:py-32 bg-elevated overflow-hidden">
        <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
        <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-2 gap-16 items-start">
          <div className="animate-fade-up">
            <div className="text-eyebrow text-violet-300/90 mb-3">Our mission</div>
            <h2 className="text-display-2 text-white mb-6">
              Automate video creation{" "}
              <span className="text-gradient-animated">for everyone.</span>
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[55ch] mb-6">
              We exist to help anyone work for themselves. Creating content shouldn't
              require a whole production team — and publishing on every platform shouldn't
              take all day. With ChronoN, our proprietary AI engine, even students with
              no budget can create professional videos automatically.
            </p>
            <p className="text-body-lg text-zinc-400 max-w-[55ch] mb-8">
              InstaEdit automates the entire pipeline: upload once, and we handle encoding,
              subtitles, thumbnails, scheduling and publishing on YouTube, TikTok, Instagram and more —
              so you can focus on what you love: creating.
            </p>
            <div className="grid grid-cols-3 gap-4">
              {[{ v: "7", l: "Platforms" }, { v: "50+", l: "Languages" }, { v: "24/7", l: "Uptime" }].map((s) => (
                <div key={s.l} className="surface-card p-4 text-center">
                  <div className="text-xl font-bold text-white tabular-nums">{s.v}</div>
                  <div className="text-eyebrow text-zinc-500 mt-1">{s.l}</div>
                </div>
              ))}
            </div>
          </div>
          <div className="relative animate-fade-up animation-delay-200">
            <div className="surface-glass border border-white/15 rounded-2xl p-8 relative overflow-hidden shadow-[0_30px_100px_-40px_rgba(124,58,237,0.4)]">
              <div aria-hidden="true" className="absolute -top-20 -right-20 w-60 h-60 rounded-full bg-violet-500/25 blur-3xl pointer-events-none" />
              <div className="relative">
                <div className="text-eyebrow text-zinc-200 mb-4">A message from the founder</div>
                <p className="text-sm text-zinc-300 leading-relaxed mb-4">
                  Growing up as the child of immigrants, I always looked for ways to build something on my own.
                  When I started creating videos, I fell in love with the freedom of being my own boss
                  — managing my own hours, following my ideas.
                </p>
                <p className="text-sm text-zinc-300 leading-relaxed mb-4">
                  But I also immediately realized how hard it was to publish everywhere. The 14-tab workflow,
                  re-encoding, manual subtitles — it felt like a full-time job just to hit "publish."
                </p>
                <p className="text-sm text-zinc-300 leading-relaxed mb-4">
                  That's why I created InstaEdit. It's the all-in-one video publishing tool I wish I had
                  had from day one. We automate all the "business stuff" so you can spend
                  your time doing what you love — creating great content.
                </p>
                <p className="text-sm text-zinc-300 leading-relaxed mb-6">
                  We're on a mission to let anyone earn a living working for themselves,
                  and we're grateful you're here. Creating content is really hard. InstaEdit is here to
                  help you breathe a little easier.
                </p>
                <blockquote className="border-l-2 border-violet-400/50 pl-4">
                  <p className="text-sm text-zinc-200 italic leading-relaxed">Best regards,</p>
                  <p className="text-sm text-white font-semibold mt-2">The InstaEdit Team</p>
                </blockquote>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * FAQ
 * -------------------------------------------------------------------------- */

function FAQ() {
  const [open, setOpen] = useState<number | null>(null);

  const faqs = [
    {
      q: "Do I need any previous experience with YouTube or editing?",
      a: "Not at all. Whether you're starting completely from scratch or already have a channel, we adapt the mentorship to your current level. We guide you step-by-step through channel creation, content strategy, and using tools like InstaEdit to handle the heavy lifting.",
    },
    {
      q: "How long does it take to monetize my channel?",
      a: "Monetization timelines depend on your niche and consistency, but our mentorship is built to accelerate the process as fast as possible. Plus, we provide a free aged YouTube channel to skip the initial trust-building sandbox and hit monetization milestones much faster.",
    },
    {
      q: "How much time do I need to commit every week?",
      a: "Thanks to our automated workflows and content frameworks, you only need about 3 to 5 hours a week. We teach you how to focus solely on high-impact tasks (like recording or approving scripts) while automation and strategy handle the rest.",
    },
    {
      q: "What is the free aged YouTube channel included in the program?",
      a: "An aged channel is an established account created months or years ago. YouTube's algorithm trusts older channels more than brand-new ones, allowing your content to get indexed faster, avoid anti-spam filters, and gain initial traction much quicker.",
    },
    {
      q: "What is the difference between the Mentorship and the Content Automation System?",
      a: "In the Mentorship Program, we teach you how to run and grow YouTube channels correctly with 1-on-1 guidance and weekly feedback. In the Content Automation System, we handle everything — content creation, editing, and publishing are completely done-for-you hands-free.",
    },
    {
      q: "What happens if I want to scale to multiple channels or languages later?",
      a: "Once your primary channel is structured and profitable, you can seamlessly upgrade to our Enterprise Scaling Plan. This allows you to expand into multiple channels and translate/repurpose your content across 20+ languages automatically.",
    },
    {
      q: "What is ChronoN?",
      a: "ChronoN is our proprietary AI engine that can generate professional videos from a simple text brief. It handles scriptwriting, voiceover, visuals, and editing — perfect for students, creators without cameras, or anyone who wants to scale content production.",
    },
    {
      q: "How many platforms does the system publish to?",
      a: "The system publishes to 7 platforms: YouTube, TikTok, Instagram, Facebook, LinkedIn, X (Twitter), and Threads. Each post is automatically formatted for its platform.",
    },
  ];

  return (
    <section id="faq" className="relative py-24 sm:py-32 overflow-hidden">
      <div className="relative mx-auto max-w-3xl px-6">
        <div className="text-center mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">FAQ</div>
          <h2 className="text-display-2 text-white">
            Questions?{" "}
            <span className="text-gradient-animated">We've got answers.</span>
          </h2>
        </div>
        <div className="space-y-3">
          {faqs.map((faq, i) => (
            <div
              key={i}
              className={`surface-card overflow-hidden animate-fade-up transition-all duration-300 ${
                open === i ? "border-violet-400/30" : ""
              } ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400", "animation-delay-500", "animation-delay-600"][i] || ""}`}
            >
              <button
                type="button"
                onClick={() => setOpen(open === i ? null : i)}
                className="w-full flex items-center justify-between gap-4 p-5 text-left"
              >
                <span className="text-sm font-semibold text-white">{faq.q}</span>
                <span className={`w-8 h-8 rounded-lg surface-glass flex items-center justify-center shrink-0 transition-transform duration-300 ${open === i ? "rotate-45" : ""}`}>
                  <Plus className="w-4 h-4 text-zinc-400" />
                </span>
              </button>
              {open === i && (
                <div className="px-5 pb-5">
                  <p className="text-sm text-zinc-400 leading-relaxed">{faq.a}</p>
                </div>
              )}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Final CTA before footer
 * -------------------------------------------------------------------------- */

function FinalCTA() {
  return (
    <section id="contact" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-40 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6 text-center">
        <div className="max-w-3xl mx-auto animate-fade-up">
          <h2 className="text-display-1 text-white mb-6">
            Not sure which path is right for you?{" "}
            <span className="text-gradient-animated">Write us on Discord.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mb-8 max-w-[52ch] mx-auto">
            Send us a message and we'll figure out together whether
            you need mentoring, full automation, or an enterprise setup.
          </p>
          <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
            >
              Write us
              <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
            </a>
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
            >
              Write us on Discord
            </a>
          </div>
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Main landing export
 * -------------------------------------------------------------------------- */

export function Landing() {
  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] font-sans antialiased overflow-x-hidden selection:bg-violet-500/40 selection:text-white">
      <Seo {...SEO} />
      <Nav />
      <main className="relative">
        <Hero />
        <StatsStrip />
        <ProblemSolution />
        <FindYourPath />
        <PipelineSection />
        <WorkflowSection />
        <Features />
        <ResultsSection />
        <AgencySection />
        <ShortsSection />
        <LongFormSection />
        <WhoAreWe />
        <FAQ />
        <FinalCTA />
      </main>
      <Footer />
    </div>
  );
}
