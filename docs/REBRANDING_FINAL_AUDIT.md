# Rebranding finale — Dark Editor → InstaEditor

**Data:** 2026-08-07
**Branch:** `main`
**Ambito:** inventario finale delle varianti legacy e distinzione tra riferimenti da mantenere e riferimenti da rimuovere.

## Risultato

La superficie commerciale e i testi rivolti all’utente usano **InstaEditor**. Il nome legacy non deve essere usato per nuovi testi UI, copy marketing o commenti descrittivi non storici.

La scansione finale è stata eseguita sui file tracciati da Git, escludendo questo report (`docs/REBRANDING_FINAL_AUDIT.md`), backup `.bak`, `node_modules`, `dist` e `test-results`. Il report è quindi escluso dal proprio conteggio. Il pattern usato è:

```text
Dark Editor | DarkEditor | dark-editor | dark_editor | darkEditor | DARK_EDITOR
```

### Inventario iniziale della scansione

| Categoria | File | Occorrenze | Disposizione |
|---|---:|---:|---|
| Compatibilità path/env/infra | 9 | 25 | Ammesse e mantenute |
| Test e casi negativi | 5 | 18 | Ammessi: verificano legacy input, path o assenza di namespace storico |
| Documentazione storica | 5 | 11 | Ammessa quando descrive migrazione, audit o stato operativo precedente |
| Commenti sorgente aggiornabili | 6 | 8 | Aggiornati a InstaEditor dove il file era pulito |
| **Totale** | **25** | **62** | **Nessun testo UI legacy intenzionale** |

## Risultato della scansione post-cleanup

Ripetendo la stessa scansione dopo gli aggiornamenti, ed escludendo ancora questo report, risultano **53 occorrenze residue in 20 file**:

| Residuo | File | Occorrenze | Motivo |
|---|---:|---:|---|
| Compatibilità path/env/infra e route test | 9 | 25 | `/dark_editor_v2`, `INSTAEDITOR_URL`, fallback `EDITOR_URL` e contratti di routing |
| Test e casi negativi | 5 | 17 | fixture URL, fallback legacy, chiavi storiche rifiutate e assertion di assenza UI |
| Documentazione storica | 5 | 10 | audit, migrazioni, deployment e note di compatibilità |
| Commento locale preesistente escluso dal commit | 1 | 1 | commento non visibile in `web/src/features/youtube/api/editorSessionsApi.ts` |
| **Totale residuo ammesso** | **20** | **53** | **zero copy UI legacy** |

Il report stesso contiene citazioni delle varianti per spiegare l’inventario, ma è escluso dalla scansione per evitare un risultato autoreferenziale.

## Riferimenti ammessi

### 1. Path runtime e routing

Il path `/dark_editor_v2` è ancora un contratto tecnico attivo: è il `basePath` della SPA Velox/Next e viene verificato da Caddy, Compose, smoke test e deploy verifier. Sono quindi ammessi:

- `/dark_editor_v2` e `/dark_editor_v2/*`;
- `/dark_editor_v2/editor/{velox_project_id}`;
- valori `editor_url` che puntano a quel path;
- test di apertura, redirect e compatibilità del path.

Non va sostituito con `/instaeditor` senza una migrazione coordinata del servizio remoto, dei consumer e del routing di produzione.

### 2. Variabili ambiente backward-compatible

- `INSTAEDITOR_URL` è il nome canonico e ha precedenza.
- `EDITOR_URL` è mantenuto come fallback legacy per gli ambienti esistenti.
- Gli esempi `.env` possono contenere entrambi, con la stessa destinazione `/dark_editor_v2`.
- `internal/config/config_test.go` verifica la precedenza del nome InstaEditor e il fallback backward-compatible del nome legacy.

### 3. Test e identificatori tecnici

Sono ammessi i riferimenti nei test quando verificano:

- URL legacy e codifica del project id;
- fallback `EDITOR_URL`;
- rigetto di vecchie chiavi browser (`dark-editor.settings`, `dark_editor_theme`);
- assenza di testo “Dark Editor” nella UI (`queryByText` negativo);
- il filename storico `AUDIT_DARK_EDITOR.md`.

`dark-editor-frame` è un locator storico di test e non rappresenta un elemento UI attivo: il test conferma che l’iframe non venga renderizzato.

### 4. Documenti storici e audit

I documenti possono citare il nome precedente quando devono spiegare una decisione, una migrazione, un audit o un deployment rimosso. Ogni nuova frase descrittiva deve usare InstaEditor e chiarire il contesto storico.

## Riferimenti rimossi/aggiornati

Sono stati rinominati a **InstaEditor** i commenti sorgente non storici in:

- `web/src/pages/internal/livestreamWizardStep2.tsx`;
- `web/src/features/channels/hooks/useYouTubePublishLiveUpdate.ts`;
- `web/src/features/channels/hooks/useChannelContent.ts`;
- `web/src/features/channels/utils/thumbnailUrl.ts`;
- `web/src/lib/storageKeys.ts`;
- intestazione del test `web/src/pages/internal/Covers.test.tsx`.

È stato inoltre corretto il riferimento documentale `openDarkEditor` in `docs/vertical-slice-e2e.md` verso l’helper canonico `openInstaEditorInNewTab`.

`web/src/features/youtube/api/editorSessionsApi.ts` contiene una modifica locale preesistente non inclusa in questo commit. Il suo singolo riferimento residuo è un commento non visibile all’utente e resta classificato come cleanup successivo del relativo lavoro locale, non come identificatore runtime.

