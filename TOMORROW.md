# 📋 Tomorrow — Cosa fare quando riapri

> Questo documento è una checklist temporanea. L'architettura supportata è
> già definita: **Vercel per il frontend e un VPS per backend e stato**.
> Non scegliere provider alternativi e non usare questo file come runbook:
> le procedure canoniche sono [`docs/DEPLOY.md`](docs/DEPLOY.md) e
> [`docs/OPERATIONS.md`](docs/OPERATIONS.md).

---

## ⏱️ Quick wins (15 min totali)

### 1. [5 min] **Verifica e ruota i secret in 1Password**
I valori reali non devono comparire in questo documento, nella chat, nei
log o nel repository. Se un secret è già stato committato o condiviso,
consideralo compromesso e ruotalo prima di riutilizzarlo.

Salva i valori soltanto nel secret manager con record separati per
`JWT_SECRET`, `ENCRYPTION_KEYS`, `ACTIVE_ENCRYPTION_KEY_ID` e
`ADMIN_INVITE_TOKEN`. Non stampare i valori nel terminale: usa input
protetto o l'iniezione diretta del secret manager.

Per generare nuovi valori locali:

```bash
openssl rand -hex 32       # JWT_SECRET
openssl rand -base64 32    # ENCRYPTION_KEYS=1:<base64-key>
openssl rand -hex 32       # ADMIN_INVITE_TOKEN
```

### 2. [8 min] **Deploy della demo su Vercel** ⭐ PRIORITÀ
Per il frontend usa Vercel; per il backend usa il VPS canonico e segui i runbook aggiornati.

1. https://vercel.com → InstaeditLogin → **Settings → Environment Variables**
2. Aggiungi:
   - Key: `VITE_DEMO_MODE`     Value: `true`
   - Key: `VITE_API_BASE_URL`  Value: `https://api.instaedit.invalid`
     (qualcosa di sintatticamente valido, non importa che esista)
3. **Deployments** → ultimo commit → **⋯** → **Redeploy**
4. Aspetta 1-2 min (build verde = tutto ok)
5. Apri l'URL Vercel → dovresti vedere:
   - [ ] Banner **arancione "Demo mode"** in alto
   - [ ] Landing su `/accounts` (no login richiesto)
   - [ ] "Welcome, Demo User"
   - [ ] "No connected accounts" → bottone **Connect more accounts**
   - [ ] `/connections` → 7 provider cards, click → toast "Connect requires backend"
   - [ ] `/compose` → form visibile, submit → toast
   - [ ] `/posts` → "No posts yet"
   - [ ] `/settings/api` → tutti i tab funzionano (API keys, webhooks)

Se tutto si vede: **hai un SPA navigabile, screenshot-ready, da far
vedere a chiunque** (investitori, designer, amici, gatto). ✅

---

## 🧭 Architettura corrente

| Area | Percorso supportato |
|---|---|
| Frontend | Vercel (`web/`, `vercel.json`) |
| Backend | VPS con Docker Compose (`api`, `worker`, `migrate`) |
| Database | PostgreSQL privato nel Compose del VPS |
| Object storage | MinIO privato nel Compose del VPS |
| Proxy e TLS | Caddy gestito sul VPS (`ops/vps/Caddyfile`) |
| Deploy frontend | Workflow Vercel documentato in `docs/DEPLOY.md` §7 |
| Operations | `docs/OPERATIONS.md` e i runbook collegati |

Questa è l'unica topologia production supportata. I materiali Fly legacy
(`docs/archive/legacy-fly/`, parser env Fly + fixture, target `fly-*`)
sono stati rimossi dal repo (2026-08-07).

---

## 🌐 Workflow production canonico

La procedura completa e vincolante è [`docs/DEPLOY.md`](docs/DEPLOY.md):
include provisioning VPS, secret production, Compose, Caddy, verifiche e
deploy Vercel. Per DNS, TLS, monitoring e recovery usa
[`docs/OPERATIONS.md`](docs/OPERATIONS.md).

Controlli pubblici minimi:

```bash
curl https://api.instaedit.org/api/v1/health   # → 200
curl https://api.instaedit.org/ready           # → 200
curl -fsSI https://app.instaedit.org/          # → 200 o redirect configurato
```

---



## 🌅 Se hai tempo / vuoi fare altro

### Aggiungi seed data alla demo (per screenshot più belli)
I mock attualmente ritornano array vuoti. Se vuoi che la demo mostri
"1 Instagram connesso + 2 post di esempio" chiedimelo e aggiungo
qualche fixture in `web/src/lib/demo.ts`. Tempo: 5 min.

### Aggiungi il job "MinIO → Drive per cold storage"
È un task separato di storage e backup; prima di implementarlo aggiorna il
runbook in `docs/DEPLOY.md` e `docs/OPERATIONS.md`. 30 min di codice.

