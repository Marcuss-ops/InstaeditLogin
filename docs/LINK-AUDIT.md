# InstaEdit — Internal Link Audit (English site)

**Scope:** Homepage (`/`), MarketingNav, MarketingFooter, and every internal Link / anchor href across 7 public routes. Block #3 of the broader site-quality plan.

## Executive summary

- **Routing reachability: ✅ all 24 internal `<Link to="…">` instances resolve to a wired route.** Mapping covers Landing, Editor, Programs, Mentoring, MarketingNav, ProtectedRoute, PlatformPage.
- **Anchor targets in Landing: ✅ all 5 anchors resolve.** `#pipeline`, `#workflow`, `#features`, `#agency`, `#who-are-we` present in `Landing.tsx` lines 459, 604, 646, 750, 993.
- **Latent anchor-prefix bug: ✅ fixed in commit `172b782` on `main`.** `MarketingNav` `DEFAULT_LINKS` emitted bare `href="#anchor"` which silently mutated the URL hash instead of navigating to `/` when mounted on `/programs` or `/mentoring`. Switched to `to="/#anchor"` so React Router first resolves the Landing page and then scrolls.
- **Static assets: ✅ all referenced public assets resolve.** `/favicon.svg`, `/favicon.ico`, `/favicon-32x32.png`, `/app-icon-1024.png`, `/data-deletion.html`, `/robots.txt`, `/sitemap.xml`, both TikTok verification files.
- **External resources: ✅ all real and well-formed.** `https://www.youtube.com/embed/{id}` for 8 marketing demos (2 short-form + 4 long-form reused across Landing + Editor, 2 long-form only on Landing), `mailto:hello@instaedit.org` for support.

## Findings

### A. Routing reachability

Internal `<Link to="…">` mapping (24 hits from `rg "to=\"/(login|programs|…)\""`):

| Component / page        | `to` paths                                                                                                                                  | Wired? |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- | ------ |
| `MarketingNav`          | `/login`, `/programs`, `/mentoring`, `/#pipeline`, `/#workflow`, `/#features`, `/#agency`, `/#who-are-we` (post-fix in `DEFAULT_LINKS`)     | ✅     |
| `MarketingFooter`       | `/programs`, `/mentoring`, `/privacy`, `/terms`, `/#pipeline`, `/#workflow`, `/#features`, `/#agency`, `/data-deletion.html`                | ✅     |
| `ProtectedRoute`        | `/login` (unauth redirect)                                                                                                                  | ✅     |
| `Landing.tsx`           | `/login` (4×), `/programs` (1×), `/#anchor` for internal in-page nav                                                                          | ✅     |
| `Editor.tsx`            | `/login` (4×: nav Login + Connect-account, hero CTA, SpeedStats CTA, StreamSection CTA), `/` (back-to root)                                   | ✅     |
| `Login.tsx`             | `/app/dashboard` (post-auth navigate), `/login` self                                                                                        | ✅     |
| `Programs.tsx`          | `/login` (5× across Hero + per-program Cards + CTA + Program entries), `/mentoring` (1×)                                                     | ✅     |
| `Mentoring.tsx`         | `/login` (3× per-package CTAs), `/programs` (2×)                                                                                            | ✅     |
| `PlatformPage`          | `/login` (3×), `/privacy`, `/terms`                                                                                                          | ✅     |

No dangling internal paths. Every route resolves to a literal `<Route>` in `App.tsx`, except `/connections` (literal redirect) and `/app/*` (protected entry which outranks `/:slug` by static-segment specificity — same ranking semantics that protects `/editor`, documented inline in `App.tsx`).

### B. Anchor target verification (`Landing.tsx`)

| Anchor           | Source location in Landing.tsx | Used by                                       | Resolves? |
| ---------------- | ------------------------------ | --------------------------------------------- | --------- |
| `id="pipeline"`  | L459 (`PipelineSection`)       | MarketingNav, MarketingFooter, Programs, Mentoring, Landing inline Nav + Footer | ✅ |
| `id="workflow"`  | L604 (`WorkflowSection`)       | same set                                       | ✅        |
| `id="features"`  | L646 (`Features`)              | same set                                       | ✅        |
| `id="agency"`    | L750 (`AgencySection`)         | same set                                       | ✅        |
| `id="who-are-we"` | L993                          | MarketingNav (`DEFAULT_LINKS`), Programs, Mentoring | ✅     |

### C. Anchor-prefix consistency

| Source                                     | Form used       | Result                                                                  |
| ------------------------------------------ | --------------- | ----------------------------------------------------------------------- |
| `MarketingNav` `DEFAULT_LINKS` (pre-fix)  | bare `#anchor` via `<a>` | **BROKEN** from `/programs` and `/mentoring` (hash only mutates) — fixed in commit `172b782` (`to="/#anchor"` via `<Link>`). |
| `MarketingNav` `DEFAULT_LINKS` (post-fix) | `to="/#anchor"` via `<Link>` | ✅ React Router resolves `/` then scrolls.                              |
| `MarketingFooter`                          | `href="/#anchor"` (full page reload) | Works (forced reload) but slower on `/`. Stylistic drift — follow-up. |
| `Programs.tsx` / `Mentoring.tsx` overrides | `href="/#anchor"` (full page reload) | Works but slower. Stylistic drift — follow-up.                          |
| `Landing.tsx` inline `Nav()` + `Footer()`  | bare `#anchor` via `<a>`            | Works — only mounted on `/` where anchors are on-page.                  |

