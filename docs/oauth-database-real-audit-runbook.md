# Audit operativo del database reale OAuth

Questa guida descrive come certificare in modo **read-only** il database
PostgreSQL reale prima di collegare o ampliare i canali YouTube di produzione.
La procedura verifica identità dell'installazione, migrazioni OAuth `084/085`,
unicità dei token per grant, presenza del refresh token cifrato e coerenza
tra `oauth_connections` e `platform_accounts`.

> **Ambito:** questa procedura diagnostica non ripara dati, non applica
> migrazioni, non revoca OAuth e non modifica account o token. Un risultato
> positivo certifica soltanto gli invarianti verificati dagli script.

## 1. Prerequisiti

Eseguire i comandi dalla root del repository, preferibilmente da una macchina
operator autorizzata o dalla directory di deploy che contiene lo stesso
checkout delle migrazioni applicate al database.

Sono necessari:

- Bash e Python 3;
- `psql` compatibile con PostgreSQL;
- accesso di rete al database reale;
- un ruolo PostgreSQL con permessi di sola lettura sulle tabelle applicative e
  sui cataloghi PostgreSQL necessari al controllo degli indici;
- `DATABASE_URL` del database corretto;
- `EXPECTED_DATABASE_INSTALLATION_UUID`, conservato nel secret manager e
  corrispondente all'installazione attesa;
- TLS PostgreSQL configurato secondo la policy dell'ambiente, preferibilmente
  `sslmode=verify-full` con CA verificabile;
- il checkout locale con le migrazioni `084` e `085` presenti.

Controllare gli strumenti senza stampare variabili d'ambiente:

```bash
command -v bash python3 psql
psql --version
[[ -f internal/database/migrations/084_oauth_subject_shared_connections.sql ]]
[[ -f internal/database/migrations/085_grant_scoped_tokens.sql ]]
```

Prima dell'audit verificare inoltre che il backup automatico sia attivo e che
l'ultimo restore drill sia stato completato. L'audit è read-only, ma un backup
verificato è un prerequisito operativo indipendente per ogni attività su dati
reali.

## 2. Regole di sicurezza obbligatorie

1. **Non inserire password nella riga di comando.** Non usare un URL come
   `postgres://user:password@host/db` nei process arguments, nella shell
   history, nei ticket o nei log.
2. Per i comandi `psql` diretti, preferire un URL senza password e un file
   `.pgpass` protetto:

   ```bash
   chmod 600 "$HOME/.pgpass-instaedit"
   ```

   I wrapper `oauth-preflight-check.sh` e
   `installation-identity-diagnostic.sh` sono un caso distinto: ricevono
   `DATABASE_URL` dall'ambiente e, se contiene una password, la spostano in un
   file temporaneo `0600` prima di invocare `psql`. La password deve quindi
   essere iniettata dal secret manager o da un ambiente protetto, mai scritta
   letteralmente nella shell command line.
3. Usare `-w` per impedire prompt interattivi e `-v ON_ERROR_STOP=1` per non
   confondere un errore SQL con un risultato vuoto.
4. Non eseguire `go run ./cmd/migrate`, `make run-migrate`, `UPDATE`, `DELETE`,
   `TRUNCATE`, `VACUUM FULL` o altri comandi di manutenzione durante questa
   procedura.
5. Non selezionare, copiare o incollare token, ciphertext, email, username o
   identificativi provider. I diagnostici inclusi sono progettati per non
   restituirli.
6. Salvare soltanto esiti aggregati e motivi tecnici. Se è necessario
   conservare l'output, proteggerlo come materiale operativo riservato e
   rimuoverlo secondo la retention policy.
7. Confermare due volte host, database e installazione attesa prima di
   eseguire il controllo. Un `MATCH` su un database sbagliato è comunque un
   falso senso di sicurezza.

## 3. Preparazione della connessione

Configurare l'ambiente in una sessione protetta. Non stampare il valore delle
variabili con `env`, `set`, `echo` o `ps`.

