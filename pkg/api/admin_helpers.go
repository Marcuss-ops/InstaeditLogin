package api

import (
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// itoa is a plain-int format helper used by the CSV writers. We use
// strconv.FormatInt instead of fmt.Sprint to avoid the strconv
// quote-bytes formatting dance on negative numbers (CSV safety
// invariant: rows must contain no double-quotes / unescaped commas).
func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// ftoa formats a 0..1 error-rate to 4 decimal places so the CSV
// renders cleanly in spreadsheet tools (no scientific notation).
// "0.0000" is the zero-rate form so empty rows sort predictably.
func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

// formatTimePtr renders a nullable wall-clock as RFC3339Nano in UTC
// (empty string when nil). The CSV readers use empty-string as
// "absent" so an absent field is distinguishable from a zero-time.
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// nowUnix returns the current wall-clock as a unix-seconds int64.
// The dashboard's "generated_at" field is a plain number so
// JS Date construction is one roundtrip away (new Date(seconds*1000)).
func nowUnix() int64 { return time.Now().Unix() }

// slogCSVStreamError logs the rare write-end failure on a CSV
// export. The handler already streamed the response so a failure
// here is "client disconnected mid-stream" or "proxy dropped"; we
// surface it at INFO (not WARN) because the data was correct and
// the next attempt by the operator will succeed.
func slogCSVStreamError(section string, err error) {
	slog.Info("admin: csv stream closed early",
		"section", section,
		"error", err.Error(),
	)
}

// ensureFmtPackageReferenced is a no-op anchor so `fmt` stays
// imported even when later refactors drop the only call site.
// (P2 era: writeError / writeJSON in this package still use fmt.)
var _ = fmt.Sprintf
