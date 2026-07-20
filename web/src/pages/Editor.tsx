import { Link } from "react-router-dom";
import { Seo } from "../components/seo/Seo";
import {
  ArrowRight,
  Zap,
  UploadCloud,
  ChevronRight,
  Clock,
  CheckCircle2,
  Sparkles,
  PlayCircle,
  MonitorPlay,
  Phone,
  Send,
  Mail,
  Globe,
  Scissors,
  Radio,
} from "lucide-react";
import type { SVGProps } from "react";

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
 * The /editor route is a sibling marketing page (not the auth-protected
 * /app/compose composer). Same visual DNA as Landing — surface card / glass
 * / display text / hero-aurora — but the content reads as a "deep dive on
 * the editor itself": a dropzone as the visual hero, then platform-by-
 * platform outputs, then the speed/cost stats, then a CTA into /login.
 *
 * Platform SVG marks are duplicated here from Landing.tsx on purpose:
 * extracting them into a shared web/src/components/PlatformLogo.tsx would
 * touch Landing.tsx and risk regressing the existing landing. Pull them
 * into a shared module in a follow-up if a third call-site ever appears.
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
      <path
        d="M13.6 21v-7.2h2.4l.36-2.8H13.6V9.05c0-.81.23-1.35 1.4-1.35h1.5V5.15c-.26-.03-1.15-.11-2.18-.11-2.16 0-3.64 1.32-3.64 3.74v2.22H8.32v2.8h2.36V21h2.92z"
        fill="#fff"
      />
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
      <path
        d="M15.6 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45"
        stroke="#25F4EE"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
      <path
        d="M15.85 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45"
        stroke="#FE2C55"
        strokeWidth="1.7"
        strokeLinecap="round"
        transform="translate(0.5 -0.4)"
      />
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
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
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
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
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
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} aria-hidden="true" {...rest}>
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
 * Curated list of supported translation markets. Headline copy claims
 * 50+; this list shows the 30 most-requested locales with flag emoji +
 * ISO code + native name so the row reads as a real product surface,
 * not a vague claim. Append here to add a market.
 *
 * Code uses BCP-47 syntax for split locales (e.g. zh-Hant). Flag emojis
 * are used as the visual kink — they're rendered as glyphs, no Twemoji
 * dependency — so flag rendering falls back gracefully on platforms
 * that strip emoji (Linux server-side, headless email).
 * -------------------------------------------------------------------------- */
const LANGUAGES = [
  { code: "en", name: "English", flag: "🇬🇧" },
  { code: "es", name: "Español", flag: "🇪🇸" },
  { code: "pt", name: "Português", flag: "🇵🇹" },
  { code: "fr", name: "Français", flag: "🇫🇷" },
  { code: "de", name: "Deutsch", flag: "🇩🇪" },
  { code: "it", name: "Italiano", flag: "🇮🇹" },
  { code: "nl", name: "Nederlands", flag: "🇳🇱" },
  { code: "pl", name: "Polski", flag: "🇵🇱" },
  { code: "sv", name: "Svenska", flag: "🇸🇪" },
  { code: "da", name: "Dansk", flag: "🇩🇰" },
  { code: "no", name: "Norsk", flag: "🇳🇴" },
  { code: "fi", name: "Suomi", flag: "🇫🇮" },
  { code: "cs", name: "Čeština", flag: "🇨🇿" },
  { code: "el", name: "Ελληνικά", flag: "🇬🇷" },
  { code: "tr", name: "Türkçe", flag: "🇹🇷" },
  { code: "ru", name: "Русский", flag: "🇷🇺" },
  { code: "uk", name: "Українська", flag: "🇺🇦" },
  { code: "ar", name: "العربية", flag: "🇸🇦" },
  { code: "he", name: "עברית", flag: "🇮🇱" },
  { code: "hi", name: "हिन्दी", flag: "🇮🇳" },
  { code: "bn", name: "বাংলা", flag: "🇧🇩" },
  { code: "th", name: "ไทย", flag: "🇹🇭" },
  { code: "vi", name: "Tiếng Việt", flag: "🇻🇳" },
  { code: "id", name: "Bahasa Indonesia", flag: "🇮🇩" },
  { code: "ms", name: "Bahasa Melayu", flag: "🇲🇾" },
  { code: "tl", name: "Filipino", flag: "🇵🇭" },
  { code: "ja", name: "日本語", flag: "🇯🇵" },
  { code: "ko", name: "한국어", flag: "🇰🇷" },
  { code: "zh", name: "中文", flag: "🇨🇳" },
  { code: "zh-Hant", name: "繁體中文", flag: "🇹🇼" },
] as const;

/* ----------------------------------------------------------------------------
 * Top nav — same shape as Landing but with "InstaEdit" reading as a `Back to`
 * link instead of an in-page anchor. Editor and Landing both link to the
 * same Login CTA.
 * -------------------------------------------------------------------------- */
