# Deploy frontend in produzione

Questa guida descrive il deploy reale del frontend di InstaEdit su Vercel.

Produzione:

- URL: `https://app.instaedit.org`
- Repository: `Marcuss-ops/InstaeditLogin`
- Branch di produzione: `main`
- Root Vercel: root del repository
- Workspace frontend: `web/`
- Output pubblicato: `web/dist/`

## Configurazione reale

Il deploy usa il [vercel.json](../vercel.json) nella root del repository. Non usare `web/vercel.json` per il deploy di produzione: il workflow GitHub esegue Vercel dalla root.

La configurazione esegue:

```text
npm --prefix web ci
npm --prefix web run build
pubblica web/dist/
```

Il workflow è [.github/workflows/deploy.yml](../.github/workflows/deploy.yml) e usa Vercel con `--prod`, quindi promuove il build all’alias di produzione invece di creare soltanto una preview.

## Deploy normale consigliato

Prima di pubblicare:

```bash
git status
cd web
npm ci
npm run lint
npm run build
cd ..
```

Poi committa e fai push su `main`:

```bash
git add web/src
git commit -m "feat(web): describe the frontend change"
git push origin main
```

Il flusso automatico è:

```text
push su main
  -> integration-fast
  -> deploy.yml (solo se integration-fast è success)
  -> Vercel --prod
  -> app.instaedit.org
```

Controlla i workflow:

```bash
gh run list --repo Marcuss-ops/InstaeditLogin --limit 10
gh run list --repo Marcuss-ops/InstaeditLogin --workflow deploy.yml --limit 5
```

Il deploy corretto deve avere:

- SHA del deploy uguale al commit desiderato;
- workflow `deploy` concluso con `success`;
- job `Deploy web/app to Vercel (production)` concluso con `success`.

## Deploy manuale reale in produzione

Se `integration-fast` fallisce per un test non correlato, il deploy automatico viene saltato intenzionalmente. Dopo aver verificato localmente lint e build, si può usare il manual override già previsto dal workflow:

```bash
gh workflow run deploy.yml \
  --repo Marcuss-ops/InstaeditLogin \
  --ref main
```

Recupera l’ID del run e attendilo:

```bash
gh run list \
  --repo Marcuss-ops/InstaeditLogin \
  --workflow deploy.yml \
  --limit 1

gh run watch RUN_ID \
  --repo Marcuss-ops/InstaeditLogin \
  --exit-status
```

Questo è il deploy vero perché il workflow usa `--prod`. Un comando Vercel senza `--prod` crea una preview e non aggiorna necessariamente `app.instaedit.org`.

## Secret GitHub necessari

In GitHub → Settings → Secrets and variables → Actions devono essere presenti:

```text
VERCEL_TOKEN
VERCEL_ORG_ID
VERCEL_PROJECT_ID
```

Non inserire questi valori nel repository, nei log o in questo documento. Se uno manca, il job Vercel fallisce prima della pubblicazione.

## Verifica dopo il deploy

Controlla prima la risposta HTTP:

```bash
curl -fsSIL https://app.instaedit.org/
```

Deve rispondere `HTTP/2 200` e avere `server: Vercel`.

Per verificare che il bundle sia nuovo, confronta l’hash degli asset JS restituiti dalla pagina:

```bash
curl -fsSL https://app.instaedit.org/ \
  | rg -o 'assets/[^" ]+\.js'
```

Per un controllo visuale apri una finestra anonima o forza il refresh senza cache. Vercel aggiorna il CDN, ma il browser può mantenere asset precedenti.

## Troubleshooting

### Il deploy risulta `skipped`

Controlla `integration-fast`. Il workflow di deploy automatico parte solo quando il workflow CI termina con `success`. Se il fallimento riguarda una parte non collegata al frontend, esegui prima:

```bash
npm --prefix web run lint
npm --prefix web run build
```

Poi usa il deploy manuale descritto sopra.

### Il push è su un branch diverso da `main`

Il branch diverso può generare CI o preview, ma non aggiorna la produzione. Fai merge su `main` oppure esegui il manual dispatch con `--ref main` dopo aver verificato che il commit corretto sia già su `main`.

### Vercel pubblica una preview ma il sito non cambia

Controlla che:

1. il workflow sia partito dalla root del repository;
2. venga usato il `vercel.json` root;
3. l’output sia `web/dist`;
4. gli argomenti contengano `--prod`;
5. il deploy abbia l’alias `app.instaedit.org`.

Non impostare `working-directory: ./web` nel workflow: bypasserebbe la configurazione canonica root.

### Modifiche locali non desiderate

Prima di `git add`, controlla sempre:

```bash
git status --short
git diff -- web/src
```

Aggiungi solo i file relativi alla modifica frontend. Non usare `git reset --hard` per ripulire il workspace senza aver verificato le modifiche degli altri lavori.
