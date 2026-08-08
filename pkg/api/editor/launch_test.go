package editor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/editorlaunch"
)

// fakeLaunchIssuer records Issue inputs and returns canned results.
// The other interface methods panic on use so an unintended call
// surfaces loudly instead of silently passing.
type fakeLaunchIssuer struct {
	issuedUserID    int64
	issuedWorkspace int64
	issuedProjectID string
	issuedScopes    []string
	issueErr        error
}

func (f *fakeLaunchIssuer) Issue(userID, workspaceID int64, projectID string, scopes []string) (string, editorlaunch.Claims, error) {
	f.issuedUserID = userID
	f.issuedWorkspace = workspaceID
	f.issuedProjectID = projectID
	f.issuedScopes = append([]string(nil), scopes...)
	if f.issueErr != nil {
		return "", editorlaunch.Claims{}, f.issueErr
	}
	return "tok-launch-1", editorlaunch.Claims{UserID: userID, WorkspaceID: workspaceID, ProjectID: projectID, ExpiresAt: 1234567890}, nil
}

func (f *fakeLaunchIssuer) IssueSession(userID, workspaceID int64, projectID string, scopes []string) (string, editorlaunch.Claims, error) {
	return "", editorlaunch.Claims{}, errors.New("IssueSession not used in launch gate tests")
}

func (f *fakeLaunchIssuer) Verify(raw, projectID, requiredScope string) (*editorlaunch.Claims, error) {
	return nil, errors.New("Verify not used in launch gate tests")
}

func (f *fakeLaunchIssuer) Consume(raw, projectID, requiredScope string) (*editorlaunch.Claims, error) {
	return nil, errors.New("Consume not used in launch gate tests")
}

func (f *fakeLaunchIssuer) VerifySession(raw, projectID, requiredScope string) (*editorlaunch.Claims, error) {
	return nil, errors.New("VerifySession not used in launch gate tests")
}

// TestLaunchHandlerAuthorizesBeforeIssuing pins the "permissions checked in
// InstaEdit before opening the editor" contract: POST /api/v1/editor/launch
// runs AuthorizeProject (read-only) FIRST and only then mints a project-bound
// launch token for the authenticated identity.
func TestLaunchHandlerAuthorizesBeforeIssuing(t *testing.T) {
	issuer := &fakeLaunchIssuer{}
	var authorizeCalls int
	module := NewEditorBFFModule(Deps{
		AuthorizeProject: func(_ context.Context, userID, workspaceID int64, projectID string, write bool) error {
			authorizeCalls++
			if userID != 7 || workspaceID != 42 || projectID != "ve_abc123" || write {
				t.Fatalf("unexpected authorization input: user=%d workspace=%d project=%q write=%v", userID, workspaceID, projectID, write)
			}
			return nil
		},
	})

	req := identityRequest(httptest.NewRequest(http.MethodPost, LaunchTokenPath, strings.NewReader(`{"project_id":"ve_abc123"}`)))
	rec := httptest.NewRecorder()
	module.launchHandler(issuer).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if authorizeCalls != 1 {
		t.Fatalf("authorize calls = %d, want 1", authorizeCalls)
	}
	if issuer.issuedUserID != 7 || issuer.issuedWorkspace != 42 || issuer.issuedProjectID != "ve_abc123" {
		t.Fatalf("issue inputs = user %d workspace %d project %q", issuer.issuedUserID, issuer.issuedWorkspace, issuer.issuedProjectID)
	}
	var resp launchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token != "tok-launch-1" || resp.ProjectID != "ve_abc123" || resp.WorkspaceID != 42 || resp.UserID != 7 {
		t.Fatalf("response = %+v", resp)
	}
}

// TestLaunchHandlerDeniesUnauthorizedProject pins that an operator who does
// not own the editor project gets 404 and NO launch token is minted — the
// token (and therefore the editor opening) is gated on the authorization
// verdict.
func TestLaunchHandlerDeniesUnauthorizedProject(t *testing.T) {
	issuer := &fakeLaunchIssuer{}
	module := NewEditorBFFModule(Deps{
		AuthorizeProject: func(context.Context, int64, int64, string, bool) error {
			return errors.New("editor project not found")
		},
	})

	req := identityRequest(httptest.NewRequest(http.MethodPost, LaunchTokenPath, strings.NewReader(`{"project_id":"ve_abc123"}`)))
	rec := httptest.NewRecorder()
	module.launchHandler(issuer).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if issuer.issuedProjectID != "" {
		t.Fatal("launch token must not be issued for an unauthorized project")
	}
}

// TestLaunchHandlerFailsClosedWhenAuthorizeProjectNil pins the fail-closed
// contract when the authorization stores are not wired: the launcher refuses
// to mint a token rather than opening the editor unauthenticated.
func TestLaunchHandlerFailsClosedWhenAuthorizeProjectNil(t *testing.T) {
	issuer := &fakeLaunchIssuer{}
	module := NewEditorBFFModule(Deps{}) // AuthorizeProject nil

	req := identityRequest(httptest.NewRequest(http.MethodPost, LaunchTokenPath, strings.NewReader(`{"project_id":"ve_abc123"}`)))
	rec := httptest.NewRecorder()
	module.launchHandler(issuer).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if issuer.issuedProjectID != "" {
		t.Fatal("launch token must not be issued when project authorization is unavailable")
	}
}
