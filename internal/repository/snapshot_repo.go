package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AccountResourceSnapshot represents a cached snapshot of a remote
// platform resource (channel stats, profile, branding). Stored in
// account_resource_snapshots and refreshed on demand or when stale.
type AccountResourceSnapshot struct {
	PlatformAccountID int64
	ResourceType      string
	Profile           map[string]interface{}
	Statistics        map[string]interface{}
	Status            map[string]interface{}
	Content           map[string]interface{}
	ProviderETag      string
	FetchedAt         time.Time
	UpdatedAt         time.Time
}

// SnapshotRepository handles CRUD for account_resource_snapshots.
type SnapshotRepository struct {
	db *sql.DB
}

// NewSnapshotRepository creates a new SnapshotRepository.
func NewSnapshotRepository(db *sql.DB) *SnapshotRepository {
	return &SnapshotRepository{db: db}
}

// GetSnapshot returns the cached snapshot for a platform account, or nil
// if no snapshot exists.
func (r *SnapshotRepository) GetSnapshot(platformAccountID int64) (*AccountResourceSnapshot, error) {
	row := r.db.QueryRow(
		`SELECT platform_account_id, resource_type, profile, statistics, status, content,
		        provider_etag, fetched_at, updated_at
		 FROM account_resource_snapshots
		 WHERE platform_account_id = $1`,
		platformAccountID,
	)

	snap := &AccountResourceSnapshot{}
	var profileJSON, statsJSON, statusJSON, contentJSON []byte
	err := row.Scan(
		&snap.PlatformAccountID, &snap.ResourceType,
		&profileJSON, &statsJSON, &statusJSON, &contentJSON,
		&snap.ProviderETag, &snap.FetchedAt, &snap.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get snapshot: %w", err)
	}

	if err := json.Unmarshal(profileJSON, &snap.Profile); err != nil {
		return nil, fmt.Errorf("unmarshal profile: %w", err)
	}
	if err := json.Unmarshal(statsJSON, &snap.Statistics); err != nil {
		return nil, fmt.Errorf("unmarshal statistics: %w", err)
	}
	if err := json.Unmarshal(statusJSON, &snap.Status); err != nil {
		return nil, fmt.Errorf("unmarshal status: %w", err)
	}
	if err := json.Unmarshal(contentJSON, &snap.Content); err != nil {
		return nil, fmt.Errorf("unmarshal content: %w", err)
	}

	return snap, nil
}

// UpsertSnapshot creates or updates the snapshot for a platform account.
// Uses INSERT ... ON CONFLICT to handle both first-time and refresh cases.
func (r *SnapshotRepository) UpsertSnapshot(snap *AccountResourceSnapshot) error {
	profileJSON, err := json.Marshal(snap.Profile)
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}
	statsJSON, err := json.Marshal(snap.Statistics)
	if err != nil {
		return fmt.Errorf("marshal statistics: %w", err)
	}
	statusJSON, err := json.Marshal(snap.Status)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	contentJSON, err := json.Marshal(snap.Content)
	if err != nil {
		return fmt.Errorf("marshal content: %w", err)
	}

	_, err = r.db.Exec(
		`INSERT INTO account_resource_snapshots
		    (platform_account_id, resource_type, profile, statistics, status, content,
		     provider_etag, fetched_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		 ON CONFLICT (platform_account_id) DO UPDATE SET
		    resource_type = EXCLUDED.resource_type,
		    profile       = EXCLUDED.profile,
		    statistics    = EXCLUDED.statistics,
		    status        = EXCLUDED.status,
		    content       = EXCLUDED.content,
		    provider_etag = EXCLUDED.provider_etag,
		    fetched_at    = EXCLUDED.fetched_at,
		    updated_at    = NOW()`,
		snap.PlatformAccountID, snap.ResourceType,
		profileJSON, statsJSON, statusJSON, contentJSON,
		snap.ProviderETag, snap.FetchedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert snapshot: %w", err)
	}
	return nil
}

// DeleteSnapshot removes the snapshot for a platform account.
func (r *SnapshotRepository) DeleteSnapshot(platformAccountID int64) error {
	_, err := r.db.Exec(
		`DELETE FROM account_resource_snapshots WHERE platform_account_id = $1`,
		platformAccountID,
	)
	if err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	return nil
}

// IsSnapshotStale returns true if the snapshot is older than the given
// duration, or if no snapshot exists.
func (r *SnapshotRepository) IsSnapshotStale(platformAccountID int64, maxAge time.Duration) (bool, error) {
	snap, err := r.GetSnapshot(platformAccountID)
	if err != nil {
		return false, err
	}
	if snap == nil {
		return true, nil
	}
	return time.Since(snap.FetchedAt) > maxAge, nil
}
