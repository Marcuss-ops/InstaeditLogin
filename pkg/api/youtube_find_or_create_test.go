package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeEditableRow returns a *models.YouTubeVideoEdit seeded for the
// given triple so the FindOrCreate mock test only varies the unique
// fields.
func fakeEditableRow(workspaceID, accountID int64, videoID, id, projectID string) *models.YouTubeVideoEdit {
	return &models.YouTubeVideoEdit{
		ID:                id,
		WorkspaceID:       workspaceID,
		PlatformAccountID: accountID,
		YouTubeVideoID:    videoID,
		VeloxProjectID:    projectID,
		Status:            "editing",
	}
}

// buildFindOrCreateRouter wires a Router with the findOrCreateFn stub
// preinstalled. Uses newPublishRouter (the existing shared factory) +
// the just-added WithUserStore option so the wiring stays declarative
// (no post-construction mutation of *Router fields).
func buildFindOrCreateRouter(
	t *testing.T,
	workspace *models.Workspace,
	accountID int64,
	channelID string,
	findOrCreate func(ctx context.Context, wsID, aID int64, videoID, sessHint, projHint string) (*models.YouTubeVideoEdit, error),
) *Router {
	t.Helper()
	channelOut := &models.WorkspaceChannel{WorkspaceID: workspace.ID, PlatformAccountID: accountID}

	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id == accountID {
				return &models.PlatformAccount{
					ID:             accountID,
					Platform:       models.PlatformYouTube,
					PlatformUserID: channelID,
				}, nil
			}
			return nil, nil
		},
	}

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				VideoID:      videoID,
				ChannelID:    channelID,
				UploadStatus: "processed",
				Privacy:      "private",
			}, nil
		},
	}

	editStore := &mockYouTubeVideoEditStore{
		findOrCreateFn: findOrCreate,
		listByAccountsFn: func(ctx context.Context, wsID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
			return nil, nil
		},
	}

	return newPublishRouter(t, workspace, editStore,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == workspace.ID {
					return workspace, nil
				}
				return nil, nil
			},
			findChannelFn: func(ctx context.Context, wsID, aID int64) (*models.WorkspaceChannel, error) {
				if wsID == workspace.ID && aID == accountID {
					return channelOut, nil
				}
				return nil, nil
			},
		}),
		WithUserStore(userStore),
		WithCredentialVault(&mockCredentialVault{
			getFn: func(ctx context.Context, accountID int64, kind string) (*models.OAuthToken, error) {
				return &models.OAuthToken{AccessToken: "test-token", Type: kind}, nil
			},
		}),
		WithYouTubeService(youTubeSvc),
	)
}

// TestCreateEditorSession_FindOrCreate_Idempotent verifies the helper
// returns the SAME row across two consecutive calls with the same
// (workspace, account, video) triple. P0#3 contract: an operator
// clicking the same dark-card twice lands on the same Dark Editor URL
// (same velox_project_id) instead of getting a fresh session per click.
func TestCreateEditorSession_FindOrCreate_Idempotent(t *testing.T) {
	t.Parallel()
	const workspaceID, accountID int64 = 11, 22
	const videoID = "yt-abc"
	const sessID = "sess-1"
	const projID = "vp-1"
	const channelID = "channel-abc"

	row := fakeEditableRow(workspaceID, accountID, videoID, sessID, projID)
	row.DesiredPrivacy = "private"

	router := buildFindOrCreateRouter(t,
		&models.Workspace{ID: workspaceID, OwnerID: 1}, accountID, channelID,
		func(ctx context.Context, wsID, aID int64, vid, _, _ string) (*models.YouTubeVideoEdit, error) {
			if wsID != workspaceID || aID != accountID || vid != videoID {
				return nil, nil
			}
			return row, nil
		},
	)

	ctx := context.Background()
	first, err := router.CreateEditorSession(ctx, CreateEditorSessionInput{
		WorkspaceID:       workspaceID,
		PlatformAccountID: accountID,
		YouTubeVideoID:    videoID,
	})
	if err != nil {
		t.Fatalf("first CreateEditorSession: %v", err)
	}
	if first.ID != sessID || first.VeloxProjectID != projID {
		t.Fatalf("first call returned wrong identity: id=%s project=%s", first.ID, first.VeloxProjectID)
	}

	second, err := router.CreateEditorSession(ctx, CreateEditorSessionInput{
		WorkspaceID:       workspaceID,
		PlatformAccountID: accountID,
		YouTubeVideoID:    videoID,
	})
	if err != nil {
		t.Fatalf("second CreateEditorSession: %v", err)
	}
	if second.ID != sessID {
		t.Fatalf("idempotency violated: first=%s second=%s", first.ID, second.ID)
	}
	if second.VeloxProjectID != projID {
		t.Fatalf("velox_project_id regression: first=%s second=%s", first.VeloxProjectID, second.VeloxProjectID)
	}
}

