import { Globe } from "lucide-react";
import type { SVGProps } from "react";

/**
 * LanguageFlag — crisp inline-SVG national flags used to render a channel's
 * configured ISO-639-1 language.
 *
 * Emoji flag glyphs are rendered as letter pairs (or not at all) on several
 * platforms (Windows, Linux desktops, headless renderers), so the groups UI
 * draws flags as small rounded SVGs instead. Shapes are simplified
 * approximations that stay recognisable at 16px; they intentionally carry no
 * trademark detail (emblems, coats of arms, calligraphy).
 *
 * Unknown/empty codes fall back to a neutral globe rather than claiming a
 * language — missing metadata stays visibly missing.
 */
export type LanguageFlagProps = SVGProps<SVGSVGElement> & {
  /** ISO-639-1 language code (e.g. "it", "en"). Case-insensitive. */
  code?: string;
  className?: string;
};

export const LANGUAGE_OPTIONS = [
  { code: "it", name: "Italiano" },
  { code: "en", name: "English" },
  { code: "es", name: "Español" },
  { code: "fr", name: "Français" },
  { code: "de", name: "Deutsch" },
  { code: "pt", name: "Português" },
  { code: "nl", name: "Nederlands" },
  { code: "pl", name: "Polski" },
  { code: "sv", name: "Svenska" },
  { code: "da", name: "Dansk" },
  { code: "no", name: "Norsk" },
  { code: "fi", name: "Suomi" },
  { code: "cs", name: "Čeština" },
  { code: "el", name: "Ελληνικά" },
  { code: "tr", name: "Türkçe" },
  { code: "ru", name: "Русский" },
  { code: "uk", name: "Українська" },
  { code: "ar", name: "العربية" },
  { code: "he", name: "עברית" },
  { code: "hi", name: "हिन्दी" },
  { code: "bn", name: "বাংলা" },
  { code: "th", name: "ไทย" },
  { code: "vi", name: "Tiếng Việt" },
  { code: "id", name: "Bahasa Indonesia" },
  { code: "ms", name: "Bahasa Melayu" },
  { code: "tl", name: "Filipino" },
  { code: "ja", name: "日本語" },
  { code: "ko", name: "한국어" },
  { code: "zh", name: "中文" },
  { code: "zh-hant", name: "繁體中文" },
] as const;

const KNOWN_FLAG_CODES = new Set<string>(LANGUAGE_OPTIONS.map(({ code }) => code));

const STRIPES = {
  it: {
    colors: ["#009246", "#F1F2F1", "#CE2B37"],
    layout: "vertical" as const,
  },
  fr: {
    colors: ["#0055A4", "#F1F2F1", "#EF4135"],
    layout: "vertical" as const,
  },
  de: { colors: ["#1B1B1B", "#DD0000", "#FFCE00"], layout: "horizontal" as const },
  pl: { colors: ["#F1F2F1", "#DC143C"], layout: "horizontal" as const },
  ru: { colors: ["#F1F2F1", "#0039A6", "#D52B1E"], layout: "horizontal" as const },
  id: { colors: ["#CE1126", "#F1F2F1"], layout: "horizontal" as const },
  nl: { colors: ["#AE1C28", "#F1F2F1", "#21468B"], layout: "horizontal" as const },
  cs: { colors: ["#F1F2F1", "#D7141A", "#11457E"], layout: "horizontal" as const },
  uk: { colors: ["#0057B7", "#FFD700"], layout: "horizontal" as const },
  th: { colors: ["#A51931", "#F4F5F8", "#2D2A4A", "#F4F5F8", "#A51931"], layout: "horizontal" as const },
  vi: { colors: ["#DA251D", "#DA251D"], layout: "horizontal" as const },
  zh: { colors: ["#DE2910", "#DE2910"], layout: "horizontal" as const },
  "zh-hant": { colors: ["#0057B7", "#F1F2F1", "#D0181E"], layout: "horizontal" as const },
} as const;

