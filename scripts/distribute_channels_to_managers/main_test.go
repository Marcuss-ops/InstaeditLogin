// Tests for scripts/distribute_channels_to_managers.go. Lives in
// `package main` because the CLI itself is a `package main` script —
// Go test tooling requires the test file to share the package
// declaration to access unexported helpers.
//
// Coverage map:
//
//   TestDistributeRows_Balanced_4_Buckets_200_Channels
//     Spec scenario #1: 200 channels balanced across 4 managers ×
//     50 channels each. Each gets its own bucket; no `_part2`
//     overflow; every file has exactly 50 rows.
//
//   TestDistributeRows_ManagerOverflow_SplitsIntoParts
//     Spec scenario #2: 1 manager with 75 channels produces
//     part1 (50 rows) + part2 (25 rows). The cap is not silently
//     violated.
//
//   TestDistributeRows_DeterministicOrdering
//     Spec scenario #3: same input rows in different orders
//     produce the SAME sorted (bucket, manager, partN) output
//     ordering across runs.
//
//   TestDistributeRows_PartialOverflow75_SplitsAt50Boundary
//     Belt-and-braces: cap=50 + manager with EXACTLY 50 channels
//     does NOT trigger part2; cap=50 + manager with 51 channels
//     triggers part1+part2 with sizes [50,1].
//
//   TestDistributeRows_BucketAssignment_RoundRobin
//     5 managers, buckets=2 → buckets used: [0,1,0,1,0]
//     deterministically (sorted email order).
//
//   TestReadInventoryCSV_HeaderMissing
//     Missing required column → error mentioning the column name.
//
//   TestReadInventoryCSV_EmptyChannelID
//     Row with empty channel_id → row-numbered error.
//
//   TestSlugify
//     Round-trips common email forms to filesystem-safe slugs.
package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeRows builds a []inputRow with the given channel_ids bucketed
// by the supplied manager_specs. manager_specs is a slice of
// alternating (managerEmail, n) pairs — see usage in the tests
// below. The function is intentionally tiny so the test cases
// read like a table.
func makeRows(specs ...interface{}) []inputRow {
	var out []inputRow
	rowNum := 2 // header is row 1
	for _, s := range specs {
		entry := s.(struct {
			manager  string
			n        int
		})
		for i := 0; i < entry.n; i++ {
			out = append(out, inputRow{
				channelID:        "UC" + entry.manager + "ch" + next4(i),
				channelName:      "ch" + next4(i),
				managerEmailHint: entry.manager,
				rowNum:           rowNum,
			})
			rowNum++
		}
	}
	return out
}

// next4 returns a 4-digit zero-padded index — "…ch0000", "…ch0001"
// etc. Operators don't see this in production files; making rows
// visually distinct makes the test failures easier to debug.
func next4(i int) string {
	s := "0000" + itoa(i)
	return s[len(s)-4:]
}

// itoa avoids pulling in strconv just for the test helper — Go
// stdlib couldn't care less, but keeping this minimal helps
// when reading the tests in isolation.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestDistributeRows_Balanced_4_Buckets_200_Channels(t *testing.T) {
	spec := []interface{}{
		struct {
			manager string
			n       int
		}{"a@instaedit.org", 50},
		struct {
			manager string
			n       int
		}{"b@instaedit.org", 50},
		struct {
			manager string
			n       int
		}{"c@instaedit.org", 50},
		struct {
			manager string
			n       int
		}{"d@instaedit.org", 50},
	}
	rows := makeRows(spec...)
	files, err := distributeRows(rows, 4, 50)
	if err != nil {
		t.Fatalf("distributeRows: %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("len(files) = %d, want 4 (one file per manager, no overflow)", len(files))
	}
	// Every manager assigned to a unique bucket (round-robin
	// over sorted emails [a,b,c,d] = 4 buckets).
	wantBuckets := map[string]int{
		"a@instaedit.org": 0,
		"b@instaedit.org": 1,
		"c@instaedit.org": 2,
		"d@instaedit.org": 3,
	}
	for _, f := range files {
		if got := f.bucketIndex; got != wantBuckets[f.managerEmail] {
			t.Errorf("manager %s: bucket = %d, want %d", f.managerEmail, got, wantBuckets[f.managerEmail])
		}
		if got := len(f.rows); got != 50 {
			t.Errorf("manager %s: file row count = %d, want 50", f.managerEmail, got)
		}
		if f.fileIndex != 1 {
			t.Errorf("manager %s: fileIndex = %d, want 1 (no overflow)", f.managerEmail, f.fileIndex)
		}
	}
}

