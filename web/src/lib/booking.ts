/* ----------------------------------------------------------------------------
 * Booking config — single-source-of-truth for the strategy-call funnel.
 *
 * Why this lives in `lib/` and not in a component:
 *   - Marketing copy, scarcity cap, intake questions and the scheduler URL
 *     all change together when we tweak the funnel; keeping them in one
 *     file means a marketer or PM can review the entire intake contract
 *     without grepping the codebase.
 *   - The same questions are sent to analytics later (currently
 *     `console.info` in BookingProvider); centralizing the options keeps
 *     the analytics payload identical across every CTA that opens it.
 *
 * Scheduler: Google Appointment Schedules (LIVE)
 *   - The URL is the Google-managed "Copy scheduling page link" format
 *     (`https://calendar.app.google/<short_id>`). Google forwards
 *     querystring params through the redirect, so `?utm_source=...`
 *     survives intact on the landing page.
 *   - Operator can rotate the scheduleId WITHOUT touching this file:
 *     set VITE_BOOKING_URL on the deployment platform (Vercel
 *     Project Settings → Environment Variables, Fly/Docker build-arg)
 *     and rebuild. The fallback below is the LIVE share-link URL used
 *     when the env var is unset (so dev / vitest / Playwright runs
 *     keep resolving to the canonical live URL without any extra
 *     setup).
 *   - Swap the fallback ONCE here AND rebuild only if rotating
 *     in-source. The BookingProvider opens the URL in `_blank` and
 *     the change fan-outs to Hero, Nav, FinalCTA, CTASection tiers,
 *     Mentoring packages, Login, and EditorContact automatically.
 *   - Get the real URL: Google Calendar → Appointment schedules →
 *     click the share icon → "Copy scheduling page link".
 *   - Keep `?utm_source=instagram_landing` so leads from this funnel
 *     stay tagged in the Appointment Schedule analytics (the e2e
 *     test asserts on this substring).
 *
 * Types: `import.meta.env.VITE_BOOKING_URL` is `string | undefined`
 * thanks to `"types": ["vite/client"]` in tsconfig.app.json. The
 * `??` (`??`-nullish) gate fires on `undefined` ONLY — an explicit
 * `VITE_BOOKING_URL=""` is honored as the URL (operator intent)
 * rather than silently swallowed by the fallback. This differs from
 * `src/lib/api.ts::API_BASE_URL` which uses `||`; for the booking
 * URL the precise semantics matter because the e2e test patches
 * `import.meta.env` via Vite and an unexpected fallback could mask
 * a misconfiguration.
 * -------------------------------------------------------------------------- */

/** External scheduler (Google Appointment Schedules) that opens in a
 *  new tab when the qualification form is submitted. Reads
 *  `import.meta.env.VITE_BOOKING_URL` at build time when set; falls
 *  back to the LIVE share-link URL otherwise. Do NOT strip the
 *  `utm_source=instagram_landing` tag: the e2e test asserts on it. */
export const BOOKING_URL =
  import.meta.env.VITE_BOOKING_URL ??
  "https://calendar.app.google/QTmr3puFKCX42i9Q8?utm_source=instagram_landing";

/** "Limited spots" copy. Hook for A/B tests — pass through as the chip
 *  label on every CTA section so the banner can be reworded in one place. */
export const MONTHLY_CAPACITY_LABEL = "10 new clients this month";

/** Coarse-grained "which tier they're booking for" — closed-set so we
 *  can guard it at the type level. The modal uses it to pick a
 *  tier-tinted accent on the header chip + analytics payload. */
export type BookingIntent =
  | "starter"
  | "growth"
  | "premium"
  | "general";

/** Intake answers. Goal + Ready are `null` until the user picks; budget
 *  defaults to `"starter"` server-side because the question was
 *  removed from the modal per operator direction ("senza chiedere
 *  budget"). The Submit button is disabled until goal + ready are
 *  non-null; the analytics payload always carries a budget value. */
export type BookingGoal = "launch" | "scale" | "automated";
export type BookingBudget = "starter" | "base" | "premium";
export type BookingReady = "yes" | "no";

export interface BookingQualification {
  goal: BookingGoal | null;
  budget: BookingBudget;
  ready: BookingReady | null;
}

/** Goal options — also drives the modal header's "Tier 1/2/3" chip. */
export const GOAL_OPTIONS: ReadonlyArray<{
  value: BookingGoal;
  label: string;
  hint: string;
  tier: string;
  accent: "emerald" | "cyan" | "violet";
}> = [
  {
    value: "launch",
    label: "Launch my first channel",
    hint: "Start from zero — proven templates + mentor.",
    tier: "Tier 1 · Starter",
    accent: "emerald",
  },
  {
    value: "scale",
    label: "Scale an existing channel",
    hint: "Multiply output, win-back the algorithm.",
    tier: "Tier 2 · Growth",
    accent: "cyan",
  },
  {
    value: "automated",
    label: "Fully automated investment",
    hint: "Done-for-you across 7 platforms, passive.",
    tier: "Tier 3 · Premium",
    accent: "violet",
  },
];

/** Budget options — clean dollar ranges (no backend-tier names or god-tier
 *  labels that would expose internal sales logic to the visitor). The
 *  selection still serializes to the BookingBudget type for analytics
 *  and lead scoring downstream. */
export const BUDGET_OPTIONS: ReadonlyArray<{
  value: BookingBudget;
  label: string;
}> = [
  { value: "starter", label: "Under $200" },
  { value: "base", label: "$500 – $1,000" },
  { value: "premium", label: "$1,500 – $5,000+" },
];

export const READY_OPTIONS: ReadonlyArray<{
  value: BookingReady;
  label: string;
  hint: string;
  accent: "emerald" | "zinc";
}> = [
  {
    value: "yes",
    label: "Yes — ready this week",
    hint: "We'll prioritize your slot and reserve onboarding time.",
    accent: "emerald",
  },
  {
    value: "no",
    label: "Not yet — exploring",
    hint: "We'll send a self-serve path instead of a hard pitch.",
    accent: "zinc",
  },
];

/** Helper used by the Submit button disabled-state. */
export function isQualificationComplete(
  q: BookingQualification,
): boolean {
  return q.goal !== null && q.budget !== null && q.ready !== null;
}

/** For pre-selecting Goal when the user lands from a tier-specific CTA
 *  (e.g. clicking the Level 2 card on /#programs pre-checks "Scale").
 *  Returning null when intent doesn't map to a goal keeps the form
 *  honest — we never lie about the user's answer. */
export function goalFromIntent(
  intent: BookingIntent,
): BookingGoal | null {
  switch (intent) {
    case "starter":
      return "launch";
    case "growth":
      return "scale";
    case "premium":
      return "automated";
    case "general":
    default:
      return null;
  }
}

/** Tier copy shown next to a CTA that opens the modal — used by
 *  CTASection tiers and Mentoring packages so each card surfaces
 *  which Tier the call will focus on. */
export const INTENT_TIER_LABEL: Record<BookingIntent, string> = {
  starter: "Tier 1 · Starter",
  growth: "Tier 2 · Growth",
  premium: "Tier 3 · Premium",
  general: "Strategy Call",
};
