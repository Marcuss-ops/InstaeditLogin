package services

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// Tests for the Drive re-initiate churn guard (C1). Contract:
//
//  1. An expired session row is reset IN PLACE (no delete + re-Create),
//     the fresh-initiate branch re-POSTs against the refreshed row, and
//     the progress write lands with the post-reset version.
//  2. When the reset pushes attempt_count past driveReinitiateCap, the
//     destination fails closed: typed ErrDriveReinitiateBudgetExhausted,
//     the row is stamped state='failed' (reconciler stops re-picking),
//     and delivery_drive_reinitiate_loops_total increments.
//  3. A CAS loss on the reset is tolerated (peer won) and Deliver
//     proceeds against the refreshed row snapshot.

// churnGuardHarness wires the destination against the fake Drive server
// (app-property list returns empty → no dedupe hit) and a Regexp-matched
// sqlmock so each repo call is matched by its distinctive SQL prefix.
type churnGuardHarness struct {
	dst  *GoogleDriveDestination
	mock sqlmock.Sqlmock
	db   *sql.DB
	drv  *httptest.Server
}

func newChurnGuardHarness(t *testing.T) *churnGuardHarness {
	t.Helper()
	drvSrv, _ := makeDestinationServer(t)
	t.Cleanup(drvSrv.Close)

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	dst, err := NewGoogleDriveDestination(
		repository.NewDeliverySessionRepository(db),
		&fakeDriveAccessTokenProvider{fixed: "fake-bearer"},
		fakeDriveEncryptor{},
		rewriteTransportForFake(t, drvSrv.URL),
		256*1024,
	)
	if err != nil {
		t.Fatalf("NewGoogleDriveDestination: %v", err)
	}
	return &churnGuardHarness{dst: dst, mock: mock, db: db, drv: drvSrv}
}

// churnFindCols mirrors the FindByIdempotencyKey projection order.
var churnFindCols = []string{
	"id", "deliverable_type", "idempotency_key", "state",
	"session_uri_encrypted", "uploaded_bytes", "total_bytes", "chunk_size",
	"mime_type", "folder_id", "filename", "app_properties",
	"remote_file_id", "remote_url",
	"worker_id", "lease_expires_at", "expires_at",
	"error_message", "error_code", "attempt_count", "version",
	"created_at", "updated_at",
}

const (
	churnKey      = "key-churn"
	churnRowID    = int64(12)
	churnWorkerID = "publish_worker_post_completion"
)

func (h *churnGuardHarness) expectFindRow(state string, sessionURI string, attemptCount, version int) {
	expires := time.Now().Add(7 * 24 * time.Hour)
	h.mock.ExpectQuery(`SELECT id, deliverable_type, idempotency_key, state`).
		WithArgs("google-drive", churnKey).
		WillReturnRows(sqlmock.NewRows(churnFindCols).AddRow(
			churnRowID, "google-drive", churnKey, state,
			sessionURI, 0, int64(1024), int64(262144),
			"video/mp4", "folder-1", "clip.mp4", []byte(`{"instaedit_delivery_id":"`+churnKey+`"}`),
			nil, nil,
			churnWorkerID, nil, expires,
			"drive_session_expired", "expired at", attemptCount, version,
			time.Now(), time.Now(),
		))
}

