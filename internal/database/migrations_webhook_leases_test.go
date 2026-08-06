//go:build integration

package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestMigration110_WebhookLeaseSchemaAndIndex(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()
	if err := RunMigrationsUpTo(db, 109); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationByName(t, db, "110_webhook_delivery_leases.sql"); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"lease_id", "lease_until", "heartbeat_at"} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='webhook_deliveries' AND column_name=$1)`, column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("missing webhook_deliveries.%s", column)
		}
	}
	var def string
	if err := db.QueryRow(`SELECT indexdef FROM pg_indexes WHERE indexname='idx_webhook_deliveries_lease_due'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "status") || !strings.Contains(def, "pending") {
		t.Fatalf("unexpected lease index: %s", def)
	}
}

func TestWebhookDeliveryLease_ThreeReplicasOneOwnerTakeoverAndCAS(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()
	if err := RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	deliveryID := seedWebhookDelivery(t, db)

	const replicas = 3
	start := make(chan struct{})
	results := make(chan struct {
		deliveries []repository.WebhookDelivery
		err        error
	}, replicas)
	var wg sync.WaitGroup
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := repository.NewWebhookRepository(db).ClaimDueDeliveries(context.Background(), 1, 2*time.Minute)
			results <- struct {
				deliveries []repository.WebhookDelivery
				err        error
			}{got, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var winner repository.WebhookDelivery
	winners := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if len(result.deliveries) == 1 {
			winner = result.deliveries[0]
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("lease winners=%d, want 1", winners)
	}
	if winner.ID != deliveryID || winner.LeaseID == "" {
		t.Fatalf("winner=%+v", winner)
	}

	if _, err := db.Exec(`UPDATE webhook_deliveries SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, deliveryID); err != nil {
		t.Fatal(err)
	}
	recovered, err := repository.NewWebhookRepository(db).ClaimDueDeliveries(context.Background(), 1, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].LeaseID == winner.LeaseID {
		t.Fatalf("recovered=%+v", recovered)
	}
	current := recovered[0].LeaseID

	if err := repository.NewWebhookRepository(db).HeartbeatLease(context.Background(), deliveryID, winner.LeaseID, time.Minute); !errors.Is(err, repository.ErrWebhookLeaseLost) {
		t.Fatalf("stale heartbeat=%v, want ErrWebhookLeaseLost", err)
	}
	if err := repository.NewWebhookRepository(db).MarkSuccess(context.Background(), deliveryID, winner.LeaseID, "stale"); !errors.Is(err, repository.ErrWebhookLeaseLost) {
		t.Fatalf("stale MarkSuccess=%v, want ErrWebhookLeaseLost", err)
	}
	if err := repository.NewWebhookRepository(db).MarkRetry(context.Background(), deliveryID, winner.LeaseID, "stale", "", "", time.Now().Add(time.Minute)); !errors.Is(err, repository.ErrWebhookLeaseLost) {
		t.Fatalf("stale MarkRetry=%v, want ErrWebhookLeaseLost", err)
	}
	if err := repository.NewWebhookRepository(db).MarkDead(context.Background(), deliveryID, winner.LeaseID, "stale", "", ""); !errors.Is(err, repository.ErrWebhookLeaseLost) {
		t.Fatalf("stale MarkDead=%v, want ErrWebhookLeaseLost", err)
	}
	if err := repository.NewWebhookRepository(db).HeartbeatLease(context.Background(), deliveryID, current, time.Minute); err != nil {
		t.Fatalf("current heartbeat: %v", err)
	}
	if err := repository.NewWebhookRepository(db).MarkSuccess(context.Background(), deliveryID, current, "current"); err != nil {
		t.Fatalf("current MarkSuccess: %v", err)
	}
}

func seedWebhookDelivery(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID, workspaceID, endpointID, eventID, deliveryID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO users (email,name) VALUES ($1,$2) RETURNING id`, "webhook-"+suffix+"@test", "webhook").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO workspaces (name,owner_id) VALUES ($1,$2) RETURNING id`, "webhook-"+suffix, userID).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO webhook_endpoints (workspace_id,url,secret,events) VALUES ($1,'http://127.0.0.1/unused','secret',ARRAY['post.published']) RETURNING id`, workspaceID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO webhook_events (event_id,event_type,workspace_id,payload) VALUES ($1,'post.published',$2,'{}') RETURNING id`, "event-"+suffix, workspaceID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO webhook_deliveries (event_id,endpoint_id) VALUES ($1,$2) RETURNING id`, eventID, endpointID).Scan(&deliveryID); err != nil {
		t.Fatal(err)
	}
	return deliveryID
}
