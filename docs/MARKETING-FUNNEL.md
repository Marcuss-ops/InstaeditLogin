# Marketing Funnel — Standard for /mentoring and /programs

Codifies the section order, cross-linking rules, and claims policy for the
two high-intent marketing landing pages (`web/src/pages/Mentoring.tsx` and
`web/src/pages/Programs.tsx`). New marketing pages must follow this standard
unless an explicit deviation is recorded here.

## 1. Funnel architecture

Every high-intent page renders sections in this order. Reordering weakens
the conversion path (e.g. pricing before value props increases bounce;
social proof before comparison reduces anchoring to the offered tier).

| # | Section | Purpose | Why this position |
|---|---------|---------|-------------------|
| 1 | **Hero** | Eyebrow + h1 + body + 1 primary + 1 secondary CTA. | Anchors the visitor to a single specific outcome. |
| 2 | **Value props** | Numbered steps / feature grid / "what you get". | Explains _how_ the outcome is delivered. |
| 3 | **Pricing / Comparison** | Tier cards + comparison table where applicable. | Lets the visitor self-select a tier before any social proof colors their judgment. |
| 4 | **Social proof** | Testimonials grid (3 quotes, distinct authors per page). | Validates the tier-selection _just made_ in step 3, before offering off-ramps. |
| 5 | **Cross-link** *(optional)* | Link to the adjacent marketing page (Mentoring ↔ Programs). | Goes _after_ social proof so the off-ramp is taken with confidence after validation. |
| 6 | **FAQ** | 6 Q&A addressing the most common late-funnel objections (refund/results/switching). | Last-mile objection handling directly before the final CTA. |
| 7 | **Final CTA** | 2-3 buttons: book a call (mailto) + try free (`/login`) + see adjacent funnel (`/programs` or `/mentoring`). | Single conversion-oriented card, mirrors the Hero button set but at higher intent. |

Pages currently following this standard: `/programs`, `/mentoring`.

## 2. Cross-linking rules

The user is on one of two adjacent marketing pages. Cross-links must be:

- **One direction per page.** Each page is the destination of the other's
  cross-link. Avoid reciprocal links in adjacent sections that loop the
  visitor.
- **Placed _after_ social proof.** Social proof validates the current page's
  offering before the visitor is invited to consider the alternative.
- **Phrased as a parallel, not an upsell.** "Want guidance?" and "See mentoring"
  are invitations, not "Buy the bigger thing". Programs is the production
  backbone; Mentoring is the strategic backbone. They're complementary.

CTA buttons on both pages mirror each other in shape:

| Position | Programs CTA | Mentoring CTA |
|----------|--------------|---------------|
| Primary | `mailto:?subject=Programs%20Question` | `mailto:?subject=Mentoring%20Request` |
| Secondary | `/login` ("Start for free" / "Try InstaEdit free") | `/login` (same) |
| Tertiary | `/mentoring` ("Want guidance?") | `/programs` ("See programs") |

## 3. Claims & pricing policy

Hard rules. Violations produce a code-review fail and may produce a
compliance flag.

### Pricing

- **No fabricated dollar amounts on the marketing site.**
  We do not publish precise integers for Mentoring packages, refund
  windows, or "results in N weeks" claims unless they are backed by code in
  `pkg/api/billing.go` or `internal/services/billing_service.go` at the
  time the page is published.
- **Use billing models, not integers.** Acceptable shapes:
  - "Billed per session"
  - "Billed per package · quarterly engagement"
  - "Custom team contract · annual SLA against milestones"
  - "Per-seat · starts at $X/month" — only if `$X` is present in the
    billing config (`config/production.yaml` or equivalent) AND has been
    validated by product. Used today only on Programs Agency tier
    ($99/mo).
- **Programs.** Creator: "Included with InstaEdit Pro" (true). Agency:
  "Per-seat · starts at $99/month" (validated). Enterprise: "Custom
  enterprise pricing" (model only).
- **Final-quote pattern.** Discovery-call pages always use mailto with
  `subject=Mentoring%20Request` or `subject=Programs%20Question` so the
  sales inbox can route inquiry by program.

### Testimonials

- **Author names and roles must be distinct across pages.** A visitor
  who reads /mentoring and then /programs must never see the same quote
  or the same author surfaced twice. Quote text must also be distinct.
- **Generic, non-attributable "creator"/"agency"/"team" persona roles are
  acceptable** as long as they don't duplicate other pages' personas.

### Timeline & refund claims

- **Generic ranges only.** "Self-paced · typically 6-8 weeks" is OK because
  it's a typical, not a guarantee. "Production-ready workflow in 6-8 weeks"
  is also accepted as a typical industry range, not a contractual SLA.
- **Specific windows are not OK** unless grounded in code: "30-day
  money-back", "results in 4 weeks", "24-hour onboarding" — all of these
  would create a contractual exposure.
- **Refund policy answer** in FAQ must read as a model description,
  not a number: "Refund terms are shared during onboarding." — not
  "30-day money-back guarantee."

### SOC2 / compliance

- Mentioning SOC2 on the Enterprise tier is OK as a feature label — the
  SOC2 audit trail exists in the product. Avoid promising specific
  certifications or renewal dates unless they're in the trust center.

## 4. Future pages

When adding a third high-intent marketing page (e.g. `/enterprise`,
`/agencies`), follow sections 1-3. Place a cross-link in section 5 of
both existing pages. Update this doc with the new page name.
