package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// fakeEditorService records CreateProject calls and mirrors the
// DefaultEditorService's create-or-reuse behaviour: it hands back a
// project that echoes the requested external project id (a real bridge
// would already exist or be persisted by the service).
type fakeEditorService struct {
	createProjectFn func(ctx context.Context, req services.CreateEditorProjectRequest) (*services.EditorProject, error)
	openProjectFn   func(ctx context.Context, req services.OpenEditorProjectRequest) (*services.EditorProject, error)
	calls           []services.CreateEditorProjectRequest
	openCalls       []services.OpenEditorProjectRequest
}

func (f *fakeEditorService) CreateProject(ctx context.Context, req services.CreateEditorProjectRequest) (*services.EditorProject, error) {
	f.calls = append(f.calls, req)
	if f.createProjectFn != nil {
		return f.createProjectFn(ctx, req)
	}
	return &services.EditorProject{
		ApplicationProjectID: req.ApplicationProjectID,
		ExternalProjectID:    req.ExternalProjectID,
		WorkspaceID:          req.WorkspaceID,
		State:                "linked",
		Created:              false,
	}, nil
}

func (f *fakeEditorService) OpenProject(ctx context.Context, req services.OpenEditorProjectRequest) (*services.EditorProject, error) {
	f.openCalls = append(f.openCalls, req)
	if f.openProjectFn != nil {
		return f.openProjectFn(ctx, req)
	}
	return nil, services.ErrEditorProjectNotFound
}

func (f *fakeEditorService) GetProjectStatus(context.Context, services.GetEditorProjectStatusRequest) (*services.EditorProjectStatus, error) {
	return nil, services.ErrEditorProjectNotFound
}

func (f *fakeEditorService) RequestRender(context.Context, services.RequestEditorRenderRequest) (*services.EditorRender, error) {
	return nil, services.ErrEditorProjectNotFound
}

// TestCreateEditorSession_EnsuresEditorProjectBridge covers the Action 6
// "Modifica" flow on the user-facing path: after the session row is
// resolved (created or reused), the helper must hand the session's
// opaque velox_project_id to the EditorService so the provider project
// is created-or-reused and the mapping is persisted. Both the CREATE
// and the REUSE path run this same step (the repository already decided
// which one), so this test pins the shared invariant.
func TestCreateEditorSession_EnsuresEditorProjectBridge(t *testing.T) {
	t.Parallel()
	const workspaceID, accountID int64 = 11, 22
	const videoID = "yt-bridge"
	const sessID = "sess-bridge"
	const projID = "ve_bridge_1"
	const channelID = "channel-bridge"

	row := fakeEditableRow(workspaceID, accountID, videoID, sessID, projID)
	row.DesiredPrivacy = "private"

	editorSvc := &fakeEditorService{}
	tps := &thumbnailProjectTestStore{}
	router := buildFindOrCreateRouter(t,
		&models.Workspace{ID: workspaceID, OwnerID: 7}, accountID, channelID,
		func(ctx context.Context, wsID, aID int64, vid, _, _ string) (*models.YouTubeVideoEdit, error) {
			if wsID != workspaceID || aID != accountID || vid != videoID {
				return nil, nil
			}
			return row, nil
		},
		WithEditorService(editorSvc),
		WithThumbnailProjectStore(tps),
	)

	got, _, err := router.CreateEditorSession(context.Background(), CreateEditorSessionInput{
		WorkspaceID:       workspaceID,
		PlatformAccountID: accountID,
		YouTubeVideoID:    videoID,
		UserID:            7,
	})
	if err != nil {
		t.Fatalf("CreateEditorSession: %v", err)
	}
	// The session is the temporary application project: the thumbnail
	// project row that the bridge FK requires must be ensured before
	// the EditorService is asked to create-or-reuse the mapping.
	if tps.ensureCalls != 1 {
		t.Fatalf("EnsureThumbnailProjectForEditorSession calls = %d, want 1", tps.ensureCalls)
	}
	if tps.lastEnsureWorkspaceID != workspaceID || tps.lastEnsureProjectID != sessID || tps.lastEnsureCreatedBy != 7 {
		t.Fatalf("ensure scope mismatch: workspace=%d project=%q createdBy=%d", tps.lastEnsureWorkspaceID, tps.lastEnsureProjectID, tps.lastEnsureCreatedBy)
	}
	// The session's stable identity (and therefore the editor_url) must
	// survive the bridge step untouched.
	if got.ID != sessID || got.VeloxProjectID != projID {
		t.Fatalf("session identity changed: id=%s project=%s", got.ID, got.VeloxProjectID)
	}
	if len(editorSvc.calls) != 1 {
		t.Fatalf("CreateProject calls = %d, want 1", len(editorSvc.calls))
	}
	call := editorSvc.calls[0]
	if call.UserID != 7 || call.WorkspaceID != workspaceID ||
		call.ApplicationProjectID != sessID || call.ExternalProjectID != projID {
		t.Fatalf("CreateProject request identity wrong: %+v", call)
	}
	if call.ExternalProjectID != projID || call.ApplicationProjectID != sessID {
		t.Fatalf("CreateProject must carry only the session/project mapping: %+v", call)
	}
}

