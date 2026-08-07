import { LANGUAGE_OPTIONS } from "../brand/LanguageFlag";

/* ----------------------------------------------------------------------------
 * Curated list of supported translation markets. Headline copy claims
 * 50+; this list shows the 30 most-requested locales with a shared
 * vector language marker, ISO code, and native name so every surface
 * uses the same visual language. Append here to add a market.
 *
 * Code uses BCP-47 syntax for split locales (e.g. zh-Hant). The visual
 * marker is rendered by the shared LanguageFlag component rather than
 * platform-dependent emoji glyphs.
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
export const CONTACT_EMAIL = "futurimilionariposta@gmail.com";
export const CONTACT_EMAIL_DISPLAY = "futurimilionariposta@…";

// re-exports for Booking URL so editor-area code can reference the
// scheduler outside the booking lib without creating a circular
// import between `editor/shared` and `lib/booking`.
export { BOOKING_URL as CONTACT_BOOKING_URL } from "../../lib/booking";

export const LANGUAGES = LANGUAGE_OPTIONS;

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