### D. Localization drift (out of scope for this block)

English-language consistency in the marketing copy is a separate plan item. Label audits happen in the localization block. Tracked but **not fixed** here:

- `MarketingNav` `DEFAULT_LINKS` use Italian labels (`Come funziona`, `Workflow`, `Features`, `Agenzie`, `Programmi`, `Mentoring`, `Chi siamo`).
- `Programs.tsx` and `Mentoring.tsx` `NAV_LINKS` overrides are also Italian.
- `MarketingFooter` headings + link labels are still Italian (`Prodotto`, `Legale`, `Privacy`, `Termini`, `Cancellazione dati`, `Pipeline AI`, `Per agenzie`).
- Mixed-language payloads linger in `Editor.tsx` (`StreamSection` headline + bullets), `Programs.tsx` CTA copy (`Start for free e scopri quale percorso…`), `Mentoring.tsx` testimonial 2.
- `PrivacyPolicy.tsx` and `TermsOfService.tsx` switch between English and Italian body text section by section.

Per the working rule (one task = one focused block), these stay as documented follow-ups.

## Per-page appendix

### Landing.tsx

Internal Links: `/login` (4×), `/programs` (1×, footer), `/` (1×, own anchor nav). Anchors: bare `#anchor` (5 items, only mounted on `/` so safe).

### Editor.tsx

Internal Links: `/` (1×, top-nav back-to-root), `/login` (4×: nav Login, nav Connect-account, SpeedStats CTA, StreamSection CTA). No internal anchors. Two `mailto:hello@instaedit.org` (Page footer). Eight YouTube `<iframe>` embeds via `https://www.youtube.com/embed/{id}` (constants top of file, real YT IDs).

### Login.tsx

Internal Links: `/app/dashboard` (post-auth `navigate`). Mail/login round-trip is local-fetch + state, no internal SPA route beyond `/app/dashboard`. Form action posts to same-origin API and then navigates.

### Programs.tsx

Internal Links via MarketingNav override with Italian labels; program CTAs point to `/login` (5× across Hero + 3 program cards + CTA tile); mentoring section CTA to `/mentoring`; footer to all the same set as MarketingNav default. CTASection has `mailto:hello@instaedit.org`.

### Mentoring.tsx

Internal Links: same pattern as Programs. `/login` (3× in package CTAs); `/programs` (2× in Hero + CTASection). `mailto:hello@instaedit.org?subject=Mentoring%20Request` for the discovery-call button.

### PrivacyPolicy.tsx & TermsOfService.tsx

No internal SPA links. Two `mailto:hello@instaedit.org` instances in each (footer + rights-exercise). Pure document pages.

### PlatformPage.tsx

`<Link to="/login">` ×3 (header CTAs), `<Link to="/privacy">`, `<Link to="/terms">` in footer. All resolve. PlatformPage is the lazy catch-all for `/:slug`.

### MarketingNav / MarketingFooter

MarketingNav is mounted only on Programs + Mentoring (with overrides that informed the default fix in commit `172b782`). MarketingFooter is mounted on Programs + Mentoring. No live broken links; one defensive fix already shipped.

### Static files (`web/public/`)

`/data-deletion.html`, `/robots.txt`, `/sitemap.xml`, `/favicon.svg`, `/favicon.ico`, `/favicon-32x32.png`, `/app-icon-1024.png`, both TikTok verification files. All served from Vite/Express static handler. `data-deletion.html` is the canonical Meta/TikTok deletion-callback URL and links out to `mailto:favamassimo082@gmail.com` and `https://myaccount.google.com/permissions` (both live).

## External resources

- YouTube embed IDs (Landing + Editor): 2 short-form (`MVwXsmRLnwM`, `XCIWzK2BuRo`) + 4 long-form (`fLhv7d6N_3c`, `iA1WT69NFbw`, `R18AVWQ92fs`, `lpKX9SKqSMw`). All real, public, embedding allowed.
- Emails: `hello@instaedit.org` (support), `favamassimo082@gmail.com` (data-deletion contact).
- Google Account permissions link in `/data-deletion.html`.

## Open follow-ups

1. **Anchor-prefix consistency on Programs/Mentoring/MarketingFooter.** Switch the `href="/#anchor"` instances to `to="/#anchor"` so React Router avoids the full page reload they're triggering today. Stylistic, not a bug.
2. **MarketingNav/Footer English labels.** Convert Italian defaults to English; drop the Italian overrides in Programs/Mentoring and rely on the same defaults. Localization block.
3. **Mixed-language content in Editor `StreamSection`, Programs CTA, Mentoring testimonial 2, legal pages.** Cut over to English in the localization block.
4. **Canonical host divergence in SEO surfaces.** `web/index.html` `og:url`/`twitter:url` uses `https://instaedit.org/`, while `sitemap.xml` uses `https://app.instaedit.org/`. Already flagged in `docs/REACHABILITY-AUDIT.md` reviewer findings.
5. **`/app` specificity on `App.tsx`.** Add an inline comment next to the `/app` route so future contributors don't accidentally let a top-level dynamic segment outrank it. Same dependency already documented for `/editor`.

## Verdict

**Reachability: ✅ no 404s, no broken routes, no dangling anchors after the commit `172b782` fix.** Five categorized follow-ups queued for separate blocks (two functional + three localization) — none blocks the link audit deliverable.
