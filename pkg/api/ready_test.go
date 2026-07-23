package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
)

// readyTestRouter constructs a Router with just the /ready wiring
// populated and the rest left nil. The recovery middleware is NOT
// mounted here so per-test assertions stay focused on /ready itself.
func readyTestRouter(t *testing.T, db *sql.DB) *Router {
	t.Helper()
	return &Router{
		dbForReady: db,
		// capabilities is required by handleHealth even though /ready
		// doesn't read it; leave nil so any /ready constraint would
		// surface here.
	}
}

// TestReady_AllGreen_200 confirms the canonical 200 path: DB pings
// OK + every canary table present. The response body is the
// readinessResponse envelope with status="ok" and the per-check
// slots all "ok".
func TestReady_AllGreen_200(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	mock.ExpectPing()

	for _, table := range database.CanaryTables {
		mock.ExpectQuery(`SELECT to_regclass\('public\.' \|\| \$1\) IS NOT NULL`).
			WithArgs(table).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	}

	r := readyTestRouter(t, db)

	srv := httptest.NewServer(http.HandlerFunc(r.handleReady))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	var body readinessResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("Status: want \"ok\", got %q", body.Status)
	}
	if body.DB != "ok" {
		t.Errorf("DB: want \"ok\", got %q", body.DB)
	}
	if body.Migrations != "ok" {
		t.Errorf("Migrations: want \"ok\", got %q", body.Migrations)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestReady_DBPingDown_503: PingError from the DB → /ready returns
// 503 with the db slot surfacing the error string.
func TestReady_DBPingDown_503(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	mock.ExpectPing().WillReturnError(errors.New("simulated ping drop"))

	r := readyTestRouter(t, db)

	srv := httptest.NewServer(http.HandlerFunc(r.handleReady))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d", resp.StatusCode)
	}
	var body readinessResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "not_ready" {
		t.Errorf("Status: want \"not_ready\", got %q", body.Status)
	}
	if !strings.Contains(body.DB, "simulated ping drop") {
		t.Errorf("DB: want error string, got %q", body.DB)
	}
}

// TestReady_MigrationMissing_503: Ping OK but a canary table is
// missing → 503 + the migrations slot names the missing table.
func TestReady_MigrationMissing_503(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	// Pretend posts table is the first canary missing.
	const missing = "posts"
	hitMissing := false
	for _, table := range database.CanaryTables {
		exists := table != missing
		if !exists {
			hitMissing = true
		}
		mock.ExpectQuery(`SELECT to_regclass\('public\.' \|\| \$1\) IS NOT NULL`).
			WithArgs(table).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(exists))
		if hitMissing {
			break
		}
	}

	r := readyTestRouter(t, db)

	srv := httptest.NewServer(http.HandlerFunc(r.handleReady))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d", resp.StatusCode)
	}
	var body readinessResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.DB != "ok" {
		t.Errorf("DB: want ok (sqlmock skips Ping), got %q", body.DB)
	}
	if !strings.Contains(body.Migrations, missing) {
		t.Errorf("Migrations: want missing table %q in the error, got %q", missing, body.Migrations)
	}
}

// TestReady_NoDBWired_503 confirms: with no DB wired the handler
// surfaces "db not configured" so the operator notices the missing
// option. The endpoint still returns 503 because DB ping can't be
// confirmed.
func TestReady_NoDBWired_503(t *testing.T) {
	r := readyTestRouter(t, nil)

	srv := httptest.NewServer(http.HandlerFunc(r.handleReady))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d", resp.StatusCode)
	}
	var body readinessResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Migrations == "ok" {
		t.Errorf("Migrations: want error (no DB wired), got ok")
	}
}
