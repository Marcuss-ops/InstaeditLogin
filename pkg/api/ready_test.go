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
func readyTestRouter(t *testing.T, db *sql.DB, ws *WorkerStatus) *Router {
	t.Helper()
	return &Router{
		dbForReady:   db,
		workerStatus: ws,
		// capabilities is required by handleHealth even though /ready
		// doesn't read it; leave nil so any /ready constraint would
		// surface here.
	}
}

// TestReady_AllGreen_200 confirms the canonical 200 path: DB pings
// OK + every canary table present + every worker has marked
// "started". The response body is the readinessResponse envelope
// with status="ok" and the per-check slots all "ok"/true.
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

	ws := NewWorkerStatus([]string{"publish", "reconcile", "outbox", "webhook", "metrics"})
	for _, name := range []string{"publish", "reconcile", "outbox", "webhook", "metrics"} {
		ws.Mark(name)
	}
	r := readyTestRouter(t, db, ws)

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
	if !body.WorkersReady {
		t.Errorf("WorkersReady: want true, got false")
	}
	if len(body.WorkersPending) != 0 {
		t.Errorf("WorkersPending: want empty, got %v", body.WorkersPending)
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

	ws := NewWorkerStatus([]string{"publish"})
	ws.Mark("publish")
	r := readyTestRouter(t, db, ws)

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

	ws := NewWorkerStatus([]string{"publish"})
	ws.Mark("publish")
	r := readyTestRouter(t, db, ws)

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

// TestReady_WorkerNotStarted_503: db+schema green but WorkerStatus
// reports pending workers → 503 + the pending list in the response.
func TestReady_WorkerNotStarted_503(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	for _, table := range database.CanaryTables {
		mock.ExpectQuery(`SELECT to_regclass\('public\.' \|\| \$1\) IS NOT NULL`).
			WithArgs(table).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	}

	ws := NewWorkerStatus([]string{"publish", "reconcile", "metrics"})
	// Only publish marks itself as started; reconcile + metrics are
	// intentionally not flipped (simulating a deadlock-on-startup).
	ws.Mark("publish")

	r := readyTestRouter(t, db, ws)
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
	if body.WorkersReady {
		t.Errorf("WorkersReady: want false (2 workers pending), got true")
	}
	// Pending should list reconcile + metrics but NOT publish.
	pendingSet := map[string]bool{}
	for _, p := range body.WorkersPending {
		pendingSet[p] = true
	}
	if !pendingSet["reconcile"] || !pendingSet["metrics"] {
		t.Errorf("WorkersPending: want [reconcile, metrics], got %v", body.WorkersPending)
	}
	if pendingSet["publish"] {
		t.Errorf("publish should NOT be in WorkersPending: %v", body.WorkersPending)
	}
}

// TestReady_NoDBWired_503 confirms: with no DB wired the handler
// surfaces "db not configured" so the operator notices the missing
// option. The endpoint still returns 503 because DB ping can't be
// confirmed.
func TestReady_NoDBWired_503(t *testing.T) {
	ws := NewWorkerStatus([]string{"publish"})
	ws.Mark("publish")
	r := readyTestRouter(t, nil, ws)

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
