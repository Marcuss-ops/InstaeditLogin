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

const KNOWN_FLAG_CODES = new Set<string>(["it", "fr", "de", "pl", "ru", "id", "en", "es", "pt", "tr", "hi", "ar"]);

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
    case "id": {
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
 * Single source of truth for the language selector: the Groups UI builds its
 * dropdown/flag buttons from this list, and languageLabel resolves labels.
 * Keep it aligned with LANGUAGE_MARKERS in groupChannelLanguage.ts.
 */
export const LANGUAGE_OPTIONS = [
  { code: "it", name: "Italiano" },
  { code: "en", name: "English" },
  { code: "es", name: "Español" },
  { code: "fr", name: "Français" },
  { code: "de", name: "Deutsch" },
  { code: "pt", name: "Português" },
  { code: "pl", name: "Polski" },
  { code: "ru", name: "Русский" },
  { code: "tr", name: "Türkçe" },
  { code: "hi", name: "हिन्दी" },
  { code: "id", name: "Bahasa Indonesia" },
  { code: "ar", name: "العربية" },
] as const;

export function languageLabel(code?: string): string | null {
  const normalized = code?.trim().toLowerCase() ?? "";
  return LANGUAGE_OPTIONS.find((option) => option.code === normalized)?.name ?? null;
}