/** 5-point star polygon points (used by the simplified Turkish flag). */
const STAR_POINTS =
  "17.3,6.0 17.77,7.35 19.2,7.38 18.06,8.25 18.48,9.62 17.3,8.8 16.12,9.62 16.54,8.25 15.4,7.38 16.83,7.35";

function StripesFlag({ colors, layout }: { colors: readonly string[]; layout: "vertical" | "horizontal" }) {
  const count = colors.length;
  return (
    <>
      {colors.map((color, index) => {
        const from = (index / count) * 24;
        const size = 24 / count;
        return layout === "vertical" ? (
          <rect key={index} x={from} y={0} width={size} height={16} fill={color} />
        ) : (
          <rect key={index} x={0} y={from} width={24} height={size} fill={color} />
        );
      })}
    </>
  );
}

function FlagArtwork({ code }: { code: string }) {
  switch (code) {
    case "it":
    case "fr":
    case "de":
    case "pl":
    case "ru":
    case "id":
    case "nl":
    case "cs":
    case "uk":
    case "th":
    case "zh":
    case "zh-hant": {
      const stripe = STRIPES[code];
      return <StripesFlag colors={stripe.colors} layout={stripe.layout} />;
    }
    case "en":
      // Simplified Union Jack: blue field, white diagonals + cross, red overlays.
      return (
        <>
          <rect width={24} height={16} fill="#012169" />
          <path d="M0 0 L24 16 M24 0 L0 16" stroke="#F1F2F1" strokeWidth={3.4} />
          <path d="M0 0 L24 16 M24 0 L0 16" stroke="#C8102E" strokeWidth={1.5} />
          <rect x={10.5} y={0} width={3} height={16} fill="#F1F2F1" />
          <rect x={11.55} y={0} width={0.9} height={16} fill="#C8102E" />
          <rect x={0} y={6.5} width={24} height={3} fill="#F1F2F1" />
          <rect x={0} y={7.55} width={24} height={0.9} fill="#C8102E" />
        </>
      );
    case "es":
      return (
        <>
          <rect y={0} width={24} height={4} fill="#AA151B" />
          <rect y={4} width={24} height={8} fill="#F1BF00" />
          <rect y={12} width={24} height={4} fill="#AA151B" />
        </>
      );
    case "pt":
      return (
        <>
          <rect x={0} width={10} height={16} fill="#046A38" />
          <rect x={10} width={14} height={16} fill="#DA291C" />
          <circle cx={10.5} cy={8} r={1.9} fill="#FFE900" />
        </>
      );
    case "vi":
      return (
        <>
          <rect width={24} height={16} fill="#DA251D" />
          <polygon points="12,3.3 13.3,6.6 16.8,6.7 14,8.8 15,12.1 12,10.1 9,12.1 10,8.8 7.2,6.7 10.7,6.6" fill="#FFCD00" />
        </>
      );
    case "sv":
      return (
        <>
          <rect width={24} height={16} fill="#006AA7" />
          <rect x={7} width={3} height={16} fill="#FECC00" />
          <rect y={6.5} width={24} height={3} fill="#FECC00" />
        </>
      );
    case "da":
      return (
        <>
          <rect width={24} height={16} fill="#C8102E" />
          <rect x={7} width={2} height={16} fill="#F1F2F1" />
          <rect y={7} width={24} height={2} fill="#F1F2F1" />
        </>
      );
    case "no":
      return (
        <>
          <rect width={24} height={16} fill="#BA0C2F" />
          <rect x={6} width={5} height={16} fill="#F1F2F1" />
          <rect x={7.5} width={2} height={16} fill="#00205B" />
          <rect y={5.5} width={24} height={5} fill="#F1F2F1" />
          <rect y={7} width={24} height={2} fill="#00205B" />
        </>
      );
    case "fi":
      return (
        <>
          <rect width={24} height={16} fill="#F1F2F1" />
          <rect x={6} width={3} height={16} fill="#003580" />
          <rect y={6.5} width={24} height={3} fill="#003580" />
        </>
      );
    case "el":
      return (
        <>
          <rect width={24} height={16} fill="#0D5EAF" />
          {[2, 6, 10, 14].map((y) => <rect key={y} y={y} width={24} height={2} fill="#F1F2F1" />)}
          <rect width={8} height={8} fill="#0D5EAF" />
          <rect x={3} width={2} height={8} fill="#F1F2F1" />
          <rect y={3} width={8} height={2} fill="#F1F2F1" />
        </>
      );
    case "he":
      return (
        <>
          <rect width={24} height={16} fill="#F1F2F1" />
          <rect y={2} width={24} height={2} fill="#0038B8" />
          <rect y={12} width={24} height={2} fill="#0038B8" />
        </>
      );
    case "bn":
      return (
        <>
          <rect width={24} height={16} fill="#006A4E" />
          <circle cx={11} cy={8} r={4.5} fill="#F42A41" />
        </>
      );
    case "ms":
    case "tl":
      return (
        <>
          <rect width={24} height={16} fill={code === "ms" ? "#CC0001" : "#CE1126"} />
          <rect width={10} height={8} fill={code === "ms" ? "#010066" : "#0038A8"} />
          <polygon points="4,2 5,4.5 7.7,4.5 5.5,6 6.5,8.5 4,7 1.5,8.5 2.5,6 0.3,4.5 3,4.5" fill="#FCD116" />
        </>
      );
    case "ja":
      return (
        <>
          <rect width={24} height={16} fill="#F1F2F1" />
          <circle cx={12} cy={8} r={4.4} fill="#BC002D" />
        </>
      );
    case "ko":
      return (
        <>
          <rect width={24} height={16} fill="#F1F2F1" />
          <path d="M12 4.7a3.3 3.3 0 1 0 0 6.6 1.65 1.65 0 1 1 0-3.3 1.65 1.65 0 1 0 0-3.3" fill="#CD2E3A" />
          <path d="M12 11.3a3.3 3.3 0 1 0 0-6.6 1.65 1.65 0 1 1 0 3.3 1.65 1.65 0 1 0 0 3.3" fill="#0047A0" />
        </>
      );
    case "tr":
      return (
        <>
          <rect width={24} height={16} fill="#E30A17" />
          <circle cx={13.2} cy={8} r={4.5} fill="#F1F2F1" />
          <circle cx={14.4} cy={8} r={3.8} fill="#E30A17" />
          <polygon points={STAR_POINTS} fill="#F1F2F1" />
        </>
      );
    case "hi":
      return (
        <>
          <rect y={0} width={24} height={5.33} fill="#FF9933" />
          <rect y={5.33} width={24} height={5.33} fill="#F1F2F1" />
          <rect y={10.66} width={24} height={5.33} fill="#138808" />
          <circle cx={12} cy={8} r={1.8} fill="#000080" />
        </>
      );
    case "ar":
      // Simplified Saudi banner (green field + white band). Real calligraphy
      // is intentionally omitted at this size.
      return (
        <>
          <rect width={24} height={16} fill="#165D31" />
          <rect y={7} width={24} height={2} fill="#F1F2F1" />
        </>
      );
    default:
      return null;
  }
}

export function LanguageFlag({ code, className = "h-4 w-4", ...rest }: LanguageFlagProps) {
  const normalized = code?.trim().toLowerCase() ?? "";
  if (!normalized || !KNOWN_FLAG_CODES.has(normalized)) {
    // No artwork for this code: neutral globe, no language claim.
    return <Globe aria-hidden="true" className={className} {...rest} />;
  }
  return (
    <svg viewBox="0 0 24 16" className={className} {...rest} aria-hidden="true" focusable="false">
      <FlagArtwork code={normalized} />
    </svg>
  );
}

/**
 * Single source of truth for the language selector: the Groups UI and the
 * editor use this list for labels, codes, and the shared SVG flags.
 */
export function languageLabel(code?: string): string | null {
  const normalized = code?.trim().toLowerCase() ?? "";
  return LANGUAGE_OPTIONS.find((option) => option.code === normalized)?.name ?? null;
}