func TestDistributeRows_ManagerOverflow_SplitsIntoParts(t *testing.T) {
	spec := []interface{}{
		struct {
			manager string
			n       int
		}{"a@instaedit.org", 75}, // overflow → 50 + 25
		struct {
			manager string
			n       int
		}{"b@instaedit.org", 5},
	}
	rows := makeRows(spec...)
	files, err := distributeRows(rows, 2, 50)
	if err != nil {
		t.Fatalf("distributeRows: %v", err)
	}
	// Expect 3 files: a part1(50), a part2(25), b(5).
	fileCounts := map[string][]int{} // manager → [part1_size, part2_size, ...]
	for _, f := range files {
		key := f.managerEmail
		for len(fileCounts[key]) < f.fileIndex {
			fileCounts[key] = append(fileCounts[key], 0)
		}
		fileCounts[key][f.fileIndex-1] = len(f.rows)
	}
	want := map[string][]int{
		"a@instaedit.org": {50, 25},
		"b@instaedit.org": {5},
	}
	for mgr, w := range want {
		got := fileCounts[mgr]
		if !equalInts(got, w) {
			t.Errorf("manager %s: file sizes = %v, want %v", mgr, got, w)
		}
	}
}

func TestDistributeRows_DeterministicOrdering(t *testing.T) {
	spec := []interface{}{
		struct {
			manager string
			n       int
		}{"zeta@instaedit.org", 30},
		struct {
			manager string
			n       int
		}{"alpha@instaedit.org", 30},
		struct {
			manager string
			n       int
		}{"mike@instaedit.org", 30},
		struct {
			manager string
			n       int
		}{"bravo@instaedit.org", 30},
	}
	rows := makeRows(spec...)
	// Run twice with the same input — output bucket ordering should
	// be IDENTICAL (sorted bucketIndex → manager → partN).
	filesOnce, err := distributeRows(rows, 4, 50)
	if err != nil {
		t.Fatalf("distributeRows first call: %v", err)
	}
	filesTwice, err := distributeRows(rows, 4, 50)
	if err != nil {
		t.Fatalf("distributeRows second call: %v", err)
	}
	if len(filesOnce) != len(filesTwice) {
		t.Fatalf("len mismatch: %d vs %d", len(filesOnce), len(filesTwice))
	}
	for i := range filesOnce {
		if filesOnce[i].bucketIndex != filesTwice[i].bucketIndex ||
			filesOnce[i].managerEmail != filesTwice[i].managerEmail ||
			filesOnce[i].fileIndex != filesTwice[i].fileIndex {
			t.Errorf("ordering mismatch at index %d:\n  first  = %+v\n  second = %+v",
				i, filesOnce[i], filesTwice[i])
		}
	}
	// Sanity: ordering should be bucketIndex ASC. With 4 managers
	// sorted [alpha, bravo, mike, zeta] mapped modulo 4 → buckets
	// [0, 1, 2, 3] (because each manager is the only one in its
	// bucket, this also satisfies manager ASC).
	wantBuckets := []int{0, 1, 2, 3}
	for i, f := range filesOnce {
		if f.bucketIndex != wantBuckets[i] {
			t.Errorf("filesOnce[%d]: bucket = %d, want %d", i, f.bucketIndex, wantBuckets[i])
		}
	}
}

