//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// TestMigration131_WebhookFanOutIdempotency proves the migration-131
// contract end to end against real Postgres: a caller that re-emits the
// SAME event_id fans out exactly ONE delivery row per endpoint, and each
// endpoint still gets its own row (fan-out is per-endpoint, dedupe is per
// (event_id, endpoint_id) pair).
//
// Failures this pins against regressions:
//   - a dropped UNIQUE index would make CreateDelivery insert a second
//     row for the same pair (the exact pre-131 duplicate-fanout bug);
//   - an over-broad uniqueness (e.g. event_id alone) would collapse
//     distinct endpoints into one delivery.
func TestMigration131_WebhookFanOutIdempotency(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()
	if err := RunMigrations(db); err != nil {
		t.Fatal(err)
	}

	// Seed one workspace with TWO endpoints (the fan-out targets) and
	// one event. Two distinct event_ids exercise the pair-scoping.
	suffix := time.Now().Format("150405.000000000")
	ctx := context.Background()
	var userID, workspaceID, endpointA, endpointB int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (email,name) VALUES ($1,$2) RETURNING id`,
		"fanout-"+suffix+"@test", "fanout").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO workspaces (name,owner_id) VALUES ($1,$2) RETURNING id`,
		"fanout-"+suffix, userID).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO webhook_endpoints (workspace_id,url,secret,events) VALUES ($1,'http://127.0.0.1/a','secret',ARRAY['post.published']) RETURNING id`,
		workspaceID).Scan(&endpointA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO webhook_endpoints (workspace_id,url,secret,events) VALUES ($1,'http://127.0.0.1/b','secret',ARRAY['post.published']) RETURNING id`,
		workspaceID).Scan(&endpointB); err != nil {
		t.Fatal(err)
	}

	eventKey := "evt-fanout-" + suffix
	var eventRowID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO webhook_events (event_id,event_type,workspace_id,payload) VALUES ($1,'post.published',$2,'{}') RETURNING id`,
		eventKey, workspaceID).Scan(&eventRowID); err != nil {
		t.Fatal(err)
	}

	repo := repository.NewWebhookRepository(db)

	// First Emit: fan out to both endpoints.
	for _, ep := range []int64{endpointA, endpointB} {
		d := &repository.WebhookDelivery{EventID: eventRowID, EndpointID: ep}
		if err := repo.CreateDelivery(ctx, d); err != nil {
			t.Fatalf("first emit endpoint %d: %v", ep, err)
		}
		if d.ID == 0 {
			t.Fatalf("first emit endpoint %d: ID must be populated (row inserted)", ep)
		}
	}

	// Re-emit the SAME event twice more: the (event_id, endpoint_id)
	// conflict must be a no-op (d.ID stays 0 → "already fanned out").
	for round := 0; round < 2; round++ {
		for _, ep := range []int64{endpointA, endpointB} {
			d := &repository.WebhookDelivery{EventID: eventRowID, EndpointID: ep}
			if err := repo.CreateDelivery(ctx, d); err != nil {
				t.Fatalf("re-emit round %d endpoint %d: %v", round, ep, err)
			}
			if d.ID != 0 {
				t.Errorf("re-emit round %d endpoint %d: ID=%d, want 0 (dedupe no-op)", round, ep, d.ID)
			}
		}
	}

	// Exactly one row per (event_id, endpoint_id) pair, one per endpoint.
	var aCount, bCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM webhook_deliveries WHERE event_id=$1 AND endpoint_id=$2`,
		eventRowID, endpointA).Scan(&aCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM webhook_deliveries WHERE event_id=$1 AND endpoint_id=$2`,
		eventRowID, endpointB).Scan(&bCount); err != nil {
		t.Fatal(err)
	}
	if aCount != 1 || bCount != 1 {
		t.Fatalf("delivery rows per endpoint: a=%d b=%d, want 1 and 1", aCount, bCount)
	}

	// The unique index itself exists (a dropped index would silently
	// reintroduce duplicates without any code change).
	var idx bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname='uq_webhook_deliveries_event_endpoint' AND indexdef LIKE '%UNIQUE%')`).Scan(&idx); err != nil {
		t.Fatal(err)
	}
	if !idx {
		t.Fatal("missing UNIQUE index uq_webhook_deliveries_event_endpoint")
	}

	// Pair scoping: the SAME endpoint with a DIFFERENT event must
	// still fan out (dedupe is per pair, not per endpoint).
	otherEventKey := "evt-fanout-2-" + suffix
	var otherEventRowID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO webhook_events (event_id,event_type,workspace_id,payload) VALUES ($1,'post.published',$2,'{}') RETURNING id`,
		otherEventKey, workspaceID).Scan(&otherEventRowID); err != nil {
		t.Fatal(err)
	}
	d := &repository.WebhookDelivery{EventID: otherEventRowID, EndpointID: endpointA}
	if err := repo.CreateDelivery(ctx, d); err != nil {
		t.Fatalf("second event fan-out: %v", err)
	}
	if d.ID == 0 {
		t.Fatal("second event fan-out must insert a fresh row (dedupe is per pair, not per endpoint)")
	}
}
