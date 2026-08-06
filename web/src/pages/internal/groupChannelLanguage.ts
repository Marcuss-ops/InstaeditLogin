export type DetectedChannelLanguage = {
  language: string | null;
  candidates: string[];
  reason: "explicit-marker" | "ambiguous-markers" | "insufficient-signal";
};

type LanguageMarker = {
  code: string;
  pattern: RegExp;
};

// Keep this list intentionally conservative. A title is only auto-applied when
// it contains an explicit language/country marker unique to one language.
// Generic words such as "news" or "official" are deliberately excluded: they
// are too common to be reliable evidence and would create silent overwrites.
const LANGUAGE_MARKERS: LanguageMarker[] = [
  { code: "it", pattern: /\b(?:italiano|italian|italia|italy|ita)\b/i },
  { code: "en", pattern: /\b(?:english|inglese|england|eng)\b/i },
  { code: "es", pattern: /\b(?:español|espanol|spanish|españa|espana|spain|castellano)\b/i },
  { code: "fr", pattern: /\b(?:français|francais|french|france)\b/i },
  { code: "de", pattern: /\b(?:deutsch|german|deutschland|germany)\b/i },
  { code: "pl", pattern: /\b(?:polski|polish|polska|poland)\b/i },
  { code: "ru", pattern: /(?:русский|россия|russian|russia)\b/i },
  { code: "tr", pattern: /\b(?:türkçe|turkce|turkish|türkiye|turkiye|turkey)\b/i },
  { code: "hi", pattern: /(?:हिन्दी|हिंदी|hindi|भारतीय)\b/i },
  { code: "id", pattern: /\b(?:bahasa indonesia|indonesian|indonesia)\b/i },
];

export function detectChannelLanguage(title: string): DetectedChannelLanguage {
  const normalizedTitle = title.trim();
  if (!normalizedTitle) {
    return { language: null, candidates: [], reason: "insufficient-signal" };
  }

  const candidates = LANGUAGE_MARKERS
    .filter(({ pattern }) => pattern.test(normalizedTitle))
    .map(({ code }) => code);
  const uniqueCandidates = Array.from(new Set(candidates));

  if (uniqueCandidates.length === 1) {
    return {
      language: uniqueCandidates[0],
      candidates: uniqueCandidates,
      reason: "explicit-marker",
    };
  }
  if (uniqueCandidates.length > 1) {
    return {
      language: null,
      candidates: uniqueCandidates,
      reason: "ambiguous-markers",
    };
  }
  return { language: null, candidates: [], reason: "insufficient-signal" };
}
