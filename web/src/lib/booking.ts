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
 *   - Swap it ONCE here — the BookingProvider opens the URL in
 *     `_blank` and the change fan-outs to Hero, Nav, FinalCTA,
 *     CTASection tiers, Mentoring packages, Login, and EditorContact
 *     automatically.
 *   - Get the real URL: Google Calendar → Appointment schedules →
 *     click the share icon → "Copy scheduling page link".
 *   - Keep `?utm_source=instagram_landing` so leads from this funnel
 *     stay tagged in the Appointment Schedule analytics.
 * -------------------------------------------------------------------------- */

/** External scheduler (Google Appointment Schedules) that opens in a
 *  new tab when the qualification form is submitted. LIVE URL — the
 *  operator-side Appointment Schedule `QTmr3puFKCX42i9Q8` is wired
 *  into the SPA bundle; if the slot is rotated, replace the ID after
 *  `/` here and rebuild. Do NOT strip the utm_source tag: the e2e
 *  test asserts on it. */
export const BOOKING_URL =
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

/** Intake answers. Each is `null` until the user selects a radio card;
 *  the Submit button is disabled until all three are non-null. */
export type BookingGoal = "launch" | "scale" | "automated";
export type BookingBudget = "starter" | "base" | "premium";
export type BookingReady = "yes" | "no";

export interface BookingQualification {
  goal: BookingGoal | null;
  budget: BookingBudget | null;
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

/** Budget options — the user-message specifies dollar ranges for
 *  qualification. We render the matching plan name under each option. */
export const BUDGET_OPTIONS: ReadonlyArray<{
  value: BookingBudget;
  label: string;
  route: string;
}> = [
  { value: "starter", label: "Under $200", route: "Routes to Starter · $197" },
  { value: "base", label: "$500 – $1,000", route: "Routes to Base / Medium plan" },
  {
    value: "premium",
    label: "$1,500 – $5,000+",
    route: "Routes to Premium / GOD Tier",
  },
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
