package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestHandleCreateYouTubeEditorSession_EditorUnconfiguredFailsFast pins
// the fail-fast contract on the Modifica flow: when INSTAEDITOR_URL is
// missing, POST /api/v1/youtube/editor-sessions returns 503 BEFORE any
// repository/provider mutation — no orphan session may be created for
// an editor that cannot be opened. The gate sits after the workspace
// ownership check, so a foreign-tenant probe still gets the canonical
// 404-as-foreign instead of leaking editor configuration state.
func TestHandleCreateYouTubeEditorSession_EditorUnconfiguredFailsFast(t *testing.T) {
	t.Parallel()
	const workspaceID, accountID int64 = 11, 22
	const videoID = "yt-no-editor"
	const channelID = "channel-no-editor"

	row := fakeEditableRow(workspaceID, accountID, videoID, "sess-no-editor", "ve_no_editor")
	row.DesiredPrivacy = "private"

	var storeInvoked bool
	router := buildFindOrCreateRouter(t,
		&models.Workspace{ID: workspaceID, OwnerID: 1}, accountID, channelID,
		func(ctx context.Context, wsID, aID int64, vid, _, _ string) (*models.YouTubeVideoEdit, error) {
			storeInvoked = true
			return row, nil
		},
	)
	router.editorURL = "" // simulate a missing INSTAEDITOR_URL

	body := fmt.Sprintf(`{"workspace_id":%d,"platform_account_id":%d,"youtube_video_id":%q}`,
		workspaceID, accountID, videoID)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	rec := httptest.NewRecorder()
	router.Setup().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	if storeInvoked {
		t.Fatal("session store must not be invoked when the editor is not configured")
	}

	// Cross-tenant probe: the ownership gate must win over the editor
	// gate — a caller who does not own the workspace gets the canonical
	// 404-as-foreign, NOT a 503 that would leak editor configuration
	// state (the reason the gate sits after the ownership check).
	storeInvoked = false
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req2, 9999) // does NOT own workspace 11
	rec2 := httptest.NewRecorder()
	router.Setup().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant probe: want 404 (404-as-foreign wins over 503), got %d body=%s", rec2.Code, rec2.Body.String())
	}
	if storeInvoked {
		t.Fatal("session store must not be invoked for a cross-tenant probe")
	}
}

// TestEditorURLForProject_EmptyWhenUnconfigured pins the launcher
// contract: with no INSTAEDITOR_URL, editorURLForProject must return ""
// — the API must never fabricate an editor destination (no frontend
// fallback, no hardcoded host). With a URL configured it must trim and
// path-escape the project handle.
func TestEditorURLForProject_EmptyWhenUnconfigured(t *testing.T) {
	t.Parallel()
	r := &Router{}
	if got := r.editorURLForProject("ve_abc"); got != "" {
		t.Fatalf("want empty editor_url when unconfigured, got %q", got)
	}
	r = &Router{editorURL: "  https://editor.example.test/  "}
	if got := r.editorURLForProject("ve_abc"); got != "https://editor.example.test/editor/ve_abc" {
		t.Fatalf("want trimmed editor_url, got %q", got)
	}
}

// TestHandleCreateThumbnailSession_EditorUnconfigured pins the
// fail-fast contract on the Velox→InstaEdit handoff: with no
// EditorBaseURL the handler returns 503 instead of minting a session
// whose editor_url would be empty.
func TestHandleCreateThumbnailSession_EditorUnconfigured(t *testing.T) {
	store, ws, users := freshHarnessForThumbnail()

	vm := NewVeloxModule(VeloxModuleDeps{
		ExternalDestinationStore: &fakeE2EDestinations{},
		ExternalDeliveryStore:    &fakeE2EDeliveries{rows: map[string]*models.ExternalDelivery{}},
		WorkspaceStore:           ws,
		UserStore:                users,
		YouTubeVideoEditStore:    store,
		VeloxAPIToken:            testVeloxAPIToken,
		// EditorBaseURL intentionally empty: the editor is unavailable.
	})
	chiRouter := chi.NewRouter()
	vm.Register(chiRouter)

	body := `{"workspace_id":12,"platform_account_id":381,"youtube_video_id":"vid"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/thumbnail-sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", buildAuthHeader(testVeloxAPIToken))
	w := httptest.NewRecorder()
	chiRouter.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d body=%s", w.Code, w.Body.String())
	}
	if len(store.rows) != 0 {
		t.Errorf("no session must be minted when the editor is not configured, got %d rows", len(store.rows))
	}
}