Per i comandi `psql` diretti, predisporre `PGPASSFILE` tramite il secret
manager:

```bash
export DATABASE_URL='postgresql://db-user@db-host:5432/instaedit?sslmode=verify-full'
export EXPECTED_DATABASE_INSTALLATION_UUID='00000000-0000-4000-8000-000000000000'
export PGPASSFILE="$HOME/.pgpass-instaedit"
chmod 600 "$PGPASSFILE"
```

Il valore UUID sopra è un segnaposto: usare quello reale fornito dal secret
manager, senza scriverlo nella documentazione o nei log.

Per i due wrapper sicuri, il secret manager deve iniettare `DATABASE_URL`
come variabile d'ambiente **con la password già presente nell'URL**, senza che
l'operatore la digiti o la esponga nella history. Il wrapper rimuove
internamente la password dal proprio URL prima di chiamare `psql` e usa un
`.pgpass` temporaneo `0600`.

I wrapper non ereditano il `PGPASSFILE` esterno: se ricevono un URL
password-free e un `.pgpass` esterno, sostituiscono comunque `PGPASSFILE` con
il proprio file temporaneo vuoto e la connessione fallisce. Per un flusso
password-free con `.pgpass` usare i comandi `psql` diretti delle sezioni
successive, non questi due wrapper.

```bash
# Esempio concettuale: il secret manager valorizza DATABASE_URL con la
# password; il valore non viene digitato, stampato o salvato nella history.
./scripts/db/oauth-preflight-check.sh
./scripts/db/installation-identity-diagnostic.sh
```

Verificare la connettività senza esporre dati applicativi:

```bash
psql "$DATABASE_URL" -X -q -w -v ON_ERROR_STOP=1 \
  -c 'SELECT current_database(), current_user, current_setting('\''server_version'\'');'
```

Il risultato deve riferirsi al database e al ruolo attesi. Se la policy
proibisce di mostrare anche questi metadati, eseguire il comando e verificare
soltanto il codice di uscita:

```bash
psql "$DATABASE_URL" -X -q -w -v ON_ERROR_STOP=1 \
  -c 'SELECT 1;' >/dev/null
```

## 4. Sequenza certificativa

Eseguire i controlli nell'ordine seguente. Fermarsi al primo errore e non
procedere al collegamento dei canali finché la causa non è stata analizzata.

### 4.1 Preflight unico raccomandato

Il wrapper prepara in sicurezza l'eventuale password presente nel
`DATABASE_URL` in un file temporaneo `0600`, passa a `psql` un URL senza
password e cancella i file temporanei in uscita. Il valore deve essere stato
iniettato nell'ambiente da un secret manager o da una sessione protetta:

```bash
./scripts/db/oauth-preflight-check.sh
```

Gli stessi valori possono essere passati tramite flag, ma le variabili
ambiente sono preferibili perché gli argomenti possono finire nella shell
history:

```bash
./scripts/db/oauth-preflight-check.sh \
  --url "$DATABASE_URL" \
  --expected-installation-uuid "$EXPECTED_DATABASE_INSTALLATION_UUID"
```

Risultato atteso: tutte le righe `✓` e infine:

```text
✓ OAuth DB preflight passed (read-only)
```

Codici di uscita del preflight:

- `0` — tutti gli invarianti sono superati;
- `1` — configurazione, tool, connessione, schema o query non disponibili;
- `2` — argomento CLI non valido;
- `3` — un invariante del database è fallito.

Il preflight controlla anche i checksum delle migrazioni locali. Perciò deve
essere eseguito da un checkout coerente con il codice che si intende
certificare, non da una copia modificata o da una directory arbitraria.

### 4.2 Verifica dettagliata delle migrazioni 084/085

Se serve un report di dettaglio, eseguire la query read-only:

```bash
psql "$DATABASE_URL" -X -q -w -v ON_ERROR_STOP=1 \
  -f scripts/db/oauth-migrations-084-085-diagnostic.sql
```