// TestCreateEditorSession_BridgeSkippedForBackgroundCaller pins the
// degraded path: background callers (processing reconciler, thumbnail
// batches, Velox service-to-service handoff) pass UserID=0 and the
// helper must NOT touch the EditorService. The mapping is minted lazily
// on the first operator open (idempotent REUSE), so skipping here is by
// design, not a gap.
func TestCreateEditorSession_BridgeSkippedForBackgroundCaller(t *testing.T) {
	t.Parallel()
	const workspaceID, accountID int64 = 11, 22
	const videoID = "yt-bridge-bg"
	const channelID = "channel-bridge-bg"

	row := fakeEditableRow(workspaceID, accountID, videoID, "sess-bg", "ve_bridge_bg")
	row.DesiredPrivacy = "private"

	editorSvc := &fakeEditorService{}
	tps := &thumbnailProjectTestStore{}
	router := buildFindOrCreateRouter(t,
		&models.Workspace{ID: workspaceID, OwnerID: 7}, accountID, channelID,
		func(ctx context.Context, wsID, aID int64, vid, _, _ string) (*models.YouTubeVideoEdit, error) {
			return row, nil
		},
		WithEditorService(editorSvc),
		WithThumbnailProjectStore(tps),
	)

	got, _, err := router.CreateEditorSession(context.Background(), CreateEditorSessionInput{
		WorkspaceID:       workspaceID,
		PlatformAccountID: accountID,
		YouTubeVideoID:    videoID,
	})
	if err != nil {
		t.Fatalf("CreateEditorSession: %v", err)
	}
	if got.VeloxProjectID != "ve_bridge_bg" {
		t.Fatalf("unexpected project id: %s", got.VeloxProjectID)
	}
	if len(editorSvc.calls) != 0 {
		t.Fatalf("CreateProject calls = %d, want 0 for background callers", len(editorSvc.calls))
	}
	if tps.ensureCalls != 0 {
		t.Fatalf("background callers must not mint application projects, got %d ensure calls", tps.ensureCalls)
	}
}

// TestCreateEditorSession_BridgeProjectEnsureFailureFailsClick: a
// failure to mint the application project row must fail the whole
// Modifica click (the bridge FK could not be satisfied) and stay typed
// so the handler maps it like any other bridge error.
func TestCreateEditorSession_BridgeProjectEnsureFailureFailsClick(t *testing.T) {
	t.Parallel()
	const workspaceID, accountID int64 = 11, 22
	const videoID = "yt-bridge-ensure-err"
	const channelID = "channel-bridge-ensure-err"

	row := fakeEditableRow(workspaceID, accountID, videoID, "sess-ensure-err", "ve_bridge_ensure_err")
	row.DesiredPrivacy = "private"

	editorSvc := &fakeEditorService{}
	tps := &thumbnailProjectTestStore{ensureErr: errors.New("ensure boom")}
	router := buildFindOrCreateRouter(t,
		&models.Workspace{ID: workspaceID, OwnerID: 7}, accountID, channelID,
		func(ctx context.Context, wsID, aID int64, vid, _, _ string) (*models.YouTubeVideoEdit, error) {
			return row, nil
		},
		WithEditorService(editorSvc),
		WithThumbnailProjectStore(tps),
	)

	_, _, err := router.CreateEditorSession(context.Background(), CreateEditorSessionInput{
		WorkspaceID:       workspaceID,
		PlatformAccountID: accountID,
		YouTubeVideoID:    videoID,
		UserID:            7,
	})
	if err == nil || !strings.Contains(err.Error(), "ensure boom") {
		t.Fatalf("want ensure failure propagated, got %v", err)
	}
	if len(editorSvc.calls) != 0 {
		t.Fatalf("CreateProject must not run after ensure failure, calls = %d", len(editorSvc.calls))
	}
}

