package editorlaunch

import (
	"errors"
	"testing"
	"time"
)

func TestManagerIssueAndVerifyProjectScopedToken(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	manager, err := New("01234567890123456789012345678901", WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	token, issued, err := manager.Issue(7, 42, "ve_project_1", []string{ScopeRead, ScopeWrite})
	if err != nil {
		t.Fatal(err)
	}
	if issued.UserID != 7 || issued.WorkspaceID != 42 || issued.ProjectID != "ve_project_1" {
		t.Fatalf("unexpected issued claims: %+v", issued)
	}
	verified, err := manager.Verify(token, "ve_project_1", ScopeWrite)
	if err != nil {
		t.Fatal(err)
	}
	if verified.UserID != 7 || verified.WorkspaceID != 42 || verified.ProjectID != "ve_project_1" {
		t.Fatalf("unexpected verified claims: %+v", verified)
	}
	if issued.ExpiresAt-issued.IssuedAt > int64(TokenTTL/time.Second) {
		t.Fatalf("token lifetime exceeds limit: %d", issued.ExpiresAt-issued.IssuedAt)
	}
}

func TestManagerVerifyRejectsProjectScopeAndExpiry(t *testing.T) {
	current := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	manager, err := New("01234567890123456789012345678901", WithClock(func() time.Time { return current }))
	if err != nil {
		t.Fatal(err)
	}
	token, claims, err := manager.Issue(7, 42, "ve_project_1", []string{ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Verify(token, "ve_other", ScopeRead); !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("project mismatch error = %v", err)
	}
	if _, err := manager.Verify(token, "ve_project_1", ScopeWrite); err == nil {
		t.Fatal("missing scope accepted")
	}
	current = time.Unix(claims.ExpiresAt, 0).Add(time.Second)
	if _, err := manager.Verify(token, "ve_project_1", ScopeRead); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired token error = %v", err)
	}
}
