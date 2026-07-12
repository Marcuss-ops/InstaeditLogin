package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// AuditLogRepository handles persistence of audit log events.
type AuditLogRepository struct {
	db *sql.DB
}

// NewAuditLogRepository creates a new AuditLogRepository.
func NewAuditLogRepository(db *sql.DB) *AuditLogRepository {
	return &AuditLogRepository{db: db}
}

// Insert persists an audit log event. UserID may be zero for anonymous events.
func (r *AuditLogRepository) Insert(log *models.AuditLog) error {
	metadataJSON, err := json.Marshal(log.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal audit metadata: %w", err)
	}

	var userID interface{}
	if log.UserID > 0 {
		userID = log.UserID
	}

	err = r.db.QueryRow(
		`INSERT INTO audit_logs (user_id, session_id, action, resource_type, resource_id, result, ip_hash, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, created_at`,
		userID, log.SessionID, log.Action, log.ResourceType, log.ResourceID, log.Result, log.IPHash, metadataJSON,
	).Scan(&log.ID, &log.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to insert audit log: %w", err)
	}
	return nil
}