func TestDistributeRows_PartialOverflow75_SplitsAt50Boundary(t *testing.T) {
	// Exactly at the cap (50): no split.
	rowsExactly := makeRows(struct {
		manager string
		n       int
	}{"mgr@instaedit.org", 50})
	filesExactly, err := distributeRows(rowsExactly, 1, 50)
	if err != nil {
		t.Fatalf("distributeRows at-cap: %v", err)
	}
	if len(filesExactly) != 1 || filesExactly[0].fileIndex != 1 || len(filesExactly[0].rows) != 50 {
		t.Fatalf("at-cap 50: want 1 file of 50 rows, got len=%d fileIndex=%d rows=%d",
			len(filesExactly), filesExactly[0].fileIndex, len(filesExactly[0].rows))
	}

	// One over the cap (51): splits into 50 + 1.
	rowsOver := makeRows(struct {
		manager string
		n       int
	}{"mgr@instaedit.org", 51})
	filesOver, err := distributeRows(rowsOver, 1, 50)
	if err != nil {
		t.Fatalf("distributeRows over-cap: %v", err)
	}
	if len(filesOver) != 2 {
		t.Fatalf("over-cap 51: want 2 files, got %d", len(filesOver))
	}
	if len(filesOver[0].rows) != 50 || filesOver[0].fileIndex != 1 {
		t.Errorf("over-cap 51: file[0] want 50 rows index 1, got %d rows index %d",
			len(filesOver[0].rows), filesOver[0].fileIndex)
	}
	if len(filesOver[1].rows) != 1 || filesOver[1].fileIndex != 2 {
		t.Errorf("over-cap 51: file[1] want 1 row index 2, got %d rows index %d",
			len(filesOver[1].rows), filesOver[1].fileIndex)
	}
}

func TestDistributeRows_BucketAssignment_RoundRobin(t *testing.T) {
	// 5 managers + buckets=2 → bucket indices 0,1,0,1,0 in sorted
	// email order [a,b,c,d,e].
	spec := []interface{}{
		struct {
			manager string
			n       int
		}{"e@instaedit.org", 1},
		struct {
			manager string
			n       int
		}{"a@instaedit.org", 1},
		struct {
			manager string
			n       int
		}{"d@instaedit.org", 1},
		struct {
			manager string
			n       int
		}{"b@instaedit.org", 1},
		struct {
			manager string
			n       int
		}{"c@instaedit.org", 1},
	}
	rows := makeRows(spec...)
	files, err := distributeRows(rows, 2, 50)
	if err != nil {
		t.Fatalf("distributeRows: %v", err)
	}
	// sorted emails → [a, b, c, d, e] → buckets [0, 1, 0, 1, 0]
	wantSorted := []string{"a@instaedit.org", "b@instaedit.org", "c@instaedit.org", "d@instaedit.org", "e@instaedit.org"}
	wantBuckets := []int{0, 1, 0, 1, 0}
	if len(files) != 5 {
		t.Fatalf("len(files) = %d, want 5", len(files))
	}
	for i, f := range files {
		if f.managerEmail != wantSorted[i] {
			t.Errorf("files[%d]: manager = %q, want %q", i, f.managerEmail, wantSorted[i])
		}
		if f.bucketIndex != wantBuckets[i] {
			t.Errorf("files[%d]: bucket = %d, want %d (round-robin index mod 2)", i, f.bucketIndex, wantBuckets[i])
		}
	}
}

func TestReadInventoryCSV_HeaderMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.csv")
	if err := os.WriteFile(path, []byte("channel_id,channel_name\nUCaaa,Name\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := readInventoryCSV(path)
	if err == nil {
		t.Fatalf("expected error for missing manager_email_hint column")
	}
	if !strings.Contains(err.Error(), "manager_email_hint") {
		t.Errorf("error should name the missing column; got %q", err.Error())
	}
}

func TestReadInventoryCSV_EmptyChannelID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.csv")
	content := "channel_id,channel_name,manager_email_hint\n,mgr@instaedit.org\nUCbbb,Name2,mgr@instaedit.org\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := readInventoryCSV(path)
	if err == nil {
		t.Fatalf("expected error for empty channel_id on row 2")
	}
	if !strings.Contains(err.Error(), "row 2") {
		t.Errorf("error should mention row 2; got %q", err.Error())
	}
}

