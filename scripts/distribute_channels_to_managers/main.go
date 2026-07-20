// Command distribute_channels_to_managers splits a 200-channel
// inventory CSV into per-manager CSVs sized for the YouTube 2026
// refresh-token / channel-count caps, so each manager can run their
// own OAuth dance without sharing a single (Google Account, OAuth
// client) pair past 50 active refresh tokens.
//
// Inputs are read from a flat CSV with the three required columns:
//
//	channel_id            UC... (the platform_user_id)
//	channel_name          display name (informational)
//	manager_email_hint    the manager's Google Workspace email that
//	                      will own the OAuth grant. Missing emails
//	                      fall under the "_unassigned" bucket.
//
// Outputs are per-bucket CSV files named
//
//	manager_<slug>.csv                  (≤ cap rows, default 50)
//	manager_<slug>_part2.csv            (2nd chunk of overflow)
//	manager_<slug>_partN.csv            (Nth chunk of overflow)
//
// Output CSVs include the full `internal/channelimport.CSVHeaderColumns`
// header so an operator can pipe the per-manager CSVs straight
// back into `POST /admin/channels/import-csv` once the OAuth dance
// is complete — the operator workflow is round-robin distribution
// → manager-by-manager OAuth → re-import via the canonical path.
//
// The bucket assignment is DETERMINISTIC over sorted manager emails
// and stable across runs. The cap is enforced strictly: a single
// manager whose rows exceed the cap produces multiple _partN files
// (cap+1 → _part1+_part2, cap*2+1 → _part1+_part2+_part3, etc.),
// never a single oversized file. This matches the YouTube 2026
// guidance in `docs/OAUTH-PRODUCTION.md` Step 8 ("Distribute the
// 200 channels").
//
// NO tokens, NO DB writes, NO network calls. Pure stdlib CLI for
// the offline operator workflow. The script's only side effect is
// writing inside `-output-dir`.
//
// Usage:
//
//	go run ./scripts/distribute_channels_to_managers \
//	    -input inventory.csv \
//	    [-output-dir ./out/managers] \
//	    [-buckets 4] \
//	    [-cap 50]
package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	capDefault          = 50
	bucketsDefault      = 4
	outputDirDefault    = "./out/managers"
	maxBucketIndexLimit = 64 // safety so a runaway config cannot gen 100k files
)

// outputCSVColumns is the canonical CSV-to-import header — the
// output files here are drop-in compatible with
// `internal/channelimport.Parse` so the operator can re-import the
// per-manager CSVs after each manager's OAuth dance. Empty fields
// for the un-populated columns are intentional — the import path
// tolerates blanks same as current operator convention.
var outputCSVColumns = []string{
	"channel_id",
	"channel_name",
	"manager_email_hint",
	"workspace",
	"group",
	"language",
	"timezone",
	"expected_upload_frequency",
}

func main() {
	var (
		flInput     = flag.String("input", "", "input inventory CSV path (required). Header row MUST contain at minimum: channel_id, channel_name, manager_email_hint.")
		flOutputDir = flag.String("output-dir", outputDirDefault, "output directory for per-manager CSVs")
		flBuckets   = flag.Int("buckets", bucketsDefault, "target bucket count (default 4 — matches the 4-5 manager rollout in docs/OAUTH-PRODUCTION.md Step 8)")
		flCap       = flag.Int("cap", capDefault, "hard cap per output file (default 50 — YouTube 2026 silent-invalidation per (Google Account, OAuth client) pair)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "distribute_channels_to_managers — split a YouTube channel inventory CSV into ≤%d-channel per-manager CSVs for the 2026 multi-manager OAuth rollout.\n\n", capDefault)
		fmt.Fprintf(os.Stderr, "Usage:\n  go run ./scripts/distribute_channels_to_managers -input inventory.csv [-output-dir DIR] [-buckets N] [-cap N]\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nSee docs/OAUTH-PRODUCTION.md 'Operator Workflow for 200-Channel Rollout' for the full operator runbook.\n")
	}
	flag.Parse()

	if *flInput == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *flBuckets < 1 {
		fmt.Fprintln(os.Stderr, "ERROR: -buckets must be >= 1")
		os.Exit(2)
	}
	if *flCap < 1 {
		fmt.Fprintln(os.Stderr, "ERROR: -cap must be >= 1")
		os.Exit(2)
	}

	rows, err := readInventoryCSV(*flInput)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: read input: "+err.Error())
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: input CSV had a header row but no data rows")
		os.Exit(1)
	}

	files, err := distributeRows(rows, *flBuckets, *flCap)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: distribute: "+err.Error())
		os.Exit(1)
	}

	if err := os.MkdirAll(*flOutputDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: mkdir output-dir: "+err.Error())
		os.Exit(1)
	}

	printSummary(files, len(rows), *flBuckets, *flCap)

	if err := writeOutputFiles(*flOutputDir, files); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: write output: "+err.Error())
		os.Exit(1)
	}
}

