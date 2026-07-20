# InstaEdit — Reachability Audit (English site)

**Data:** blocco di audit del sito pubblico inglese.
**Scopo:** verificare che ogni pagina pubblica attesa sia raggiungibile e linked dalla navigazione / footer.

## Routing map (`web/src/App.tsx`)

| URL             | Componente                | Tipo      | Note                                                 |
| --------------- | ------------------------- | --------- | ---------------------------------------------------- |
| `/`             | `Landing.tsx`             | React     | Home pubblica                                        |
| `/editor`       | `Editor.tsx`              | React     | Pagina di pitch dell'editor (precede il catch-all)  |
| `/login`        | `Login.tsx`               | React     | Entry point canonico per utenti                      |
| `/privacy`      | `PrivacyPolicy.tsx`       | React     | Privacy policy                                       |
| `/terms`        | `TermsOfService.tsx`      | React     | ToS — **NON** `/tos` (vedi nota)                     |
| `/programs`     | `Programs.tsx`            | React     | Pacchetti / programmi                                |
| `/mentoring`    | `Mentoring.tsx`           | React     | Percorso mentoring 1:1                               |
| `/:slug`        | `PlatformPage` (lazy)     | React     | Catch-all per pagine piattaforma (`/tiktok`, ecc.)   |
| `/app/*`        | `InternalLayout`          | React     | Protected — richiede auth                            |
| `/connections`  | redirect → `/app/linking` | React     | Backward-compat                                      |
| `*`             | redirect → `/`            | React     | 404 fallback                                          |

## File esistenza (`web/src/pages` + `web/public`)

✅ Tutte le pagine React attese esistono:
- `web/src/pages/Landing.tsx`           → `/`
- `web/src/pages/Editor.tsx`            → `/editor`
- `web/src/pages/Login.tsx`             → `/login`
- `web/src/pages/PrivacyPolicy.tsx`     → `/privacy`
- `web/src/pages/TermsOfService.tsx`    → `/terms`
- `web/src/pages/Programs.tsx`          → `/programs`
- `web/src/pages/Mentoring.tsx`         → `/mentoring`

✅ Asset statici pubblici (`web/public/`):
- `data-deletion.html` — servito a `/data-deletion.html` (pattern canonico Meta/TikTok per la `Data Deletion Callback URL`)
- `privacy.html`       — servito a `/privacy.html` (duplicato statico, non routato)
- `tos.html`           — servito a `/tos.html` (duplicato statico, non routato)
- `sitemap.xml`, `robots.txt`, favicon e verification files

## Navigation wiring

`MarketingNav.tsx` espone i link:
- `/programs`   ✅
- `/mentoring`  ✅
- `/login`      ✅
- Anchor in-page (Come funziona / Workflow / Features / Agenzie / Chi siamo)

`MarketingFooter.tsx` espone i link:
- `/programs`                ✅
- `/mentoring`               ✅
- `/privacy`                 ✅
- `/terms`                   ✅
- `/data-deletion.html`      ✅ (Meta/TikTok canonical)

## Issues rilevati (non bloccanti)

1. **MarketingNav in italiano.** I label sono ancora: `Come funziona`, `Workflow`, `Features`, `Agenzie`, `Programmi`, `Mentoring`, `Chi siamo`, `Accedi`. Il resto del sito è in Inglese → incoerenza visibile su ogni pagina pubblica.
2. **MarketingFooter in italiano.** Headings e voci: `Prodotto`, `Privacy`, `Termini`, `Cancellazione dati`, `Programmi`, `Mentoring`. Stessa incoerenza del punto 1.
3. **Sitemap sparso.** `web/public/sitemap.xml` elenca solo `/login`, `/privacy`, `/terms`. Mancano `/`, `/editor`, `/programs`, `/mentoring`, `/data-deletion.html` — tutte pagine pubbliche indicizzabili utili per la SEO.
4. **Slug ambiguity.** `tos` viene talvolta invocato nella documentazione (`ENDPOINTS.md`, footer legacy) ma la route canonica è `/terms`. Verificare che ogni riferimento esterno punti a `/terms`.
5. **Catch-all `/:slug`.** Ordine corretto in `App.tsx` (`/editor` precede `/:slug`) → nessun bug. Da mantenere se in futuro si aggiungono route figlie con letterali che potrebbero collidere.
6. **Static HTML duplicati.** `privacy.html` e `tos.html` esistono accanto alle controparti React. Non generano 404 ma sono una fonte di divergenza potenziale (canonica = React route). Valutare se rimuoverli o marcarli esplicitamente come legacy.

## Reviewer findings (post self-review)

Affinamenti ricevuti dopo il primo commit (commit `b02d05a`):

1. **`sitemap.xml` aveva un commento stantio:** il vecchio header diceva che `/` "302-redirects to /accounts which is auth-only". Drift evidente: ora `/` è il `Landing` pubblico. Sistematico in `web/public/sitemap.xml` + aggiunte tutte le pagine pubbliche mancanti (`/`, `/editor`, `/programs`, `/mentoring`, `/data-deletion.html`) con priorità corrette.
2. **Anchor targets non verificati:** `MarketingNav` espone `#pipeline`, `#workflow`, `#features`, `#agency`, `#who-are-we`. L'audit li dichiara reachable lato routing ma non ha confermato che i corrispondenti `id` siano presenti in `Landing.tsx`. Verifica da fare come follow-up — un semplice `grep` sugli `id` della Landing chiude il punto.
3. **`/app` ordering reliance:** `/:slug` è registrato *prima* di `/app` in `App.tsx`. In React Router v6 funziona perché `/app` (segmento statico) outrank-a `/:slug` (segmento dinamico) per specificity — ma nessun commento lo dichiara esplicito, mentre il commento su `/editor` documenta proprio questa stessa dipendenza. Da allineare.

## Verdetto finale (post review)

**Reachability: ✅ tutte le pagine pubbliche attese sono raggiungibili** via routing React o via file statico. Drift SEO (`sitemap.xml`) sistemato nello stesso blocco di audit. Gap residui: (a) verifica `id` Landing per gli anchor nav, (b) allineamento commento in `App.tsx` per `/app` specificity.
