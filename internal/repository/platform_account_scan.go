package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// scanPlatformAccountRows scans rows matching the standard SELECT column
// order used by the platform account list queries. Extracted so filtered
// and unfiltered queries share the same mapping logic.
func scanPlatformAccountRows(rows *sql.Rows) ([]*models.PlatformAccount, error) {
	var accounts []*models.PlatformAccount
	for rows.Next() {
		a := &models.PlatformAccount{}
		var metadata []byte
		if err := rows.Scan(&a.ID, &a.UserID, &a.Platform, &a.PlatformUserID, &a.Username, &a.Status, &a.ConnectedAt,
			&a.LastValidatedAt, &a.LastRefreshAt, &a.ReauthRequiredAt, &a.LastErrorCode,
			&a.LastErrorMessage, &metadata, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan platform account: %w", err)
		}
		a.Metadata = scanMetadata(metadata)
		accounts = append(accounts, a)
	}
	return accounts, nil
}

// coalesceStr returns the first non-empty string.
func coalesceStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// scanMetadata unmarshals a JSONB byte slice into a Metadata map.
func scanMetadata(data []byte) models.Metadata {
	if len(data) == 0 {
		return models.Metadata{}
	}
	var m models.Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return models.Metadata{}
	}
	return m
}
