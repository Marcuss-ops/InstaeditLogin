import { Globe } from "lucide-react";
import type { ComponentType, SVGProps } from "react";
// Official, accurate flag artwork (3:2) imported per-flag so only the 30
// languages the UI supports end up in the bundle. Each import is a tiny
// standalone React SVG component.
import IT from "country-flag-icons/react/3x2/IT";
import GB from "country-flag-icons/react/3x2/GB";
import ES from "country-flag-icons/react/3x2/ES";
import FR from "country-flag-icons/react/3x2/FR";
import DE from "country-flag-icons/react/3x2/DE";
import PT from "country-flag-icons/react/3x2/PT";
import NL from "country-flag-icons/react/3x2/NL";
import PL from "country-flag-icons/react/3x2/PL";
import SE from "country-flag-icons/react/3x2/SE";
import DK from "country-flag-icons/react/3x2/DK";
import NO from "country-flag-icons/react/3x2/NO";
import FI from "country-flag-icons/react/3x2/FI";
import CZ from "country-flag-icons/react/3x2/CZ";
import GR from "country-flag-icons/react/3x2/GR";
import TR from "country-flag-icons/react/3x2/TR";
import RU from "country-flag-icons/react/3x2/RU";
import UA from "country-flag-icons/react/3x2/UA";
import SA from "country-flag-icons/react/3x2/SA";
import IL from "country-flag-icons/react/3x2/IL";
import IN from "country-flag-icons/react/3x2/IN";
import BD from "country-flag-icons/react/3x2/BD";
import TH from "country-flag-icons/react/3x2/TH";
import VN from "country-flag-icons/react/3x2/VN";
import ID from "country-flag-icons/react/3x2/ID";
import MY from "country-flag-icons/react/3x2/MY";
import PH from "country-flag-icons/react/3x2/PH";
import JP from "country-flag-icons/react/3x2/JP";
import KR from "country-flag-icons/react/3x2/KR";
import CN from "country-flag-icons/react/3x2/CN";
import TW from "country-flag-icons/react/3x2/TW";

/**
 * LanguageFlag — crisp inline-SVG national flags used to render a channel's
 * configured ISO-639-1 language.
 *
 * Emoji flag glyphs are rendered as letter pairs (or not at all) on several
 * platforms (Windows, Linux desktops, headless renderers), so the groups UI
 * draws flags as small rounded SVGs instead — using the official
 * `country-flag-icons` artwork rather than approximations, so every flag
 * looks sharp and current at any size.
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

type FlagComponent = ComponentType<SVGProps<SVGSVGElement>>;

// ISO-639-1 language code → representative country flag.
const FLAG_BY_CODE = {
  it: IT,
  en: GB,
  es: ES,
  fr: FR,
  de: DE,
  pt: PT,
  nl: NL,
  pl: PL,
  sv: SE,
  da: DK,
  no: NO,
  fi: FI,
  cs: CZ,
  el: GR,
  tr: TR,
  ru: RU,
  uk: UA,
  ar: SA,
  he: IL,
  hi: IN,
  bn: BD,
  th: TH,
  vi: VN,
  id: ID,
  ms: MY,
  tl: PH,
  ja: JP,
  ko: KR,
  zh: CN,
  "zh-hant": TW,
} as unknown as Record<string, FlagComponent>;

export function LanguageFlag({ code, className = "h-4 w-4", ...rest }: LanguageFlagProps) {
  const normalized = code?.trim().toLowerCase() ?? "";
  const Flag = FLAG_BY_CODE[normalized];
  if (!Flag) {
    // No artwork for this code: neutral globe, no language claim.
    return <Globe aria-hidden="true" className={className} {...rest} />;
  }
  return <Flag aria-hidden="true" focusable="false" className={className} {...rest} />;
}

/**
 * Single source of truth for the language selector: the Groups UI and the
 * editor use this list for labels, codes, and the shared SVG flags.
 */
export function languageLabel(code?: string): string | null {
  const normalized = code?.trim().toLowerCase() ?? "";
  return LANGUAGE_OPTIONS.find((option) => option.code === normalized)?.name ?? null;
}
