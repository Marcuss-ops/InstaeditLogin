import type { SVGProps } from "react";
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
      <path
        d="M13.6 21v-7.2h2.4l.36-2.8H13.6V9.05c0-.81.23-1.35 1.4-1.35h1.5V5.15c-.26-.03-1.15-.11-2.18-.11-2.16 0-3.64 1.32-3.64 3.74v2.22H8.32v2.8h2.36V21h2.92z"
        fill="#fff"
      />
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
export function XLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
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
export function LinkedInLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
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
export function ThreadsLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
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

export const CONTACT_PHONE_DISPLAY = "+39 327 464 9129";
export const CONTACT_PHONE_TEL = "+393274649129";
export const CONTACT_DISCORD_URL = "https://discord.com/users/1201477873719050332";
export const CONTACT_DISCORD_HANDLE = "@instaedit";
export const CONTACT_EMAIL = "futurimilionariposta@gmail.com";
export const CONTACT_EMAIL_DISPLAY = "futurimilionariposta@…";

export const LANGUAGES = [
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

export function YouTubeEmbed({
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
        {/* `web-share` removed from Chromium 120+ Permissions-Policy
            allow-list (triggers `[warn] Unrecognized feature: 'web-share']` in
            DevTools). See commit 2902c76. */}
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