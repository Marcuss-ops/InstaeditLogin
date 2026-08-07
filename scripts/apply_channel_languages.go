//go:build channellang

// Package main — offline CLI that applies the same title-based language
// detection used by the Groups UI ("Analizza lingue dai titoli") to every
// YouTube channel in the database.
//
// Mirrors scripts/import_channels_csv.go in spirit: a small, idempotent,
// env-driven DB writer for ops. It deliberately reuses the SAME marker
// table as the frontend (web/src/pages/internal/groupChannelLanguage.ts) so
// behaviour stays identical between the UI and this offline path.
//
// Operator invocation (run on the VPS host, which has Postgres on
// 127.0.0.1:5432):
//
//	# preview only (no writes):
//	go run -tags=channellang ./scripts/apply_channel_languages.go \
//	    --database-url "postgresql://USER:PASS@127.0.0.1:5432/instaedit_login?sslmode=disable"
//
//	# apply (fills only EMPTY languages):
//	go run -tags=channellang ./scripts/apply_channel_languages.go --apply \
//	    --database-url "postgresql://USER:PASS@127.0.0.1:5432/instaedit_login?sslmode=disable"
//
//	# also overwrite conflicting configured values (review the dry-run FIX rows first):
//	... --apply --fix ...
//
// Behaviour contract (identical to the UI):
//   - only accounts with a UNIQUE explicit marker are touched;
//   - `--apply` writes the detected language only when the channel has none
//     configured (SET rows);
//   - `--fix` additionally REPLACES a configured value that conflicts with
//     the title marker — mirroring the UI's confirm-and-apply overwrite
//     flow, which the operator must opt into explicitly;
//   - ambiguous or unmarked titles are never written.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type languageMarker struct {
	code    string
	pattern *regexp.Regexp
}

