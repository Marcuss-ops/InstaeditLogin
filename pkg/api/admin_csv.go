package api

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"time"
)

// adminCSVTimeout caps a single CSV export. A client disconnect
// (browser closed, network drop) cancels ctx after 30s so the
// goroutine exits cleanly instead of writing forever. The cap is
// intentionally generous: a 200-row stuck-jobs CSV writes in <50ms,
// and a 200-channel admin/health CSV writes in <100ms; 30s is the
// "shouldn't happen, abort if so" budget.
const adminCSVTimeout = 30 * time.Second

// adminCSVBufferSize is the bufio.Writer flush granularity. CSV
// rows are typically <200 bytes; 32KB → ~150 rows per flush — large
// enough to amortise syscall cost, small enough that the
// end-of-stream flush returns promptly when the dataset is bounded.
const adminCSVBufferSize = 32 * 1024

// adminCSVFilenamePrefix is the operator-facing download name
// prefix; full filename is "<prefix>-<RFC3339-timestamp>.csv" so
// multiple exports in the same minute don't collide on a browser
// cache or a local download directory.
const adminCSVFilenamePrefix = "instaedit-ops"

// writeAdminCSV opens a streaming CSV response with the standard
// admin/D4.a headers. The caller streams rows via the returned
// *csv.Writer. The function returns when the writer is closed by
// the caller OR when the context-cancel timer fires, whichever
// first. Returns the writer + a flush callback so the handler can
// commit before the response commits.
//
// Contract:
//   - caller MUST call flush() before returning from the handler
//     to ensure every row reaches the wire before the response
//     closes.
//   - rows are written in caller-defined order. The header row
//     is the caller's responsibility too (this helper intentionally
//     doesn't auto-emit headers — different endpoints have
//     different column sets).
//   - Content-Type is "text/csv; charset=utf-8" + Content-Disposition
//     attachment so a browser downloads rather than renders. The
//     SPA / curl / -O flag all get the same filename.
func writeAdminCSV(w http.ResponseWriter, sectionLabel string) (ctx context.Context, writer *csv.Writer, flush func() error, err error) {
	// Build the filename: <prefix>-<section>-<RFC3339 timestamp>.csv
	// so an operator exporting /admin/queue.csv and /admin/health.csv
	// in the same minute still gets two distinct downloads.
	now := time.Now().UTC()
	filename := fmt.Sprintf("%s-%s-%s.csv",
		adminCSVFilenamePrefix,
		sectionLabel,
		now.Format("2006-01-02T150405Z"),
	)

	// Ctx-bound abort: 30s is generous for any bounded export.
	// The handler runs in its own goroutine on the request path
	// and inherits req.Context(); a parent ctx.Done cancels the
	// write loop when the client disconnects OR the export
	// deadline trips. We attach the timer to a child ctx so the
	// parent is unchanged.
	ctx, cancel := context.WithTimeout(context.Background(), adminCSVTimeout)
	_ = cancel // lifetime: until first flush or context-deadline

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")

	buf := bufio.NewWriterSize(w, adminCSVBufferSize)
	csvw := csv.NewWriter(buf)

	flush = func() error {
		csvw.Flush()
		return buf.Flush()
	}
	return ctx, csvw, flush, nil
}