function Nav() {
  return (
    <nav className="fixed top-0 left-0 right-0 z-50">
      <div className="surface-glass border-b border-white/10">
        <div className="mx-auto max-w-7xl h-16 px-6 flex items-center justify-between">
          <Link to="/" className="flex items-center gap-2 group" aria-label="Back to InstaEdit">
            <span className="inline-flex w-7 h-7 items-center justify-center rounded-md bg-white text-black shadow-[0_0_24px_-6px_rgba(255,255,255,0.4)]">
              <Zap className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-white text-sm">
              InstaEdit
            </span>
            <span className="hidden sm:inline-flex items-center gap-1 ml-2 text-xs text-zinc-500 group-hover:text-zinc-300 transition-colors">
              <ChevronRight className="w-3 h-3" />
              Editor
            </span>
          </Link>
          <div className="flex items-center gap-2">
            <Link
              to="/login"
              className="hidden sm:inline-flex text-sm font-medium px-4 py-2 text-zinc-300 hover:text-white transition-colors"
            >
              Log in
            </Link>
            <Link
              to="/login"
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-full bg-white text-black text-sm font-semibold hover:bg-zinc-100 transition-colors shadow-[0_8px_30px_-10px_rgba(255,255,255,0.4)]"
            >
              Connect account
              <ArrowRight className="w-4 h-4" />
            </Link>
          </div>
        </div>
      </div>
    </nav>
  );
}

/* ----------------------------------------------------------------------------
 * Dropzone mockup — pure visual, no real upload. Same surface-card/glass
 * language as Landing's hero dashboard mockup. Disabled state via
 * `aria-disabled` and `cursor-default` so it reads as decorative-only, not
 * a fake submit. The animation is only `animate-pulse-glow` on the
 * progress bar so the panel breathes.
 * -------------------------------------------------------------------------- */
