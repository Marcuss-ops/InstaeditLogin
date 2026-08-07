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

## Conclusione

Il rebranding visuale e descrittivo è concluso. Le occorrenze residue sono intenzionali: compatibilità runtime, fallback backward-compatible, test di regressione o storia documentale. Nessun riferimento legacy residuo deve essere interpretato come autorizzazione a introdurre nuovo copy “Dark Editor”.
