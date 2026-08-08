package api

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type authorizationTeamStore struct {
	role string
	err  error
}

func (s authorizationTeamStore) AddMember(int64, int64, string) error                { return nil }
func (s authorizationTeamStore) RemoveMember(int64, int64) error                     { return nil }
func (s authorizationTeamStore) ListMembers(int64) ([]models.WorkspaceMember, error) { return nil, nil }
func (s authorizationTeamStore) GetRole(int64, int64) (string, error)                { return s.role, s.err }
func (s authorizationTeamStore) IsAdmin(int64, int64) (bool, error)                  { return s.role == "admin", s.err }
func (s authorizationTeamStore) CreateInvite(int64, int64, string, string) (*models.WorkspaceInvite, error) {
	return nil, nil
}
func (s authorizationTeamStore) FindInviteByToken(string) (*models.WorkspaceInvite, error) {
	return nil, nil
}
func (s authorizationTeamStore) AcceptInvite(string, int64) error { return nil }

func TestWorkspaceRoleAllowed_OwnerAndTeamRoles(t *testing.T) {
	workspace := &models.Workspace{ID: 42, OwnerID: 1}
	cases := []struct {
		name string
		user int64
		role string
		min  string
		want bool
	}{
		{"owner can read", 1, "", workspaceRoleViewer, true},
		{"owner can write", 1, "", workspaceRoleEditor, true},
		{"viewer can read", 2, "viewer", workspaceRoleViewer, true},
		{"viewer cannot write", 2, "viewer", workspaceRoleEditor, false},
		{"editor can read", 3, "editor", workspaceRoleViewer, true},
		{"editor can write", 3, "editor", workspaceRoleEditor, true},
		{"admin can write", 4, "admin", workspaceRoleEditor, true},
		{"unknown member denied", 5, "", workspaceRoleViewer, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := workspaceRoleAllowed(tc.user, workspace, authorizationTeamStore{role: tc.role}, tc.min); got != tc.want {
				t.Fatalf("workspaceRoleAllowed() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkspaceRoleAllowed_FailsClosedOnMissingOrBrokenMembershipStore(t *testing.T) {
	workspace := &models.Workspace{ID: 42, OwnerID: 1}
	if workspaceRoleAllowed(2, workspace, nil, workspaceRoleViewer) {
		t.Fatal("non-owner must be denied when TeamStore is not configured")
	}
	if workspaceRoleAllowed(2, workspace, authorizationTeamStore{role: "admin", err: errors.New("database unavailable")}, workspaceRoleViewer) {
		t.Fatal("membership lookup errors must fail closed")
	}
	if workspaceRoleAllowed(2, nil, authorizationTeamStore{role: "admin"}, workspaceRoleViewer) {
		t.Fatal("nil workspace must be denied")
	}
	if workspaceRoleAllowed(0, workspace, authorizationTeamStore{role: "admin"}, workspaceRoleViewer) {
		t.Fatal("invalid user id must be denied")
	}
}
