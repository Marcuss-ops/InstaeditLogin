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
  // The short forms are intentionally accepted after a title separator or
  // at the end of a concatenated brand name (BoxeClubITA, BoxeClubFr).
  { code: "it", pattern: /(?:^|[\s._-])(?:italiano|italian|italia|italy|ita)(?=$|[\s._-])|ita$/i },
  { code: "en", pattern: /(?:^|[\s._-])(?:english|inglese|england|eng)(?=$|[\s._-])|eng$/i },
  { code: "es", pattern: /(?:^|[\s._-])(?:español|espanol|spanish|españa|espana|spain|castellano|es)(?=$|[\s._-])|es$/i },
  { code: "fr", pattern: /(?:^|[\s._-])(?:français|francais|french|france|fr)(?=$|[\s._-])|fr$/i },
  { code: "de", pattern: /(?:^|[\s._-])(?:deutsch|german|deutschland|germany|de)(?=$|[\s._-])|de$/i },
  { code: "pt", pattern: /(?:^|[\s._-])(?:português|portugues|portuguese|brasil|brazil|pt)(?=$|[\s._-])|pt$/i },
  { code: "pl", pattern: /(?:^|[\s._-])(?:polski|polish|polska|poland|pl)(?=$|[\s._-])|pl$/i },
  { code: "ru", pattern: /(?:^|[\s._-])(?:русский|россия|russian|russia|ru)(?=$|[\s._-])|ru$/i },
  { code: "tr", pattern: /(?:^|[\s._-])(?:türkçe|turkce|turkish|türkiye|turkiye|turkey|tr)(?=$|[\s._-])|tr$/i },
  { code: "hi", pattern: /(?:^|[\s._-])(?:हिन्दी|हिंदी|hindi|भारतीय)(?=$|[\s._-])|hindi$/i },
  { code: "id", pattern: /(?:^|[\s._-])(?:bahasa indonesia|indonesian|indonesia|id)(?=$|[\s._-])|id$/i },
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