func (h *churnGuardHarness) expectResetReturning(newAttemptCount int) {
	h.mock.ExpectQuery(`^UPDATE delivery_sessions`).
		WithArgs(churnRowID, churnWorkerID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count"}).AddRow(newAttemptCount))
}

func (h *churnGuardHarness) expectResetCASHLost() {
	// Empty result set → sql.ErrNoRows inside the repo → typed
	// ErrDeliverySessionVersionMismatch.
	h.mock.ExpectQuery(`^UPDATE delivery_sessions`).
		WithArgs(churnRowID, churnWorkerID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count"}))
}

func (h *churnGuardHarness) expectMarkFailedConsumed() {
	h.mock.ExpectExec(`^UPDATE delivery_sessions`).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func (h *churnGuardHarness) deliver() (*models.DeliveryResult, error) {
	return h.dst.Deliver(context.Background(),
		&models.MediaAsset{ID: "asset-churn", SizeBytes: 1024, ContentType: "video/mp4"},
		&models.DeliveryDestination{
			Provider: "google-drive",
			Config: map[string]string{
				"drive_account_id":  "1",
				"folder_id":         "folder-1",
				"filename_template": "{title}.mp4",
			},
		}, churnKey)
}

func TestGoogleDriveDestination_Deliver_ExpiredRow_ResetInPlace_Reinitiates(t *testing.T) {
	h := newChurnGuardHarness(t)

	// Find: row in state='expired' (attempt_count=4, version=7).
	h.expectFindRow("expired", "", 4, 7)
	// In-place reset: attempt_count 4→5 (monotonic), version CAS at 7.
	h.expectResetReturning(5)
	// Re-Find after the reset: state='initiated', empty URI, version=8.
	h.expectFindRow("initiated", "", 5, 8)

	// The fake Drive server answers the list (dedupe miss), then the
	// fresh POST initiate with 501 (not implemented) → Deliver fails at
	// the re-initiate stage, but ONLY after the full reset path ran
	// (Find → Reset → Re-Find → fresh POST).
	_, err := h.deliver()
	if err == nil {
		t.Fatalf("expected the 501 initiate-stage error after a successful reset path")
	}
	if !strings.Contains(err.Error(), "postInitiateSession") {
		t.Errorf("err should fail at the fresh-initiate stage; got %v", err)
	}
	if strings.Contains(err.Error(), string(ErrDriveReinitiateBudgetExhausted.Error())) {
		t.Errorf("attempt_count=5 is within budget; unexpected budget exhaustion: %v", err)
	}
	if err := h.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("reset path must run Find→Reset→Re-Find→UpdateProgress in order; unmet: %v", err)
	}
}

func TestGoogleDriveDestination_Deliver_ReinitiateBudgetExhausted_FailsClosed(t *testing.T) {
	h := newChurnGuardHarness(t)

	before := testutil.ToFloat64(metrics.DriveReinitiateLoops.WithLabelValues("google-drive"))

	// Find: row already expired with attempt_count=5 (== cap).
	h.expectFindRow("expired", "", 5, 7)
	// Reset bumps to 6 — past driveReinitiateCap=5.
	h.expectResetReturning(6)
	// Re-Find (row refreshed), then the failed-stamp MUST run.
	h.expectFindRow("initiated", "", 6, 8)
	h.expectMarkFailedConsumed()

	_, err := h.deliver()
	if !errors.Is(err, ErrDriveReinitiateBudgetExhausted) {
		t.Fatalf("err = %v, want ErrDriveReinitiateBudgetExhausted", err)
	}

	// Alarm metric incremented exactly once for google-drive.
	after := testutil.ToFloat64(metrics.DriveReinitiateLoops.WithLabelValues("google-drive"))
	if after-before != 1 {
		t.Errorf("delivery_drive_reinitiate_loops_total delta = %v, want 1", after-before)
	}

	// The failed-stamp (reconciler stop-picking guarantee) consumed.
	if err := h.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("MarkFailed after budget exhaustion must persist; unmet: %v", err)
	}
}

func TestGoogleDriveDestination_Deliver_ResetCASLoss_Tolerated(t *testing.T) {
	h := newChurnGuardHarness(t)

	before := testutil.ToFloat64(metrics.DriveReinitiateLoops.WithLabelValues("google-drive"))

	// Find: expired row (version=7).
	h.expectFindRow("expired", "", 5, 7)
	// Reset loses the CAS (peer won) — tolerated, NOT fatal.
	h.expectResetCASHLost()
	// Re-Find: the peer already put the row back to 'uploading' with a
	// live URI and a bumped version — Deliver proceeds on that snapshot.
	// The stored value is base64(encryptor ciphertext), matching what
	// the destination's Create/UpdateProgress write.
	storedURI := base64.StdEncoding.EncodeToString([]byte("enc:https://session-uri"))
	h.expectFindRow("uploading", storedURI, 5, 9)

	// With a live URI the fresh-initiate branch is skipped; Deliver
	// reaches the source-URL guard and errors there (dest.RemoteURL is
	// empty in this harness) — proving the CAS loss didn't abort.
	_, err := h.deliver()
	if err == nil {
		t.Fatalf("expected the empty-RemoteURL guard error after tolerating the CAS loss")
	}
	if !strings.Contains(err.Error(), "RemoteURL") {
		t.Errorf("err should surface the source-URL guard; got %v", err)
	}

	// CAS loss is not a churn event: no alarm.
	after := testutil.ToFloat64(metrics.DriveReinitiateLoops.WithLabelValues("google-drive"))
	if after != before {
		t.Errorf("CAS loss must not increment delivery_drive_reinitiate_loops_total; delta = %v", after-before)
	}
	if err := h.mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