Risultati attesi:

- due righe `migration_status` con stato `APPLIED`;
- `migration_summary` con `PASS`, `expected_count=2` e
  `observed_count=2`;
- cinque righe `index_status` con `PASS`;
- `index_summary` con `PASS`, `expected_count=5` e
  `observed_count=5`.

`MISSING`, `CHECKSUM_MISMATCH` o `FAIL` richiedono stop operativo. Non
modificare manualmente `schema_migrations` e non ricalcolare checksum per
forzare un risultato positivo.

### 4.3 Identità dell'installazione

Il controllo stampa soltanto `MATCH`, `MISMATCH` o `MISSING`. Usa il
`DATABASE_URL` iniettato nell'ambiente e gestisce autonomamente il file
temporaneo delle credenziali:

```bash
./scripts/db/installation-identity-diagnostic.sh
```

Risultato atteso:

```text
MATCH
```

`MISMATCH` o `MISSING` terminano con codice `3`. Non usare quel database per
l'audit OAuth finché l'identità non è stata verificata dal responsabile del
sistema. Il diagnostico non stampa né l'UUID atteso né quello letto dal DB.

### 4.4 Duplicati token per grant e tipo

```bash
psql "$DATABASE_URL" -X -q -w -v ON_ERROR_STOP=1 \
  -f scripts/db/duplicate-token-diagnostic.sql
```

Risultato atteso: **zero righe**. L'output, se presente, contiene soltanto
`oauth_connection_id`, `token_type` e `token_row_count`. Un
`oauth_connection_id` nullo rappresenta un token orfano/non associato, non un
grant valido.

### 4.5 Bearer senza refresh token non vuoto

```bash
psql "$DATABASE_URL" -X -q -w -v ON_ERROR_STOP=1 \
  -f scripts/db/bearer-without-refresh-diagnostic.sql
```

Risultato atteso: **zero righe**. La query controlla solo se la lunghezza del
campo cifrato è nulla o zero; non seleziona né decifra il ciphertext. Ogni
riga con `active_platform_account_count > 0` è un blocco prioritario prima di
collegare o pubblicare con quel grant.

### 4.6 Coerenza tra grant e platform account

```bash
psql "$DATABASE_URL" -X -q -w -v ON_ERROR_STOP=1 \
  -f scripts/db/oauth-account-status-consistency-diagnostic.sql
```

Risultato atteso: **zero righe**. Le anomalie classificate includono grant
mancante, owner/provider non coerente, account attivo con grant non attivo,
account disconnesso con grant ancora attivo e propagazione incompleta di
`reauth_required`.

### 4.7 Presenza aggregata delle credenziali cifrate

Solo se serve un report complementare sui singoli record token, eseguire:

```bash
psql "$DATABASE_URL" -X -q -w -v ON_ERROR_STOP=1 \
  -f scripts/db/token-presence-diagnostic.sql
```

Questo diagnostico **non restituisce token o ciphertext**: applica
`octet_length(...)` e restituisce esclusivamente booleani di presenza,
scadenze, conteggi di scope, stati e timestamp tecnici. Non aggiungere mai
le colonne `encrypted_*` alla proiezione SQL e non salvare l'output integrale
fuori dal sistema operativo autorizzato. Il controllo è informativo; il
risultato certificativo atteso resta quello del preflight e dei diagnostici
precedenti.

## 5. Tabella di esito

| Controllo | PASS atteso | Azione se fallisce |
| --- | --- | --- |
| Connettività/TLS | connessione riuscita | fermare; verificare host, CA, ruolo e rete |
| Migrazioni 084/085 | `APPLIED`, checksum coerenti | fermare; confrontare checkout e `schema_migrations` |
| Indici 084/085 | 5/5 `PASS` | fermare; non applicare fix manuali in produzione |
| Installation UUID | `MATCH` | fermare; possibile DB/ambiente errato |
| Token duplicati | zero righe | fermare; preservare evidenza tecnica e aprire incidente |
| Bearer senza refresh | zero righe | fermare per account/grant colpiti; richiedere reauth o remediation approvata |
| Stato grant/account | zero righe | fermare; correggere il flusso applicativo/dati con procedura autorizzata |