function DropzoneMockup() {
  return (
    <div
      aria-disabled="true"
      className="relative surface-glass rounded-3xl border border-white/15 overflow-hidden shadow-[0_30px_120px_-30px_rgba(124,58,237,0.55)] animate-fade-up animation-delay-200"
    >
      {/* Faint outer glow */}
      <div
        aria-hidden="true"
        className="absolute -inset-4 hero-aurora opacity-50 blur-2xl rounded-[2rem] pointer-events-none -z-10"
      />
      {/* Header strip — mimics the dashboard's window chrome so the page
          reads as part of the same product family. */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-white/10">
        <div className="flex items-center gap-1.5">
          <span className="w-3 h-3 rounded-full bg-[#ff5f57]" />
          <span className="w-3 h-3 rounded-full bg-[#febc2e]" />
          <span className="w-3 h-3 rounded-full bg-[#28c840]" />
        </div>
        <div className="text-xs text-zinc-400 font-medium tracking-tight">
          instaedit.app · Editor
        </div>
        <div className="w-12 h-6 rounded-md surface-card-soft flex items-center justify-center">
          <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 mr-1.5" />
          <span className="text-[10px] text-zinc-300">Live</span>
        </div>
      </div>

      {/* Dropzone body */}
      <div className="p-6 sm:p-8">
        <div className="rounded-2xl border-2 border-dashed border-white/15 bg-[#0d0d15]/70 px-6 py-10 sm:py-14 text-center">
          <div className="inline-flex w-14 h-14 items-center justify-center rounded-2xl ring-1 ring-violet-400/40 bg-gradient-to-br from-violet-500/30 to-cyan-500/20 text-violet-200 mb-4">
            <UploadCloud className="w-7 h-7" />
          </div>
          <div className="text-lg font-semibold text-white">
            Drag your raw idea here
          </div>
          <div className="text-sm text-zinc-400 mt-1.5 max-w-[42ch] mx-auto">
            MP4, MOV, WebM or HEVC up to 4 GB. Vertical, horizontal, square — we accept
            what you have.
          </div>
          <div className="mt-5 inline-flex items-center gap-2 px-3 py-1.5 rounded-full bg-white/[0.06] text-[11px] text-zinc-300 ring-1 ring-white/10">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse-glow" />
            or paste a YouTube / Drive link
          </div>
        </div>

        {/* Mock processing state — purely decorative, shows the "engine at
            work" claim with three sub-step indicators. */}
        <div className="mt-5 space-y-2.5">
          {[
            { l: "Encoding · 7 variants per platform", on: true },
            { l: "Subtitles · auto-transcribed + translated", on: true },
            { l: "Thumbnails · generated A/B tests", on: false },
          ].map((step) => (
            <div
              key={step.l}
              className="flex items-center justify-between px-3 py-2 rounded-lg bg-white/[0.04] ring-1 ring-white/5"
            >
              <div className="flex items-center gap-2.5">
                <span
                  className={`w-2 h-2 rounded-full ${
                    step.on
                      ? "bg-emerald-400 animate-pulse-glow"
                      : "bg-zinc-600"
                  }`}
                />
                <span className="text-xs text-zinc-200">{step.l}</span>
              </div>
              <span className="text-[10px] text-zinc-500 tabular-nums">
                {step.on ? "ok" : "queued"}
              </span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function YouTubeEmbed({
  id,
  title,
  aspect,
}: {
  id: string;
  title: string;
  aspect: "9/16" | "16/9";
}) {
  const aspectClass = aspect === "9/16" ? "aspect-[9/16]" : "aspect-[16/9]";

  return (
    <div className="relative overflow-hidden rounded-2xl border border-white/15 bg-[#0a0a12] shadow-[0_25px_80px_-25px_rgba(0,0,0,0.85)]">
      <div className={aspectClass}>
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

function VideoExamplesSection() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 bg-gradient-to-br from-violet-500/12 via-transparent to-cyan-500/10 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-12 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3 inline-flex items-center gap-2">
            <PlayCircle className="w-4 h-4" />
            Real examples
          </div>
          <h2 className="text-display-2 text-white">
            See what the editor produces <span className="text-gradient">in practice.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[60ch]">
            Our system researches the best stock footage and sound effects,
            generates supporting AI images, and places every clip at the right
            moment. A human stays in the loop for the final creative pass —
            After Effects-level quality in minutes, not hours.
          </p>
        </div>

        <div className="grid md:grid-cols-3 gap-4 mb-20">
          {[
            {
              title: "The best resources found automatically",
              copy: "The engine finds the strongest stock footage and SFX for every moment of your story.",
            },
            {
              title: "Placed at the exact moment",
              copy: "AI images and supporting clips are synced with narration, rhythm and emotional arc.",
            },
            {
              title: "Quality with human review",
              copy: "Every cut undergoes creative review for polished results, on par with After Effects, in minutes.",
            },
          ].map((feature) => (
            <div key={feature.title} className="surface-card p-5">
              <CheckCircle2 className="w-5 h-5 text-emerald-400 mb-3" />
              <h3 className="text-sm font-semibold text-white">{feature.title}</h3>
              <p className="text-sm text-zinc-400 leading-relaxed mt-2">{feature.copy}</p>
            </div>
          ))}
        </div>

        <div className="grid lg:grid-cols-12 gap-12 items-start">
          <div className="lg:col-span-5">
            <div className="text-eyebrow text-violet-300/90 mb-3 inline-flex items-center gap-2">
              <PlayCircle className="w-4 h-4" />
              Short-form
            </div>
            <h3 className="text-display-3 text-white mb-3">
              Vertical videos designed for the feed.
            </h3>
            <p className="text-sm text-zinc-400 max-w-[45ch]">
              Native 9:16 outputs for YouTube Shorts, Instagram Reels and TikTok —
              ready to publish without reformatting.
            </p>
          </div>
          <div className="lg:col-span-7 grid grid-cols-1 sm:grid-cols-2 gap-5">
            {SHORT_DEMOS.map((demo) => (
              <YouTubeEmbed key={demo.id} {...demo} aspect="9/16" />
            ))}
          </div>
        </div>

        <div className="mt-24 rounded-3xl border border-cyan-400/15 bg-gradient-to-br from-cyan-500/[0.10] via-[#0d0d15]/80 to-pink-500/[0.08] p-6 sm:p-10 shadow-[0_30px_100px_-45px_rgba(34,211,238,0.45)]">
          <div className="max-w-3xl mb-8">
            <div className="text-eyebrow text-cyan-300/90 mb-3 inline-flex items-center gap-2">
              <MonitorPlay className="w-4 h-4" />
              Long-form
            </div>
            <h3 className="text-display-3 text-white mb-3">
              Horizontal masters for every channel.
            </h3>
            <p className="text-body-lg text-zinc-400 max-w-[60ch]">
              Long-form exports with the right framing, descriptions, thumbnails and chapters
              for YouTube, Facebook, Instagram and LinkedIn.
            </p>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            {LONGFORM_DEMOS.map((demo) => (
              <YouTubeEmbed key={demo.id} {...demo} aspect="16/9" />
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Per-platform outputs row — the visual punchline of the editor page. Each
 * card shows an "auto-generated" badge + the platform brand mark + a title
 * placeholder. Together they read as "one render → 7 native posts".
 * -------------------------------------------------------------------------- */
function OutputCard({
  Logo,
  color,
  format,
  title,
  delay,
}: {
  Logo: (props: LogoProps) => React.ReactElement;
  color: string;
  format: string;
  title: string;
  delay: string;
}) {
  return (
    <div
      className={`surface-card p-5 relative overflow-hidden animate-fade-up ${delay}`}
    >
      <div
        aria-hidden="true"
        className="absolute -top-12 -right-12 w-32 h-32 rounded-full blur-2xl pointer-events-none"
        style={{ background: color, opacity: 0.25 }}
      />
      <div className="relative">
        <div className="flex items-center justify-between mb-3.5">
          <div className="inline-flex w-9 h-9 rounded-lg overflow-hidden ring-1 ring-white/15">
            <Logo className="w-full h-full" />
          </div>
          <span className="inline-flex items-center gap-1 text-[10px] font-medium text-emerald-300/90 uppercase tracking-wider">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
            Auto
          </span>
        </div>
        <div className="text-[11px] text-zinc-500 uppercase tracking-wider mb-1">
          {format}
        </div>
        <div className="text-sm font-semibold text-white leading-snug line-clamp-2">
          {title}
        </div>
        <div
          className="mt-3 h-1 rounded-full"
          style={{ background: color, opacity: 0.7 }}
        />
      </div>
    </div>
  );
}

function OutputsSection() {
  const sample = {
    instagram: { format: "9:16 · Reels", title: "How we ship 10× faster" },
    tiktok: { format: "9:16 · Autoplay", title: "10× faster — full demo" },
    youtube: { format: "16:9 · Short", title: "How we ship 10× faster" },
    facebook: { format: "1:1 · Reels", title: "How we ship 10× faster" },
    x: { format: "16:9 · Native", title: "10× faster, one render" },
    linkedin: { format: "1.91:1 · Post", title: "How we ship 10× faster" },
    threads: { format: "4:5 · Post", title: "10× faster, one render" },
  } as const;

  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-gradient-to-br from-violet-500/12 via-transparent to-cyan-500/10 pointer-events-none"
      />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[420px] h-[420px] -top-20 -right-32 animate-drift-slow opacity-50" />
        <div className="glow-orb bg-cyan-400 w-[380px] h-[380px] -bottom-32 -left-24 animate-drift-rev opacity-45" />
      </div>

      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-12 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">
            One render. Seven native posts.
          </div>
          <h2 className="text-display-2 text-white">
            Every output is modeled for its{" "}
            <span className="text-gradient">platform.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[60ch]">
            Our engine reads each platform's quirks — aspect ratio,
            duration limits, thumbnail rules, caption tone — so a single
            raw render lands natively without you opening another tab.
          </p>
        </div>

        <div className="grid sm:grid-cols-2 lg:grid-cols-4 gap-4">
          {PLATFORM_REGISTRY.map((p, i) => {
            const d = sample[p.key];
            return (
              <OutputCard
                key={p.key}
                Logo={p.Logo}
                color={p.color}
                format={d.format}
                title={d.title}
                delay={`animation-delay-${i * 100}`}
              />
            );
          })}
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * SpeedStats — three before/after stats. The "before" is what an editor
 * typically does today; the "after" is what InstaEdit converts it to.
 * -------------------------------------------------------------------------- */
function SpeedStats() {
  const stats = [
    { before: "6 ore", after: "8 min", label: "Manual editing" },
    { before: "14 schede", after: "1 dashboard", label: "Re-uploads per platform" },
    { before: "7 export", after: "1 render", label: "Re-encoding per channel" },
  ];
  return (
    <section className="relative py-20 sm:py-24 bg-elevated overflow-hidden">
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-12 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">
            Why an editor
          </div>
          <h2 className="text-display-2 text-white">
            From hours of manual work to{" "}
            <span className="text-gradient">a single click.</span>
          </h2>
        </div>

        <div className="grid sm:grid-cols-3 gap-4">
          {stats.map((s, i) => (
            <div
              key={s.label}
              className={`surface-card p-7 relative overflow-hidden animate-fade-up ${
                ["", "animation-delay-100", "animation-delay-200"][i]
              }`}
            >
              <div className="text-sm text-zinc-500 line-through mb-3">
                {s.before}
              </div>
              <div className="text-display-3 text-white tabular-nums">
                {s.after}
              </div>
              <div className="text-eyebrow text-zinc-500 mt-3">
                {s.label}
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * HowItWorks — three numbered stages with a connector line. Visual DNA
 * matches Landing's Workflow section but tuned for the editor scene.
 * -------------------------------------------------------------------------- */
function HowItWorks() {
  const steps = [
    {
      n: "01",
      title: "Drag your raw idea",
      copy: "Vertical, horizontal or square — upload what you shot. We accept MP4, MOV, WebM and HEVC up to 4 GB.",
      Icon: UploadCloud,
      ring: "ring-violet-400/40",
      iconColor: "text-violet-300",
    },
    {
      n: "02",
      title: "The engine rewrites for every platform",
      copy: "Subtitles, thumbnails, hashtags and chapters are generated automatically per platform in one pass.",
      Icon: Sparkles,
      ring: "ring-cyan-400/40",
      iconColor: "text-cyan-300",
    },
    {
      n: "03",
      title: "Schedule once. Publish everywhere.",
      copy: "Pick a time. InstaEdit distributes slots per platform so every audience sees it at peak engagement.",
      Icon: Clock,
      ring: "ring-pink-400/40",
      iconColor: "text-pink-300",
    },
  ];
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div
        aria-hidden="true"
        className="hidden lg:block absolute top-[58%] left-0 right-0 h-px bg-gradient-to-r from-transparent via-violet-400/40 to-transparent pointer-events-none"
      />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">
            How it works
          </div>
          <h2 className="text-display-2 text-white">
            From raw idea to <span className="text-gradient">publication.</span>
          </h2>
        </div>

        <ol className="grid sm:grid-cols-2 lg:grid-cols-3 gap-5 relative">
          {steps.map((s, i) => (
            <li
              key={s.n}
              className={`surface-card p-7 relative overflow-hidden animate-fade-up ${
                ["", "animation-delay-100", "animation-delay-200"][i]
              }`}
            >
              <div
                aria-hidden="true"
                className="absolute -top-16 -right-16 w-44 h-44 rounded-full bg-radial from-violet-500/30 to-violet-500/0 opacity-70 blur-2xl pointer-events-none"
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
                <h3 className="text-display-3 text-white mb-2">
                  {s.title}
                </h3>
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
 * TranslateSection — marketing mockup for the 50+ language localization
 * feature. Same 12-col grid rhythm as OutputsSection/HowItWorks. Left
 * column = eyebrow + headline + 3 feature bullets. Right column = a
 * window-chromed "Translate preview" panel showing the original English
 * line stacked against 4 native-script translations, plus a curated
 * chip grid of supported locales with a "+ 20 more" tail that visually
 * closes the gap to the 50+ claim.
 *
 * The mock text lines are tuned to feel like real subtitles — short,
 * idiomatic, and culturally natural (Italian flips "of five" to a
 * singular team reference, Japanese uses "午後に一人で完結する" which
 * reads as natural JP rather than a literal calque). They're decorative —
 * do not use them to claim actual localized output.
 * -------------------------------------------------------------------------- */
function TranslateSection() {
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

/* ----------------------------------------------------------------------------
 * ShortsCutSection — marketing mockup for the auto-cut feature that
 * pulls 9:16 verticals out of a horizontal long-form. Right column =
 * window-chromed timeline mockup with the source bar at the top and
 * six auto-detected cut segments highlighted across it; below the bar
 * sits a 3-column queue of cards, one per cut, with timestamp ranges
 * and mini progress fills. Times on the timeline are mocked (24:13
 * source) but they read like real editor scrubbing numbers.
 *
 * AUTO-DETECT label (top-right) is intentional copy — it sets the
 * expectation that the editor picks the cuts itself, which is the
 * whole value-prop of the section.
 * -------------------------------------------------------------------------- */
function ShortsCutSection() {
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

/* ----------------------------------------------------------------------------
 * StreamSection — marketing mockup for the 24/7 multistream feature
 * that fans one source out to 7 live destinations. Right column =
 * window-chromed control-room panel: a large 16:8 LIVE hero preview
 * block at the top (showing the now-playing program + aggregate
 * viewer count + uptime), then a 4-up channel grid covering all
 * entries in PLATFORM_REGISTRY. Each channel tile shows its logo,
 * status pill (LIVE or SOON), and a mocked viewer count.
 *
 * The control-room panel intentionally does NOT mirror the real
 * product's publishing UI — the Dashboard does that already. This
 * panel reads as "broadcast equipment" so the page differentiates
 * the stream feature visually from the calendar/queue surfaces.
 * -------------------------------------------------------------------------- */
function StreamSection() {
  type ChannelTile = {
    platform: "youtube" | "facebook" | "instagram" | "tiktok" | "x" | "linkedin" | "threads";
    viewers: number;
    status: "live" | "queued";
    show: string;
  };
  // Per-platform program names so the control-room tiles don't read as a
  // copy-paste — each destination shows what's actually airing on it.
  // Threads runs later (queued → SOON), the rest are LIVE in the mocked
  // snapshot. Aggregate viewers correctly excludes the queued channel.
  const channels: ReadonlyArray<ChannelTile> = [
    { platform: "youtube", viewers: 12483, status: "live", show: "Loop → AMA replay" },
    { platform: "facebook", viewers: 3142, status: "live", show: "Loop → Live Q&A" },
    { platform: "instagram", viewers: 4217, status: "live", show: "Loop → Reels-live" },
    { platform: "tiktok", viewers: 9821, status: "live", show: "Loop → Live drop" },
    { platform: "x", viewers: 1844, status: "live", show: "Loop → Spaces prep" },
    { platform: "linkedin", viewers: 612, status: "live", show: "Loop → Industry chat" },
    { platform: "threads", viewers: 504, status: "queued", show: "Scheduled · 18:30" },
  ];
  const aggregateViewers = channels
    .filter((c) => c.status === "live")
    .reduce((sum, c) => sum + c.viewers, 0);

  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-gradient-to-bl from-violet-500/[0.10] via-transparent to-rose-500/[0.10] pointer-events-none"
      />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[440px] h-[440px] -top-32 -left-24 animate-drift-slow opacity-50" />
        <div className="glow-orb bg-rose-400 w-[360px] h-[360px] -bottom-32 -right-20 animate-drift-rev opacity-40" />
      </div>

      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-12 items-center">
        <div className="lg:col-span-5 animate-fade-up">
          <div className="text-eyebrow text-rose-300/90 mb-3 inline-flex items-center gap-2">
            <Radio className="w-4 h-4" />
            Streaming 24/7
          </div>
          <h2 className="text-display-2 text-white">
            One source.{" "}
            <span className="text-gradient">Seven live destinations.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">            A single control room fans out to every channel — automatic loop of your library, scheduled programming, and 24/7 streaming, even while you sleep.
          </p>

          <ul className="mt-7 space-y-3">
            {[
              {
                t: "Multistream to 7 channels",
                d: "Simultaneous push to YouTube, Facebook, Instagram, TikTok, X, LinkedIn and Threads.",
              },
              {
                t: "Loop + scheduled programming",
                d: "Built-in scheduler for program blocks, replays and live sessions.",
              },
              {
                t: "Always-on with fallback",
                d: "If a source drops, the loop continues until the next slot.",
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

        <div className="lg:col-span-7 animate-fade-up animation-delay-200">
          <div className="surface-glass border border-white/15 rounded-2xl overflow-hidden shadow-[0_30px_100px_-40px_rgba(244,114,182,0.45)]">
            <div className="flex items-center justify-between px-4 py-3 border-b border-white/10">
              <div className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-full bg-[#ff5f57]" />
                <span className="w-2.5 h-2.5 rounded-full bg-[#febc2e]" />
                <span className="w-2.5 h-2.5 rounded-full bg-[#28c840]" />
              </div>
              <div className="text-xs text-zinc-400 font-medium tracking-tight">
                Control room
                <span className="ml-2 text-zinc-500">
                  · {channels.length} destinations
                </span>
              </div>
              <div className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-rose-500/15 ring-1 ring-rose-400/30">
                <span className="w-1.5 h-1.5 rounded-full bg-rose-400 animate-pulse-glow" />
                <span className="text-[10px] font-bold text-rose-200 tracking-wider">
                  LIVE
                </span>
              </div>
            </div>

            <div className="p-5 sm:p-6 space-y-5">
              {/* Hero live preview */}
              <div className="relative aspect-[16/8] rounded-xl overflow-hidden ring-1 ring-white/10 bg-gradient-to-br from-violet-700 via-fuchsia-600 to-cyan-500">
                <div
                  aria-hidden="true"
                  className="absolute inset-0 bg-[radial-gradient(circle_at_30%_40%,rgba(255,255,255,0.25),transparent_55%),radial-gradient(circle_at_72%_62%,rgba(0,0,0,0.45),transparent_60%)]"
                />
                <div className="absolute top-3 left-3 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-rose-500/90 text-[10px] font-bold tracking-wider text-white shadow-[0_0_18px_-2px_rgba(244,63,94,0.6)]">
                  <span className="w-1.5 h-1.5 rounded-full bg-white animate-pulse-glow" />
                  LIVE 24/7
                </div>
                <div className="absolute top-3 right-3 text-[10px] text-white/85">
                  Uptime{" "}
                  <span className="tabular-nums font-semibold">21h 04m</span>
                </div>
                <div className="absolute bottom-3 left-3 right-3 flex items-end justify-between gap-3">
                  <div className="min-w-0">
                    <div className="text-[10px] uppercase tracking-wider text-white/70 mb-0.5">
                      Multistream
                    </div>
                    <div className="text-base sm:text-lg font-semibold text-white leading-tight truncate">
                      Shared broadcast ·{" "}
                      <span className="text-white/70">6 destinations live</span>
                    </div>
                  </div>
                  <div className="text-right flex-shrink-0">
                    <div className="text-[10px] uppercase tracking-wider text-white/70 mb-0.5">
                      Aggregate viewers
                    </div>
                    <div className="text-lg sm:text-2xl font-bold text-white tabular-nums leading-none">
                      {aggregateViewers.toLocaleString()}
                    </div>
                  </div>
                </div>
              </div>

              {/* Channels grid */}
              <div>
                <div className="flex items-center justify-between mb-2.5">
                  <div className="text-eyebrow text-zinc-500">Channels</div>
                  <div className="text-[10px] text-zinc-500">
                    <span className="text-white font-semibold tabular-nums">
                      6
                    </span>
                    <span className="mx-1">live ·</span>
                    <span className="text-white font-semibold tabular-nums">
                      1
                    </span>
                    <span className="ml-1">scheduled</span>
                  </div>
                </div>
                <div className="grid grid-cols-2 sm:grid-cols-4 gap-2.5">
                  {channels.map((ch) => {
                    const entry = PLATFORM_REGISTRY.find(
                      (p) => p.key === ch.platform,
                    );
                    if (!entry) return null;
                    const Logo = entry.Logo;
                    const isLive = ch.status === "live";
                    return (
                      <div
                        key={ch.platform}
                        className="surface-card-soft rounded-lg p-3 relative overflow-hidden"
                      >
                        <div
                          aria-hidden="true"
                          className="absolute -top-12 -right-12 w-24 h-24 rounded-full blur-2xl pointer-events-none"
                          style={{
                            background: entry.color,
                            opacity: isLive ? 0.30 : 0.14,
                          }}
                        />
                        <div className="relative">
                          <div className="flex items-center justify-between mb-2">
                            <span className="inline-flex w-6 h-6 rounded-md overflow-hidden ring-1 ring-white/15">
                              <Logo className="w-full h-full" />
                            </span>
                            {isLive ? (
                              <span className="inline-flex items-center gap-1 text-[10px] font-bold tracking-wider text-rose-300">
                                <span className="w-1.5 h-1.5 rounded-full bg-rose-400 animate-pulse-glow" />
                                LIVE
                              </span>
                            ) : (
                              <span className="text-[10px] font-bold tracking-wider text-amber-300">
                                SOON
                              </span>
                            )}
                          </div>
                          <div className="text-[10px] uppercase tracking-wider text-zinc-500">
                            {entry.name}
                          </div>
                          <div className="flex items-baseline gap-1.5 mt-0.5">
                            <span
                              className={`text-sm font-bold tabular-nums ${isLive ? "text-white" : "text-zinc-400"}`}
                            >
                              {isLive
                                ? ch.viewers.toLocaleString()
                                : "—"}
                            </span>
                            <span className="text-[10px] text-zinc-500">
                              {isLive ? "viewers" : ch.show}
                            </span>
                          </div>
                        </div>
                      </div>
                    );
                  })}
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
 * ContactSection — a direct phone line for prospects who want a human
 * conversation before signing up. Sits between HowItWorks and FinalCTA so
 * the page reads top-to-bottom as: "see the editor → speak to the team →
 * open an account". The number is also a clickable tel: link so mobile
 * visitors can tap to dial.
 *
 * Number: +39 327 464 9129 (Italian mobile, WhatsApp enabled). The tel: URI
 * uses no spaces/formatting per RFC 3966 so it dials correctly on every
 * platform.
 * -------------------------------------------------------------------------- */
const CONTACT_PHONE_DISPLAY = "+39 327 464 9129";
const CONTACT_PHONE_TEL = "+393274649129";
const CONTACT_TELEGRAM_URL = "https://t.me/ytfuri";
const CONTACT_TELEGRAM_HANDLE = "@ytfuri";
const CONTACT_EMAIL = "futurimilionariposta@gmail.com";
// Visible email handle on the chip, truncated at the @ so the email
// affordance is on-screen (mobile-safe, no hover required). Full
// address remains in href + aria-label + title for the click target
// and assistive tech.
const CONTACT_EMAIL_DISPLAY = "futurimilionariposta@…";

function ContactSection() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div
        aria-hidden="true"
        className="absolute inset-0 cta-glow pointer-events-none"
      />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">\n        <div className="glow-orb bg-violet-500 w-[440px] h-[440px] -top-32 -left-24 animate-drift-slow opacity-55" />\n        <div className="glow-orb bg-emerald-400 w-[380px] h-[380px] -bottom-32 -right-24 animate-drift-rev opacity-40" />\n      </div>

      <div className="relative mx-auto max-w-5xl px-6">
        <div className="surface-glass border border-white/15 rounded-3xl px-8 py-14 sm:px-14 sm:py-16 text-center relative overflow-hidden shadow-[0_40px_120px_-40px_rgba(124,58,237,0.5)] animate-fade-up">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-6">\n            <Phone
              className="w-3.5 h-3.5 text-emerald-400"
              aria-hidden="true"
              focusable="false"
            />
            <span>            Parla con il team</span>
          </div>

          <h2 className="text-display-2 text-white max-w-[24ch] mx-auto">
            Want a <span className="text-gradient">closer look?</span>
          </h2>

          {/* Italian call-out — surfaces the user-requested wording
              as an intentional bilingual chip so it reads as deliberate
              copy, not a translation patch sitting inside an otherwise
              English meta row. \*/}
          <div className="mt-5 inline-flex items-center gap-2 px-3 py-1.5 rounded-full bg-emerald-500/10 ring-1 ring-emerald-400/25 text-xs font-medium text-emerald-200">
            <Phone
              className="w-3 h-3"
              aria-hidden="true"
              focusable="false"
            />
            <span>
              Scrivi a{" "}
              <span className="tabular-nums font-semibold">
                {CONTACT_PHONE_DISPLAY}
              </span>{" "}
              per maggiori informazioni
            </span>
          </div>

          <p className="text-body-lg text-zinc-300/90 mt-6 max-w-[52ch] mx-auto">            For custom demos, tailored workflows, or any need that doesn't fit the self-service flow — give us a call. We'll show you what's possible for your team in under ten minutes.
          </p>

          {/* Primary CTA — phone stays the dominant action (white pill).
              Alt-channel options (Telegram, Email, Login) live in a
              second, equally-weighted row so the visual hierarchy reads
              "call us" → "or pick another channel". Telegram opens in a
              new tab with rel=noopener noreferrer per OWASP guidance. */}
          <div className="mt-9 flex items-center justify-center">
            <a
              href={`tel:${CONTACT_PHONE_TEL}`}
              className="group inline-flex items-center gap-3 px-6 py-3.5 rounded-full bg-white text-black text-base font-semibold hover:bg-zinc-100 transition-colors shadow-[0_10px_40px_-10px_rgba(255,255,255,0.55)]"
              aria-label={`Call ${CONTACT_PHONE_DISPLAY} for more information`}
            >
              <Phone
                className="w-5 h-5 group-hover:rotate-[-12deg] transition-transform"
                aria-hidden="true"
                focusable="false"
              />
              <span className="tabular-nums font-semibold">
                {CONTACT_PHONE_DISPLAY}
              </span>
            </a>
            <Link
              to="/login"
              className="inline-flex items-center gap-2 px-6 py-3.5 rounded-full surface-glass text-zinc-200 font-medium hover:text-white hover:border-white/25 transition-colors"
            >
              Oppure apri un account
              <ArrowRight className="w-5 h-5" />
            </Link>
          </div>

          <div className="mt-5 flex items-center justify-center gap-3 flex-wrap">
            <a
              href={CONTACT_TELEGRAM_URL}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-5 py-3 rounded-full surface-glass text-sm font-medium text-zinc-200 hover:text-white hover:border-white/25 transition-colors"
              aria-label={`Open Telegram chat with ${CONTACT_TELEGRAM_HANDLE}`}
            >
              <Send
                className="w-4 h-4 text-sky-300"
                aria-hidden="true"
                focusable="false"
              />
              <span>
                Telegram <span className="text-zinc-400">{CONTACT_TELEGRAM_HANDLE}</span>
              </span>
            </a>
            <a
              href={`mailto:${CONTACT_EMAIL}`}
              className="inline-flex items-center gap-2 px-5 py-3 rounded-full surface-glass text-sm font-medium text-zinc-200 hover:text-white hover:border-white/25 transition-colors"
              aria-label={`Email ${CONTACT_EMAIL}`}
              title={CONTACT_EMAIL}
            >
              <Mail
                className="w-4 h-4 text-violet-300"
                aria-hidden="true"
                focusable="false"
              />
              <span>
                Email{" "}
                <span className="text-zinc-400">
                  {CONTACT_EMAIL_DISPLAY}
                </span>
              </span>
            </a>
          </div>

          <div className="mt-7 text-xs text-zinc-500 flex items-center justify-center gap-2 flex-wrap">
            <span>Lun–Ven · 09:00–18:00 CET</span>
            <span aria-hidden="true">·</span>
            <span>Anche su WhatsApp</span>
          </div>
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------------------------
 * Footer — same Product + Legal two-column shape as Landing. New: a single
 * "Editor" line under Product that links back to /editor (so the page is
 * reachable from itself once the user has scrolled past the hero).
 * -------------------------------------------------------------------------- */
function Footer() {
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
            The InstaEdit Editor turns raw video into seven native
            posts — captions, chapters, and thumbnails per platform.
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

        <div className="lg:col-span-7 grid grid-cols-1 sm:grid-cols-2 gap-8">
          {[
            {
              heading: "Product",
              links: [
                { l: "Editor", to: "/editor" },
                { l: "Home", to: "/" },
                { l: "Sign in", to: "/login" },
                { l: "Privacy", to: "/privacy" },
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
          ].map((col) => (
            <div key={col.heading}>
              <div className="text-eyebrow text-zinc-500 mb-4">
                {col.heading}
              </div>
              <ul className="space-y-3">
                {col.links.map((link) => (
                  <li key={link.l}>
                    {"to" in link ? (
                      <Link
                        to={link.to as string}
                        className="text-sm text-zinc-300 hover:text-white transition-colors"
                      >
                        {link.l}
                      </Link>
                    ) : (
                      <a
                        href={link.href}
                        className="text-sm text-zinc-300 hover:text-white transition-colors"
                      >
                        {link.l}
                      </a>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>
      <div className="border-t border-white/5">
        <div className="mx-auto max-w-7xl px-6 py-6 flex flex-col sm:flex-row items-center justify-between gap-3 text-xs text-zinc-500">
          <div>© {new Date().getFullYear()} InstaEdit, Inc.</div>
          <div>From Raw idea. To every channel. In minutes.</div>
        </div>
      </div>
    </footer>
  );
}

/* ----------------------------------------------------------------------------
 * Editor export — mounted at the public /editor route, no auth, no
 * /app/*-style ProtectedRoute. The page IS the marketing surface for the
 * editor feature; the actual editing happens at /app/compose (authed).
 * -------------------------------------------------------------------------- */
const SEO = {
  title: "InstaEdit Editor — One render, every channel",
  description:
    "Render one video and adapt it natively to TikTok, YouTube, Instagram, Facebook, X, LinkedIn, and Threads — subtitles, thumbnails, hashtags, and chapters included.",
  canonical: "https://app.instaedit.org/editor",
} as const;

export function Editor() {
  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] font-sans antialiased overflow-x-hidden selection:bg-violet-500/40 selection:text-white">
      <Seo {...SEO} />
      <Nav />
      <main className="relative">
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
                <Sparkles className="w-3.5 h-3.5 text-violet-300" />
                <span>The InstaEdit Editor · proprietary graphics engine</span>
              </div>

              <h1 className="text-display-1 text-white">
                From Raw idea.{" "}
                <span className="text-gradient">To every channel.</span>{" "}
                In minutes.
              </h1>

              <p className="text-body-lg text-zinc-300/90 mt-7 max-w-[60ch]">
                One render in. Seven platform-native posts out — captioned,
                thumbnailed, and ready to schedule. Our proprietary graphics
                engine handles the encoding, captions, and per-platform
                quirks so you ship more in less time.
              </p>

              <div className="flex flex-wrap items-center gap-3 mt-9">
                <Link
                  to="/login"
                  className="group inline-flex items-center gap-2 px-5 py-3 rounded-full bg-white text-black text-sm font-semibold hover:bg-zinc-100 transition-colors shadow-[0_10px_40px_-10px_rgba(255,255,255,0.45)]"
                >
                  Connect account
                  <ArrowRight className="w-4 h-4 group-hover:translate-x-0.5 transition-transform" />
                </Link>
                <a
                  href="#outputs"
                  className="inline-flex items-center gap-2 px-5 py-3 rounded-full surface-glass text-sm font-medium text-zinc-200 hover:text-white hover:border-white/25 transition-colors"
                >
                  See what ships
                  <ChevronRight className="w-4 h-4" />
                </a>
              </div>

              <div className="mt-10 flex items-center gap-4 flex-wrap">
                <span className="text-eyebrow text-zinc-500">Outputs to</span>
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
                <span className="text-xs text-zinc-500">+ more each quarter</span>
              </div>
            </div>

            <div className="lg:col-span-5 xl:col-span-6 relative">
              <DropzoneMockup />
            </div>
          </div>
        </section>

        <VideoExamplesSection />
        <div id="outputs">
          <OutputsSection />
        </div>
        <SpeedStats />
        <HowItWorks />
        <TranslateSection />
        <ShortsCutSection />
        {/* Thin divider between ShortsCut and Stream — both sit on the page
            background (no bg-elevated) so without this cue the eye reads
            them as one continuous slab. Mirrors the section-divider utility
            already defined in index.css. Purely decorative (aria-hidden). */}
        <div
          aria-hidden="true"
          className="section-divider mx-auto max-w-7xl"
        />
        <StreamSection />
        <ContactSection />
      </main>
      <Footer />
    </div>
  );
}