// Keep in sync with web/src/pages/internal/groupChannelLanguage.ts.
//
// Go's regexp (RE2) has no lookahead, so the trailing `(?=$|[\s._-])`
// boundary of the TS source is expressed as a consuming `(?:$|[\s._-])`
// alternation. Detection semantics are identical: a marker must be a whole
// token (start-of-name, or preceded by a separator) and end at a boundary.
var languageMarkers = []languageMarker{
	// Like the other 2-letter ISO codes (es$, de$, pt$…), the bare `it$`
	// suffix is accepted: the channel naming convention in this product is
	// "name + language suffix" (RapGameIt, Pop Prime IT).
	{code: "it", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:italiano|italian|italia|italy|ita|it)(?:$|[\s._-])|ita$|it$`)},
	{code: "en", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:english|inglese|england|eng)(?:$|[\s._-])|eng$`)},
	{code: "es", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:español|espanol|spanish|españa|espana|spain|castellano|es|sp)(?:$|[\s._-])|es$|sp$`)},
	{code: "fr", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:français|francais|french|france|fr|fre)(?:$|[\s._-])|fr$|fre$`)},
	{code: "de", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:deutsch|german|deutschland|germany|de)(?:$|[\s._-])|de$`)},
	{code: "pt", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:português|portugues|portuguese|brasil|brazil|pt)(?:$|[\s._-])|pt$`)},
	{code: "pl", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:polski|polish|polska|poland|pl)(?:$|[\s._-])|pl$`)},
	{code: "ru", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:русский|россия|russian|russia|ru)(?:$|[\s._-])|ru$`)},
	{code: "tr", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:türkçe|turkce|turkish|türkiye|turkiye|turkey|tr)(?:$|[\s._-])|tr$`)},
	{code: "hi", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:हिन्दी|हिंदी|hindi|भारतीय)(?:$|[\s._-])|hindi$`)},
	{code: "id", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:bahasa indonesia|indonesian|indonesia|id)(?:$|[\s._-])|id$`)},
	{code: "ar", pattern: regexp.MustCompile(`(?i)(?:^|[\s._-])(?:عربي|عربية|عرب|arabic|arab|ar)(?:$|[\s._-])|ar$`)},
}

func detectLanguage(title string) (string, bool) {
	normalized := strings.TrimSpace(title)
	if normalized == "" {
		return "", false
	}
	var candidates []string
	for _, marker := range languageMarkers {
		if marker.pattern.MatchString(normalized) {
			candidates = append(candidates, marker.code)
		}
	}
	unique := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, c := range candidates {
		if !seen[c] {
			seen[c] = true
			unique = append(unique, c)
		}
	}
	if len(unique) == 1 {
		return unique[0], true
	}
	return "", false
}

type accountRow struct {
	ID          int64
	Name        string
	CurrentLang string
	GroupName   sql.NullString
}

func main() {
	var databaseURL string
	var apply bool
	var fix bool
	flag.StringVar(&databaseURL, "database-url", os.Getenv("DATABASE_URL"), "Postgres DSN (defaults to $DATABASE_URL)")
	flag.BoolVar(&apply, "apply", false, "Write detected languages to the DB. Without it the run is a dry-run preview.")
	flag.BoolVar(&fix, "fix", false, "With --apply, also overwrite configured values that conflict with the title (FIX rows). Off by default: overwrites need explicit consent, like the UI confirm flow.")
	flag.Parse()
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "required: --database-url <dsn> OR set $DATABASE_URL")
		os.Exit(2)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		log.Error("open db", "err", err.Error())
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()
	rows, err := db.QueryContext(ctx, `
		SELECT pa.id, COALESCE(pa.username, pa.platform_user_id) AS name,
		       COALESCE(pa.metadata->>'language', '') AS lang,
		       (SELECT string_agg(g.name, ', ') FROM group_accounts ga
		          JOIN groups g ON g.id = ga.group_id
		         WHERE ga.account_id = pa.id) AS group_name
		  FROM platform_accounts pa
		 WHERE pa.platform = 'youtube'
		 ORDER BY pa.id`)
	if err != nil {
		log.Error("query accounts", "err", err.Error())
		os.Exit(1)
	}
	var accounts []accountRow
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.ID, &a.Name, &a.CurrentLang, &a.GroupName); err != nil {
			log.Error("scan", "err", err.Error())
			os.Exit(1)
		}
		accounts = append(accounts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Error("iterate", "err", err.Error())
		os.Exit(1)
	}

	var setCount, fixCount, okCount, skipCount int
	var fixBlocked int
	type planned struct {
		row      accountRow
		detected string
	}
	var changes []planned
	var fixSkipped []planned

	for _, a := range accounts {
		detected, confident := detectLanguage(a.Name)
		current := strings.TrimSpace(a.CurrentLang)
		switch {
		case !confident:
			skipCount++
		case current == "" || current == detected:
			if current == detected {
				okCount++
			} else {
				setCount++
				changes = append(changes, planned{row: a, detected: detected})
			}
		default:
			fixCount++
			if fix {
				changes = append(changes, planned{row: a, detected: detected})
			} else {
				fixBlocked++
				fixSkipped = append(fixSkipped, planned{row: a, detected: detected})
			}
		}
	}

	mode := "dry-run"
	if apply {
		mode = "apply"
	}
	fmt.Printf("\n=== ANALISI LINGUE CANALI (modalità: %s) ===\n", mode)
	fmt.Printf("canali analizzati: %d | da impostare: %d | da correggere: %d%s | gia' ok: %d | senza segnale: %d\n\n",
		len(accounts), setCount, fixCount, func() string {
			if fixBlocked > 0 {
				return fmt.Sprintf(" (di cui %d bloccate: servono --fix)", fixBlocked)
			}
			return ""
		}(), okCount, skipCount)

	for _, ch := range changes {
		group := ""
		if ch.row.GroupName.Valid && ch.row.GroupName.String != "" {
			group = " [" + ch.row.GroupName.String + "]"
		}
		if ch.row.CurrentLang == "" {
			fmt.Printf("  SET   #%d  %-26s → %s%s\n", ch.row.ID, ch.row.Name, ch.detected, group)
		} else {
			fmt.Printf("  FIX   #%d  %-26s %s → %s%s\n", ch.row.ID, ch.row.Name, ch.row.CurrentLang, ch.detected, group)
		}
	}
	for _, ch := range fixSkipped {
		group := ""
		if ch.row.GroupName.Valid && ch.row.GroupName.String != "" {
			group = " [" + ch.row.GroupName.String + "]"
		}
		fmt.Printf("  FIX-BLOCCATA #%d  %-26s %s → %s%s   (aggiungi --fix per applicare)\n", ch.row.ID, ch.row.Name, ch.row.CurrentLang, ch.detected, group)
	}

	if !apply {
		fmt.Println("\nDry-run: nessuna modifica scritta. Rilancia con --apply per applicare (e --fix per le sovrascritture).")
		return
	}
	if fixBlocked > 0 {
		fmt.Printf("\nAttenzione: %d FIX bloccate senza --fix. Rilancia con --fix se vuoi sovrascrivere anche le lingue configurate.", fixBlocked)
	}
	if len(changes) == 0 {
		fmt.Println("\nNessuna modifica da applicare.")
		return
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Error("begin tx", "err", err.Error())
		os.Exit(1)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		UPDATE platform_accounts
		   SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('language', $1::text)
		 WHERE id = $2`)
	if err != nil {
		log.Error("prepare", "err", err.Error())
		os.Exit(1)
	}
	for _, ch := range changes {
		if _, err := stmt.ExecContext(ctx, ch.detected, ch.row.ID); err != nil {
			log.Error("update", "id", ch.row.ID, "err", err.Error())
			os.Exit(1)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Error("commit", "err", err.Error())
		os.Exit(1)
	}
	fmt.Printf("\nApplicate %d lingue (SET+FIX) in un'unica transazione.\n", len(changes))
}