// TestCreateEditorSession_BridgeFailurePropagates: a provider-side
// failure of the mapping step must fail the whole Modifica click (no
// editor_url pointing at an unmapped project) and stay typed so the
// handler can map it.
func TestCreateEditorSession_BridgeFailurePropagates(t *testing.T) {
	t.Parallel()
	const workspaceID, accountID int64 = 11, 22
	const videoID = "yt-bridge-err"
	const channelID = "channel-bridge-err"

	row := fakeEditableRow(workspaceID, accountID, videoID, "sess-err", "ve_bridge_err")
	row.DesiredPrivacy = "private"

	editorSvc := &fakeEditorService{
		createProjectFn: func(ctx context.Context, req services.CreateEditorProjectRequest) (*services.EditorProject, error) {
			return nil, services.ErrEditorProjectInvalid
		},
	}
	router := buildFindOrCreateRouter(t,
		&models.Workspace{ID: workspaceID, OwnerID: 7}, accountID, channelID,
		func(ctx context.Context, wsID, aID int64, vid, _, _ string) (*models.YouTubeVideoEdit, error) {
			return row, nil
		},
		WithEditorService(editorSvc),
	)

	_, _, err := router.CreateEditorSession(context.Background(), CreateEditorSessionInput{
		WorkspaceID:       workspaceID,
		PlatformAccountID: accountID,
		YouTubeVideoID:    videoID,
		UserID:            7,
	})
	if err == nil || !errors.Is(err, services.ErrEditorProjectInvalid) {
		t.Fatalf("want wrapped ErrEditorProjectInvalid, got %v", err)
	}
}

// TestCreateEditorSession_BridgeMismatchRejected: if the editor service
// ever resolved a different external project than the session owns, the
// helper must reject it — the operator must never be redirected to a
// foreign project through a stale/racy bridge.
func TestCreateEditorSession_BridgeMismatchRejected(t *testing.T) {
	t.Parallel()
	const workspaceID, accountID int64 = 11, 22
	const videoID = "yt-bridge-mismatch"
	const channelID = "channel-bridge-mismatch"

	row := fakeEditableRow(workspaceID, accountID, videoID, "sess-mismatch", "ve_bridge_mine")
	row.DesiredPrivacy = "private"

	editorSvc := &fakeEditorService{
		createProjectFn: func(ctx context.Context, req services.CreateEditorProjectRequest) (*services.EditorProject, error) {
			return &services.EditorProject{
				ApplicationProjectID: req.ApplicationProjectID,
				ExternalProjectID:    "ve_foreign",
				WorkspaceID:          req.WorkspaceID,
			}, nil
		},
	}
	router := buildFindOrCreateRouter(t,
		&models.Workspace{ID: workspaceID, OwnerID: 7}, accountID, channelID,
		func(ctx context.Context, wsID, aID int64, vid, _, _ string) (*models.YouTubeVideoEdit, error) {
			return row, nil
		},
		WithEditorService(editorSvc),
	)

	_, _, err := router.CreateEditorSession(context.Background(), CreateEditorSessionInput{
		WorkspaceID:       workspaceID,
		PlatformAccountID: accountID,
		YouTubeVideoID:    videoID,
		UserID:            7,
	})
	if err == nil || !errors.Is(err, services.ErrEditorProjectInvalid) {
		t.Fatalf("want ErrEditorProjectInvalid on foreign handle, got %v", err)
	}
}