// TestCreateEditorSession_FindOrCreate_StampsSourceThumbnailOnCreateOnly
// covers the one-shot SourceThumbnailURL stamp: the helper stamps the
// operator-supplied URL ONLY on the CREATE path (SourceThumbnailURL was
// empty). Subsequent clicks must NOT overwrite an existing URL, so the
// "first click wins" contract is enforced.
func TestCreateEditorSession_FindOrCreate_StampsSourceThumbnailOnCreateOnly(t *testing.T) {
	t.Parallel()
	const workspaceID, accountID int64 = 11, 22
	const videoID = "yt-thumbnail"
	const sessID = "sess-thumb"
	const projID = "vp-thumb"
	const existingURL = "https://existing.example.com/thumb.jpg"
	const secondClickURL = "https://second.example.com/thumb.jpg"
	const channelID = "channel-thumb"

	row := fakeEditableRow(workspaceID, accountID, videoID, sessID, projID)
	row.SourceThumbnailURL = existingURL

	router := buildFindOrCreateRouter(t,
		&models.Workspace{ID: workspaceID, OwnerID: 1}, accountID, channelID,
		func(ctx context.Context, wsID, aID int64, vid, _, _ string) (*models.YouTubeVideoEdit, error) {
			return row, nil
		},
	)

	ctx := context.Background()
	got, err := router.CreateEditorSession(ctx, CreateEditorSessionInput{
		WorkspaceID:        workspaceID,
		PlatformAccountID:  accountID,
		YouTubeVideoID:     videoID,
		SourceThumbnailURL: secondClickURL,
	})
	if err != nil {
		t.Fatalf("CreateEditorSession: %v", err)
	}
	if got.SourceThumbnailURL != existingURL {
		t.Fatalf("SourceThumbnailURL must not be overwritten on REUSE (first-click-wins); got %q want %q",
			got.SourceThumbnailURL, existingURL)
	}
}

// TestCreateEditorSession_FindOrCreate_ConcurrentRaceModelsPartialUnique
// models the partial-UNIQUE race: N goroutines call FindOrCreate for the
// same triple simultaneously. The helper must converge on a single
// identity. Mirrors the SQL behaviour where the second goroutine hits
// the 23505 race-loser branch and re-selects the winner's row.
func TestCreateEditorSession_FindOrCreate_ConcurrentRaceModelsPartialUnique(t *testing.T) {
	t.Parallel()
	const workspaceID, accountID int64 = 11, 22
	const videoID = "yt-race"
	const channelID = "channel-race"

	winner := fakeEditableRow(workspaceID, accountID, videoID, "race-winner", "vp-winner")
	winner.DesiredPrivacy = "private"

	var calls int32
	router := buildFindOrCreateRouter(t,
		&models.Workspace{ID: workspaceID, OwnerID: 1}, accountID, channelID,
		func(ctx context.Context, wsID, aID int64, vid, _, _ string) (*models.YouTubeVideoEdit, error) {
			atomic.AddInt32(&calls, 1)
			if wsID != workspaceID || aID != accountID || vid != videoID {
				return nil, nil
			}
			return winner, nil
		},
	)

	const N = 16
	var wg sync.WaitGroup
	ids := make([]string, N)
	projects := make([]string, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			row, err := router.CreateEditorSession(context.Background(), CreateEditorSessionInput{
				WorkspaceID:       workspaceID,
				PlatformAccountID: accountID,
				YouTubeVideoID:    videoID,
			})
			if err != nil {
				t.Errorf("goroutine %d: %v", idx, err)
				return
			}
			ids[idx] = row.ID
			projects[idx] = row.VeloxProjectID
		}(i)
	}
	wg.Wait()

	for i, id := range ids {
		if id != "race-winner" {
			t.Fatalf("goroutine %d saw identity=%s; expected race-winner (idempotency violated)", i, id)
		}
	}
	for i, p := range projects {
		if p != "vp-winner" {
			t.Fatalf("goroutine %d saw project=%s; expected vp-winner", i, p)
		}
	}
	if got := atomic.LoadInt32(&calls); got != N {
		t.Fatalf("findOrCreateFn expected %d calls, got %d", N, got)
	}
}

// TestCreateEditorSession_FindOrCreate_NewProjectOnTerminalStatus ensures
// that, after a session lands in 'published' (terminal), the next click
// can still mint a NEW row for the same triple. The partial UNIQUE INDEX
// `uniq_youtube_video_edits_open_session` (migration 071) deliberately
// excludes 'published' so the existing row no longer blocks a fresh
// INSERT for the same (workspace, account, video) tuple.
func TestCreateEditorSession_FindOrCreate_NewProjectOnTerminalStatus(t *testing.T) {
	t.Parallel()
	const workspaceID, accountID int64 = 11, 22
	const videoID = "yt-republish"
	const channelID = "channel-republish"

	newRow := fakeEditableRow(workspaceID, accountID, videoID, "sess-new", "vp-new")
	newRow.Status = "editing"

	router := buildFindOrCreateRouter(t,
		&models.Workspace{ID: workspaceID, OwnerID: 1}, accountID, channelID,
		func(ctx context.Context, wsID, aID int64, vid, _, _ string) (*models.YouTubeVideoEdit, error) {
			if wsID != workspaceID || aID != accountID || vid != videoID {
				return nil, nil
			}
			// Simulate the repository's behaviour once the old row
			// has moved to 'published': the partial UNIQUE no longer
			// matches, so the helper inserts a fresh session and
			// returns the new row.
			return newRow, nil
		},
	)

	row, err := router.CreateEditorSession(context.Background(), CreateEditorSessionInput{
		WorkspaceID:       workspaceID,
		PlatformAccountID: accountID,
		YouTubeVideoID:    videoID,
	})
	if err != nil {
		t.Fatalf("CreateEditorSession: %v", err)
	}
	if row.ID != "sess-new" {
		t.Fatalf("expected fresh row id after published-then-reclick, got %s", row.ID)
	}
	if row.VeloxProjectID != "vp-new" {
		t.Fatalf("expected fresh velox_project_id, got %s", row.VeloxProjectID)
	}
}