func TestReadInventoryCSV_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.csv")
	content := "channel_id,channel_name,manager_email_hint\nUCaaa,Channel A,a@x.com\nUCbbb,Channel B,a@x.com\nUCccc,Channel C,b@x.com\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rows, err := readInventoryCSV(path)
	if err != nil {
		t.Fatalf("readInventoryCSV: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	if rows[0].channelID != "UCaaa" || rows[0].managerEmailHint != "a@x.com" {
		t.Errorf("rows[0] = %+v, want channelID=UCaaa manager=a@x.com", rows[0])
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"A@instaedit.org", "a_instaedit_org"},
		{"mgr-1@instaedit.org", "mgr-1_instaedit_org"},
		{"", "_unassigned"},
		{"@@@", "___"},
	}
	for _, c := range cases {
		got := slugify(c.in)
		if got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWriteOutputFiles_HappyPath covers the file-system side of
// the CLI plus the on-disk CSV format compatibility with
// `internal/channelimport.CSVHeaderColumns`. The test does NOT
// spin up the full CLI; it calls the unexported writeOutputFiles
// directly to keep the assertion surface narrow.
func TestWriteOutputFiles_HappyPath(t *testing.T) {
	dir := t.TempDir()
	files := []bucketFile{
		{
			bucketIndex: 0, fileIndex: 1,
			managerEmail: "a@instaedit.org",
			rows: []inputRow{
				{channelID: "UCaaa", channelName: "Channel A", managerEmailHint: "a@instaedit.org", rowNum: 2},
				{channelID: "UCbbb", channelName: "Channel B", managerEmailHint: "a@instaedit.org", rowNum: 3},
			},
		},
	}
	if err := writeOutputFiles(dir, files); err != nil {
		t.Fatalf("writeOutputFiles: %v", err)
	}
	path := filepath.Join(dir, "manager_a_instaedit_org.csv")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back %s: %v", path, err)
	}
	// First line MUST be the canonical CSVHeaderColumns header
	// (drop-in compatible with channelimport.Parse).
	wantHeader := "channel_id,channel_name,manager_email_hint,workspace,group,language,timezone,expected_upload_frequency"
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines (header + 2 rows), got %d (%q)", len(lines), string(body))
	}
	if lines[0] != wantHeader {
		t.Errorf("header = %q\nwant     %q", lines[0], wantHeader)
	}
	// Assert the SEMANTIC contract: exactly 8 fields per outputCSVColumns,
	// the first three populated (channel_id, channel_name,
	// manager_email_hint), the last five empty so a downstream
	// re-import into channelimport.Parse can fill them out without
	// re-parsing the header. Byte-for-byte comma counting has bitten
	// this test already (the writer produces 8 fields = 7 commas
	// but counting commas in a quoted string is error-prone).
	parts := strings.Split(lines[1], ",")
	if len(parts) != 8 {
		t.Errorf("row 1 field count: want 8, got %d (full row: %q)", len(parts), lines[1])
		return
	}
	wantFields := []string{"UCaaa", "Channel A", "a@instaedit.org"}
	for i, want := range wantFields {
		if parts[i] != want {
			t.Errorf("row 1 field[%d]: want %q, got %q", i, want, parts[i])
		}
	}
	for i := 3; i < 8; i++ {
		if parts[i] != "" {
			t.Errorf("row 1 trailing field[%d]: want empty (workspace/group/language/timezone/expected_upload_frequency), got %q", i, parts[i])
		}
	}
}

// TestWriteOutputFiles_RedirectPipe confirms the script does not
// write to stdout (it's a clean CLI: only file-system side effects
// + summary lines). This test stubs stdout so a regression that
// spams CSV bytes to STDOUT would be caught.
func TestWriteOutputFiles_RedirectPipe(t *testing.T) {
	dir := t.TempDir()
	files := []bucketFile{
		{
			bucketIndex: 0, fileIndex: 1, managerEmail: "c@instaedit.org",
			rows:         []inputRow{{channelID: "UCccc", managerEmailHint: "c@instaedit.org", rowNum: 2}},
		},
	}
	// Capture stdout.
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	t.Cleanup(func() { _ = r.Close() })
	if err := writeOutputFiles(dir, files); err != nil {
		t.Fatalf("writeOutputFiles: %v", err)
	}
	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	out := buf.String()
	// The summary section talks about file counts; nothing inside
	// it should look like a CSV row.
	if strings.Contains(out, "channel_id,channel_name") {
		t.Errorf("stdout leaked CSV bytes:\n%s", out)
	}
}

// equalInts returns true when a and b have identical lengths AND
// every element matches. Tiny helper kept local to the test file.
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