Un singolo fallimento rende il risultato complessivo **NON CERTIFICATO**.
Non ignorare un fallimento perché il Vault blocca già l'operazione: il Vault
può garantire fail-closed, ma non rende coerenti dashboard e database.

## 6. Troubleshooting sicuro

### `DATABASE_URL is required` / UUID non valido

Non stampare il valore. Verificare nel secret manager che la variabile sia
iniettata nella sessione e che l'UUID sia nella forma UUID standard. Evitare
flag con valori sensibili quando è sufficiente l'ambiente.

### `psql is required` o errore di connessione

Installare/usare il client approvato, verificare DNS, firewall, CA e
`sslmode`. Non abbassare TLS a `disable` per aggirare il problema. Controllare
che `.pgpass` sia `0600` e che la riga corrisponda a host, porta, database e
utente effettivamente usati.

### `required OAuth schema is incomplete`

Confrontare solo i nomi di tabelle/colonne/indici riportati dal preflight con
la versione applicata. Non stampare cataloghi completi e non eseguire
migrazioni automaticamente durante l'audit.

### `MISSING` o `CHECKSUM_MISMATCH` sulle migrazioni

Confrontare i checksum del checkout con quelli registrati dal migration
runner. Un checksum diverso può indicare una migrazione modificata dopo
l'applicazione o un checkout non allineato; richiede revisione, non una
sovrascrittura della tabella di tracking.

### UUID `MISMATCH`

Fermarsi immediatamente e verificare di non essere connessi a staging,
restore, database vecchio o installazione diversa. Non sostituire il valore
atteso per far passare il controllo senza approvazione esplicita.

### Righe nelle query token/account

Conservare solo il numero di righe, gli ID tecnici strettamente necessari e il
motivo classificato, secondo la policy di accesso. Non eseguire query ad hoc
aggiungendo `encrypted_token`, `encrypted_refresh_token`, username, email o
resource ID provider. L'analisi/remediation deve usare il repository e i
flussi applicativi approvati.

## 7. Test locali degli strumenti (senza database reale)

Prima dell'esecuzione reale, verificare che gli script e i test privacy del
checkout siano integri:

```bash
bash -n scripts/db/oauth-preflight-check.sh \
        scripts/db/installation-identity-diagnostic.sh

./scripts/db/test-oauth-preflight-check.sh
./scripts/db/test-installation-identity-diagnostic.sh
./scripts/db/test-oauth-migrations-084-085-diagnostic.sh
./scripts/db/test-duplicate-token-diagnostic.sh
./scripts/db/test-bearer-without-refresh-diagnostic.sh
./scripts/db/test-oauth-account-status-consistency-diagnostic.sh
./scripts/db/test-token-presence-diagnostic.sh
```

Questi test sono statici/mockati e non certificano il database reale. La loro
riuscita è un prerequisito della procedura, non il suo risultato finale.

## 8. Chiusura e registrazione dell'audit

Registrare in un sistema operativo protetto:

- timestamp e operatore;
- commit/checkout usato per eseguire l'audit;
- ambiente dichiarato e codice di uscita del preflight;
- esito PASS/FAIL per ogni controllo;
- conteggi aggregati e motivi, senza credenziali o valori sensibili;
- eventuali ticket di follow-up.

Non registrare `DATABASE_URL`, password, `.pgpass`, token, ciphertext, output
integrale contenente identificativi non necessari o UUID in chiaro.

L'audit è certificativo soltanto quando tutti i controlli risultano verdi e
l'operatore ha verificato anche backup/restore, chiavi di cifratura e
configurazione OAuth/redirect URI fuori dal perimetro SQL.
