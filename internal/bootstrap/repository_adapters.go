package bootstrap

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// The adapters in this file keep repository-specific conversions out of the
// startup composition path. They intentionally remain thin: no business
// policy belongs in bootstrap.

func valueString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func valueStringMap(m map[string]any, k string) string { v, _ := m[k].(string); return v }

func valueStringPtrMap(m map[string]any, k string) *string {
	v := valueStringMap(m, k)
	if v == "" {
		return nil
	}
	return &v
}

func valueIntPtrMap(m map[string]any, k string) *int64 {
	v, ok := m[k].(float64)
	if !ok {
		return nil
	}
	n := int64(v)
	return &n
}

func valueIntsMap(m map[string]any, k string) []int64 {
	raw, _ := m[k].([]any)
	out := make([]int64, 0, len(raw))
	for _, x := range raw {
		if v, ok := x.(float64); ok {
			out = append(out, int64(v))
		}
	}
	return out
}

type connectionStateStoreWrapper struct {
	repo *repository.ConnectionStateRepository
}

func (w *connectionStateStoreWrapper) Create(state *repository.ConnectionState) error {
	return w.repo.Create(state)
}

func (w *connectionStateStoreWrapper) Consume(id string, expectedNonce string, jwtWorkspaceID int64) (*repository.ConnectionState, error) {
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid uuid: %w", err)
	}
	return w.repo.Consume(parsedID, expectedNonce, jwtWorkspaceID)
}

type auditLogStoreWrapper struct {
	repo *repository.AuditLogRepository
}

func (w *auditLogStoreWrapper) Log(ctx context.Context, eventType, actorID string, resourceType, resourceID string, metadata map[string]interface{}) error {
	var userID int64
	if actorID != "" && actorID != "system" {
		_, _ = fmt.Sscan(actorID, &userID)
	}
	var resID int64
	if resourceID != "" {
		_, _ = fmt.Sscan(resourceID, &resID)
	}

	result := "success"
	if r, ok := metadata["result"].(string); ok {
		result = r
	}
	ipHash := ""
	if ip, ok := metadata["ip_hash"].(string); ok {
		ipHash = ip
	}
	sessionID := ""
	if sid, ok := metadata["session_id"].(string); ok {
		sessionID = sid
	}

	return w.repo.Insert(&models.AuditLog{
		UserID:       userID,
		SessionID:    sessionID,
		Action:       eventType,
		ResourceType: resourceType,
		ResourceID:   resID,
		Result:       result,
		IPHash:       ipHash,
		Metadata:     metadata,
	})
}