// inputRow is the parsed + normalised record for one channel from
// the inventory CSV. Fields are pre-trimmed; rowNum is 1-based and
// counts the header row so error messages are easy to map back.
type inputRow struct {
	channelID        string
	channelName      string
	managerEmailHint string
	rowNum           int
}

// bucketFile is a single output file: a (bucket, manager, partN)
// group of <cap rows. fileIndex==1 denotes the unsuffixed manager
// CSV; >1 denotes _partN overflow files for managers with >cap rows.
type bucketFile struct {
	bucketIndex  int
	fileIndex    int
	managerEmail string
	rows         []inputRow
}

// readInventoryCSV opens path, validates the header row, and parses
// the data rows. Required columns: channel_id, channel_name,
// manager_email_hint. Extra columns (forward compat) are tolerated
// but ignored. Missing channel_id on a data row is a hard error
// (operator-invisible-mistake guard).
func readInventoryCSV(path string) ([]inputRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	const wantChannelID = "channel_id"
	const wantChannelName = "channel_name"
	const wantManagerEmailHint = "manager_email_hint"

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv parse: %w", err)
	}
	if len(records) == 0 {
		return nil, errors.New("empty CSV (no header row)")
	}

	header := records[0]
	required := []string{wantChannelID, wantChannelName, wantManagerEmailHint}
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.TrimSpace(strings.ToLower(h))] = i
	}
	for _, want := range required {
		if _, ok := idx[want]; !ok {
			return nil, fmt.Errorf("missing required header column %q (got header row: %v)", want, header)
		}
	}

	rows := make([]inputRow, 0, len(records)-1)
	for i, rec := range records[1:] {
		padded := make([]string, len(header))
		copy(padded, rec)
		channelID := strings.TrimSpace(padded[idx[wantChannelID]])
		if channelID == "" {
			return nil, fmt.Errorf("row %d: channel_id is required and was empty", i+2)
		}
		rows = append(rows, inputRow{
			channelID:        channelID,
			channelName:      strings.TrimSpace(padded[idx[wantChannelName]]),
			managerEmailHint: strings.TrimSpace(padded[idx[wantManagerEmailHint]]),
			rowNum:           i + 2,
		})
	}
	return rows, nil
}

// distributeRows assigns every input row to exactly one bucketFile
// and returns the slice in deterministic order: SORTED manager
// email ASC, with fileIndex ASC as a stable tiebreak. This is the
// order the operator-facing summary and output file listing use
// so all of one manager's overflow files appear together.
//
// Bucket assignment is round-robin over SORTED unique manager
// emails (manager i is assigned to bucket i % buckets) so the
// bucket annotation is stable across runs regardless of input
// ordering. bucketIndex is preserved on each bucketFile as an
// informational annotation -- the sorter below intentionally does
// NOT use bucketIndex as a comparator (operator reads the summary
// by manager, not by bucket).
//
// For a single manager whose row count exceeds cap, rows are
// chunked sequentially into (manager_<slug>.csv,
// manager_<slug>_part2.csv, manager_<slug>_partN.csv). This keeps
// the per-file size ≤cap which is the literal contract the refresh-
// token / channel-count caps care about — distributing a 200-channel
// manager across 4 buckets would defeat the 1-OAuth-grant-per-1-
// manager policy.
func distributeRows(rows []inputRow, buckets, cap int) ([]bucketFile, error) {
	if buckets < 1 || buckets > maxBucketIndexLimit {
		return nil, fmt.Errorf("buckets=%d out of range [1,%d]", buckets, maxBucketIndexLimit)
	}
	if cap < 1 {
		return nil, fmt.Errorf("cap=%d must be >= 1", cap)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	managers, byManager := groupByManager(rows)
	managerToBucket := make(map[string]int, len(managers))
	for i, mgr := range managers {
		managerToBucket[mgr] = i % buckets
	}

	files := make([]bucketFile, 0, len(managers))
	for _, mgr := range managers {
		mgrRows := byManager[mgr]
		bucketIdx := managerToBucket[mgr]
		// Sequential chunking; rows are already in input-row order.
		for part, i := 0, 0; i < len(mgrRows); i += cap {
			end := i + cap
			if end > len(mgrRows) {
				end = len(mgrRows)
			}
			part++
			files = append(files, bucketFile{
				bucketIndex:  bucketIdx,
				fileIndex:    part,
				managerEmail: mgr,
				rows:         append([]inputRow(nil), mgrRows[i:end]...),
			})
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].managerEmail != files[j].managerEmail {
			return files[i].managerEmail < files[j].managerEmail
		}
		return files[i].fileIndex < files[j].fileIndex
	})
	// bucketIndex is intentional as a stable internal annotation; the
	// PRIMARY sort key is manager email so the operator-facing
	// summary groups all of one manager's overflow files together.
	return files, nil
}