## Regola per le verifiche future

Una scansione futura è accettabile quando ogni match è in una delle categorie ammesse sopra. In particolare:

1. nessun match in testo UI, title, meta description o copy marketing;
2. nessun nuovo identificatore runtime `DarkEditor`/`darkEditor`;
3. `/dark_editor_v2` resta coperto dai test di routing;
4. `EDITOR_URL` resta testato come fallback finché tutti gli ambienti non passano a `INSTAEDITOR_URL`;
5. i match storici devono dichiarare esplicitamente il contesto storico o di compatibilità.

## Verifiche eseguite

- ricerca case-insensitive di tutte le varianti legacy;
- controllo separato di source, test, documenti, env, Caddy, Compose e script operativi;
- verifica che il path `/dark_editor_v2` non sia stato modificato;
- `git diff --check` sui file aggiornati;
- test frontend mirati e suite frontend già verificata nel ciclo browser precedente;
- code review dei cambiamenti di branding.

## Aggiornamento 2026-08-08 — passata documentale VeloxFrontend

Riscansione completa dei tre repo (InstaeditLogin, VeloxFrontend, VeloxEditiingg) per i riferimenti descrittivi correnti “Dark Editor”. I sorgenti (Go, TS/TSX) sono puliti; sono stati aggiornati a **InstaEditor** i riferimenti descrittivi non storici rimasti in VeloxFrontend:

- `web/README.md` — sezione “InstaEditor configuration in `next.config.js`”;
- `web/CONTRIBUTING.md` — heading “# InstaEditor (separate terminal)”;
- `web/e2e/cross_repo_smoke.spec.ts` — commento “the real InstaEditor and Vite SPA shells”;
- `docs/AMISH_DEMO_CLEANUP.md` — “The InstaEditor receives project context from InstaEdit…”;
- `REFACTOR_PLAN.md` — heading “## 3. VeloxFrontend — InstaEditor (13 file)”.

Restano intenzionali: path runtime `/dark_editor_v2` e `basePath` Next.js (contratto tecnico attivo), variabili `DARK_EDITOR_*`, chiavi localStorage tecniche, test/negative-pin, CHANGELOG e documenti storici che citano la migrazione o l’audit.

## Aggiornamento 2026-08-08 — stato Definition of Done (disaccoppiamento)

Il test finale di accettazione della Definition of Done è stato eseguito con
**Velox completamente spento**: login, Groups, Channels, video, Drive e
Posting restano funzionanti (stessi codici con Velox ON e OFF) e **solo**
"Apri InstaEditor" risulta non disponibile (502 upstream editor call failed).

Durante il test è stato trovato e corretto un bug di wiring:
`LaunchTokenIssuer` non veniva propagato nel wrapper `modules_editor.go`, per
cui il launch handler non veniva mai montato e `POST /editor/launch` cadeva
nel catch-all proxy (404 falso anche con Velox attivo). Fix + test di
regressione, commit `3f3d8fa6`.

Nota preesistente non bloccante (non impatta il DoD):
`GET /api/v1/workspaces/{id}/channels` → 404 per shadowing chi tra il modulo
team e il modulo auth; la pagina Canali usa `/api/v1/accounts` e
`/api/v1/workspaces`, entrambi funzionanti.

I dettagli dell'accettazione sono registrati in
[`VELOX-FRONTEND-GROUPS-BOUNDARY.md`](./VELOX-FRONTEND-GROUPS-BOUNDARY.md)
(sezione "Esito Test A") e in
[`project-bridge-contract.md`](./project-bridge-contract.md) §12.

## Riscansione 2026-08-08 (seconda) — esito finale sui tre repo

Riscansione completa aggiornata di InstaeditLogin, `VeloxFrontend/web` e
VeloxEditiingg per la frase esatta “Dark Editor” (case-insensitive):

- **VeloxFrontend/web: 0 occorrenze** del testo “Dark Editor” (né UI, né
  commenti, né docs).
- **InstaeditLogin:** nessun riferimento visuale/UI; le sole occorrenze
  testuali erano 2 righe descrittive non storiche, **corrette ora** a
  InstaEditor: `docs/project-bridge-contract.md` §10 (“InstaEditor tests for
  opaque project handles...”) e `docs/DEPLOY.md` §4 (parentesi riordinata a
  “InstaEditor deployment, formerly Dark Editor”). Le altre occorrenze sono
  path runtime `/dark_editor_v2`, chiavi `dark_editor_*`/`dark-editor-*`,
  test negativi, migrazioni storiche immutabili e documenti di audit.
- **VeloxEditiingg:** solo riferimenti storici/migrazioni/guardie
  (`128_drop_dark_editor_domain.sql`, test di migrazione, ROADMAP/CHANGELOG/
  DEPLOY-CHECKLIST che descrivono la rimozione già avvenuta, `check-no-legacy.sh`).

Conclusione della verifica: **zero riferimenti visuali residui** in tutti e
tre i repo; ogni occorrenza rimanente appartiene alle categorie ammesse
(compatibilità runtime, test di regressione, storia documentale). Il concetto
tecnico di tema `dark` (ThemeProvider, chiavi `dark-editor-theme`, classi
CSS) non è stato toccato.

## Conclusione

Il rebranding visuale e descrittivo è concluso. Le occorrenze residue sono intenzionali: compatibilità runtime, fallback backward-compatible, test di regressione o storia documentale. Nessun riferimento legacy residuo deve essere interpretato come autorizzazione a introdurre nuovo copy “Dark Editor”.