### Self-host email (Postfix)
Se vuoi inviare magic-link dalla tua VPS, configurazione Postfix +
DKIM + SPF + DMARC. Mezza giornata. Per ora skippa — l'email
transazionale non è wirata comunque.

### Self-host error tracking (GlitchTip)
È una feature separata e non modifica la topologia Vercel + VPS.

---

## 📊 Stato del progetto ad oggi

| Cosa | Stato |
|---|---|
| Codice Go (API + worker) | ✅ pronto |
| Codice React (SPA) | ✅ pronto |
| Demo mode (frontend senza backend) | ✅ pronto (test 178/178 verdi) |
| Object storage production | ✅ MinIO nel Compose del VPS |
| Frontend production | ✅ Vercel |
| Meta app | ⏸️ da verificare secondo il provider |
| DNS `api.instaedit.org` | ⏳ verificare in `docs/OPERATIONS.md` |
| Secret production | ⏳ gestire in `docs/DEPLOY.md` §3 |
| Migrazione infrastrutturale | ✅ topologia Vercel + VPS definita |

---

## 🆘 Se ti blocchi

Mandami l'output del comando + la riga esatta dell'errore. Ti rispondo
con il prossimo comando o il fix. No screenshot, no copia-incolla di
secret, no panico.

**Buona sessione domani. 🚀**

---

## 🎯 Booking flow go-live (post-MR)

> Sprint: marketing strategy-call funnel (`POST /api/v1/booking_events`,
> `BookingProvider` modal). Code-side pronto end-to-end — mancano solo i
> 2 swap operatore-side descritti sotto prima di mergiare / deployare.

### Operatori: 1 swap prima del deploy (`BOOKING_HASH_SECRET`)

> **✅ già fatto nel codice**: L'URL dello scheduler è ora cablato in
> `web/src/lib/booking.ts` come
> `https://calendar.app.google/QTmr3puFKCX42i9Q8?utm_source=instagram_landing`
> (formato "Copy scheduling page link" di Google Appointment
> Schedules — il short id `QTmr3puFKCX42i9Q8` sopravvive a querystring
> forwarding, utm_source arriva intatto alla pagina di booking).
> Rimane solo l'env-var lato backend.

**1. Aggiungi `BOOKING_HASH_SECRET` al container di produzione**

- Genera una stringa random ≥32 char (`openssl rand -hex 32`).
- Iniettala come env var sul container `cmd/api`/`cmd/worker`:
  `BOOKING_HASH_SECRET=<stringa>`.
- WITHOUT questo env var, `pkg/api/booking_events.go::computeBookingPepper()`
  logga `WARN: BOOKING_HASH_SECRET unset; booking_events uses a fresh
  per-process random pepper` e i `dedupe_hash` NON sono stabili tra
  restart (refresh-spam da stesso browser produrrà nuove righe invece
  di essere idempotente).

### Operatori: `CORS_ALLOWED_ORIGINS` esplicito

- Anche se `FRONTEND_URL` popola l'allowlist del same-origin gate del
  booking endpoint, è consigliato settare `CORS_ALLOWED_ORIGINS`
  esplicitamente con il dominio di produzione (CSV se multi-dominio).
- Esempio (comporre con il proprio apex):
  `CORS_ALLOWED_ORIGINS=https://instaedit.org,https://app.instaedit.org`
- Formato atteso: lista CSV, EXACT match (no suffix/glob). Lo stesso
  valore popola sia l'allowlist CORS globale sia
  `BookingEventsModule.AllowedOrigins` (vedi
  `pkg/api/routes.go:55-59` → `r.allowedOrigin`).

### Backlog logico (post go-live, NON blocker)

| # | Task | Owner | Note |
|---|------|-------|------|
| 1 | `GET /api/v1/admin/booking_events` con API-key auth + cursor su `created_at` | backend | Permette al sales team di skippare SQL/Metabase |
| 2 | Dashboard metabase/custom collegata alla (1) | data | Dopo che (1) è in piedi |
| 3 | Test d'integrazione backend `INSERT ... ON CONFLICT DO UPDATE` idempotency | backend | Aggiungere a `pkg/api/booking_events_test.go` (già esistente? verifica) |
| 4 | Hardening: BOOKING_HASH_SECRET rotate-on-deploy | backend | Operazionale; documentare runbook rotazione |

### Verifica pre-merge

```bash
# Test E2E del flusso BookingProvider:
cd InstaeditLogin/web && npx playwright test tests/e2e/booking-flow.spec.ts --reporter=line
# Atteso: 1 passed (10.5s) — come da baseline corrente.

# Typecheck TS del web:
cd InstaeditLogin/web && npx tsc -b --pretty
# Atteso: zero errori, build-deps aggiornati.

# Smoke backend (con server in piedi):
curl -X POST http://localhost:8080/api/v1/booking_events \
  -H "Origin: http://localhost:5173" \
  -H "Content-Type: application/json" \
  -d '{"intent":"general","goal":"launch","budget":"starter","ready":"yes"}'
# Atteso: 200 + {"status":"recorded"}. 403 se Origin manca o non matcha.
```