// groupByManager returns the SORTED unique manager emails and a
// map of manager → rows preserving input order within each group.
// Empty manager_email_hint values fall under "_unassigned" so the
// operators never see an empty bucket label.
func groupByManager(rows []inputRow) ([]string, map[string][]inputRow) {
	managers := make(map[string][]inputRow)
	for _, r := range rows {
		mgr := r.managerEmailHint
		if mgr == "" {
			mgr = "_unassigned"
		}
		managers[mgr] = append(managers[mgr], r)
	}
	keys := make([]string, 0, len(managers))
	for k := range managers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, managers
}

// slugify turns a manager email into a file-system safe slug.
// Lowercases, replaces non [a-z0-9_-] with underscores, caps at 48
// chars. Empty input → "_unassigned" (defensive duplicate of the
// groupByManager convention).
func slugify(s string) string {
	if s == "" {
		return "_unassigned"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 48 {
		out = out[:48]
	}
	if out == "" {
		return "_unassigned"
	}
	return out
}

// fileName returns the deterministic output CSV name for a given
// bucketFile. fileIndex==1 produces the unsuffixed manager CSV;
// >1 produces _partN suffixed overflows.
func fileName(f bucketFile) string {
	slug := slugify(f.managerEmail)
	if f.fileIndex <= 1 {
		return fmt.Sprintf("manager_%s.csv", slug)
	}
	return fmt.Sprintf("manager_%s_part%d.csv", slug, f.fileIndex)
}

// printSummary writes a per-file summary plus an aggregate trailer
// to stdout. Per-file summary includes bucket index, channel count,
// manager email, first/last channel_ids, and the output path.
// Aggregate trailer shows totals + a warning if any file violated
// the cap.
func printSummary(files []bucketFile, inputCount, buckets, cap int) {
	fmt.Println("=== Distribute channels to managers ===")
	fmt.Printf("Input rows:           %d\n", inputCount)
	fmt.Printf("Buckets configured:   %d\n", buckets)
	fmt.Printf("Cap (per output):     %d\n", cap)
	fmt.Printf("Output files:         %d\n\n", len(files))

	for _, f := range files {
		first := ""
		last := ""
		if len(f.rows) > 0 {
			first = f.rows[0].channelID
			last = f.rows[len(f.rows)-1].channelID
		}
		fmt.Printf("Bucket %d | %3d channels | manager=%-32s | %s .. %s | %s\n",
			f.bucketIndex, len(f.rows), f.managerEmail, first, last, fileName(f))
	}

	// Warnings: anything over the cap (defensive — distributeRows
	// enforces the cap, so this never fires today; kept in for
	// future per-file-size policy changes).
	overCap := 0
	for _, f := range files {
		if len(f.rows) > cap {
			fmt.Fprintf(os.Stderr, "WARN: file %s exceeds cap (%d > %d)\n", fileName(f), len(f.rows), cap)
			overCap++
		}
	}
	if overCap > 0 {
		fmt.Fprintf(os.Stderr, "Total over-cap files: %d (review the cap policy)\n", overCap)
	}
}

// writeOutputFiles creates one CSV per bucketFile in dir, with the
// canonical outputColumns header so the result feeds straight
// into channelimport.Parse. The trailing "Written N files" block
// is the canonical operator-facing artifact: it lists, in the same
// deterministic order as the summary, exactly which files were
// created.
func writeOutputFiles(dir string, files []bucketFile) error {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		fullPath := filepath.Join(dir, fileName(f))
		out, err := os.Create(fullPath)
		if err != nil {
			return fmt.Errorf("create %s: %w", fullPath, err)
		}
		w := csv.NewWriter(out)
		if err := w.Write(outputCSVColumns); err != nil {
			_ = out.Close()
			return fmt.Errorf("write header %s: %w", fullPath, err)
		}
		// channel_id, channel_name, manager_email_hint, workspace, group, language, timezone, expected_upload_frequency
		rec := make([]string, len(outputCSVColumns))
		rec[0] = "" // sentinel to ensure len
		for _, r := range f.rows {
			// zeros out then sets indexes 0..2
			for i := range rec {
				rec[i] = ""
			}
			rec[0] = r.channelID
			rec[1] = r.channelName
			rec[2] = r.managerEmailHint
			if err := w.Write(rec); err != nil {
				_ = out.Close()
				return fmt.Errorf("write row %d %s: %w", r.rowNum, fullPath, err)
			}
		}
		w.Flush()
		if err := w.Error(); err != nil {
			_ = out.Close()
			return fmt.Errorf("flush %s: %w", fullPath, err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("close %s: %w", fullPath, err)
		}
		paths = append(paths, fullPath)
	}
	fmt.Println()
	fmt.Println("=== Wrote " + strconv.Itoa(len(paths)) + " files ===")
	for _, p := range paths {
		fmt.Println("  " + p)
	}
	return nil
}