### Note GDPR / privacy (già rispettate dal codice)

- `ip_hash` SHA-256(pepper + ip) → mai memorizzato raw IP.
- `user_agent` / `referer` troncati a 512 byte sul lato Go
  (`bookingEventUATruncate`, `bookingEventRefererTruncate`).
- Nessuna PII (no email / nome) nella tabella `booking_events` —
  primo punto di raccolta contatto è la call stessa.
- ON CONFLICT (dedupe_hash) DO UPDATE = idempotenza refresh-spam;
  cambio risposte stesso IP = nuova riga (corretto, NON inteso come
  suppression).

### Riferimenti interni

- Endpoint handler: `pkg/api/booking_events.go`
- Module wiring: `pkg/api/routes.go:55-59`
- Bootstrap wiring: `internal/bootstrap/app.go` (`bookingEventRepo` +
  `api.WithBookingEventStore`)
- Schema: `internal/database/migrations/076_booking_events.sql`
- Backend model: `internal/models/booking_event.go`
- Frontend modal: `web/src/components/booking/BookingProvider.tsx`
- Frontend config (URL swap): `web/src/lib/booking.ts`
- E2E test: `web/tests/e2e/booking-flow.spec.ts`


---

## 🔁 Schedule rotation playbook (post go-live)

> **Quando serve**: hai cambiato account Google / vuoi un nuovo scheduleId / hai cancellato per errore il vecchio Appointment Schedule.

### Tempo medio di turnaround

- **3-5 minuti** round-trip (copia ID → commit + push → Vercel rebuild → smoke anonimo).

### 5 step operator-side

1. **Copia nuovo ID da Google Calendar**
   - Google Calendar → Appointment schedules → click lucchetto/share → *"Copy scheduling page link"*.
   - URL risultante: `https://calendar.app.google/<NEW_ID>`.
2. **Sostituisci ID in `web/src/lib/booking.ts`** (riga 31 circa)
   - In-source: rimpiazza la stringa fallback `QTmr3puFKCX42i9Q8` con `<NEW_ID>`.
   - **OPPURE** (rotazione senza commit code-side): setta `VITE_BOOKING_URL=https://calendar.app.google/<NEW_ID>?utm_source=instagram_landing` come variabile di build nel progetto Vercel. Vedi il callout al commento `web/src/lib/booking.ts:11-14`.
3. **Rebuild frontend**
   - Vercel: push su `main` trigger auto-rebuild.
   - Locale: `cd InstaeditLogin/web && npm ci && npx tsc --noEmit -p tsconfig.app.json && npm run build`.
4. **Deploy**
   - Vercel: si pubblica automaticamente dopo il commit.
   - Vercel: il push su `main` pubblica automaticamente il frontend; per una rotazione senza commit aggiorna la variabile di build e avvia un redeploy.
5. **Smoke test anonimo (3 min)**
   - Apri `https://instaedit.org` in incognito.
   - Click CTA *"Schedule Your Free Strategy Call"* → completa le 3 domande → Submit.
   - Verifica apertura nuova tab verso `https://calendar.app.google/<NEW_ID>?utm_source=instagram_landing` con griglia di slot visibile senza login Google.
   - Facoltativo: completa una booking reale con email alias per validare il path completo (email di conferma + evento su Google Calendar + Meet link).

### Link interni

- Sezione go-live completo: vedi [`🎯 Booking flow go-live (post-MR)`](#-booking-flow-go-live-post-mr) sopra.
- File sorgente del fallback URL: `web/src/lib/booking.ts:31`.
- Env override hook (header comment con tutte le semantiche): `web/src/lib/booking.ts:1-37`.
- E2E test che pinna il contratto: `web/tests/e2e/booking-flow.spec.ts`.
- Endpoint backend **NON impattato** dalla rotazione: `/api/v1/booking_events` valida solo `intent/goal/budget/ready` (vedi `pkg/api/booking_events.go`); il redirect URL è solo client-side.

### Quando NON serve rotazione

- **Cambio copy marketing** (limited spots, tier labels, copy form): modifica solo `MONTHLY_CAPACITY_LABEL`, `GOAL_OPTIONS`, `INTENT_TIER_LABEL` in `web/src/lib/booking.ts`. Endpoint invariato.
- **Cambio dominio frontend** (es. `instaedit.org` → `.com`): solo env `FRONTEND_URL` + `CORS_ALLOWED_ORIGINS` lato backend (`pkg/api/router.go:55-58`).
- **Cambio layout modal/CTA**: refactor component-side in `web/src/components/booking/` o nelle pagine CTA; niente env/url da toccare.

---
