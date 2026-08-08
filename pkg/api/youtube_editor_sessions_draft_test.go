package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// runPutDraft wires a Router with the supplied mocks + the test
// identity, then issues PUT against
// /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/draft.
// Identity is injected via context (no JWT middleware — same pattern
// as runGetEditorSessionByID).
func runPutDraft(
	t *testing.T,
	editStore *mockYouTubeVideoEditStore,
	ws *fakeWorkspaceStoreForSessionGet,
	identity auth.Identity,
	projectID string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	r := &Router{
		youtubeVideoEditStore: editStore,
		workspaceStore:        ws,
		editorURL:             "https://editor.instaedit.test",
	}
	mux := chi.NewRouter()
	mux.Method(http.MethodPut, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/draft", http.HandlerFunc(r.handleSaveEditorSessionDraftByProject))
	req := httptest.NewRequest(http.MethodPut, "/api/v1/youtube/editor-sessions/by-project/"+projectID+"/draft", strings.NewReader(body))
	if identity != nil {
		req = req.WithContext(withIdentity(req.Context(), identity))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// draftStoreRig builds the mock store pre-wired for the partial-merge
// scenarios:
//   - findByProjectFn resolves a session in workspace 12 owned by 42
//     (so the ownership pre-flight passes for uid 42);
//   - findDraftFn returns an existing draft with title "Old Title",
//     description "Old Description", tags [old], language "it",
//     privacy "private";
//   - saveDraftFn captures the merged values the handler persisted.
//
// Returns the store + a pointer to the captured SaveDraft arguments.
func draftStoreRig(t *testing.T) (*mockYouTubeVideoEditStore, *capturedSaveDraft) {
	t.Helper()
	captured := &capturedSaveDraft{}
	store := &mockYouTubeVideoEditStore{
		findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:             "session-1",
				WorkspaceID:    12,
				VeloxProjectID: projectID,
				Status:         "editing",
			}, nil
		},
		findDraftFn: func(_ context.Context, _ string) (*models.YouTubeVideoEdit, error) {
			title := "Old Title"
			description := "Old Description"
			lang := "it"
			privacy := "private"
			scheduled := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
			return &models.YouTubeVideoEdit{
				DraftTitle:           &title,
				DraftDescription:     &description,
				DraftTags:            []string{"old"},
				DraftDefaultLanguage: &lang,
				DraftDesiredPrivacy:  &privacy,
				DraftTranslations:    map[string]models.YouTubeTranslation{"en": {Title: "Old EN", Description: "Old body"}},
				DraftPublishAt:       &scheduled,
			}, nil
		},
		saveDraftFn: func(_ context.Context, id string, title string, description string, tags []string, defaultLanguage string, defaultAudioLanguage string, translations map[string]models.YouTubeTranslation, desiredPrivacy string, publishAt *time.Time, _ time.Time) error {
			captured.ID = id
			captured.Title = title
			captured.Description = description
			captured.Tags = tags
			captured.DefaultLanguage = defaultLanguage
			captured.DefaultAudioLanguage = defaultAudioLanguage
			captured.Translations = translations
			captured.DesiredPrivacy = desiredPrivacy
			captured.PublishAt = publishAt
			return nil
		},
	}
	return store, captured
}

// capturedSaveDraft mirrors the SaveDraft argument list so tests can
// assert what the handler actually merged.
type capturedSaveDraft struct {
	ID                   string
	Title                string
	Description          string
	Tags                 []string
	DefaultLanguage      string
	DefaultAudioLanguage string
	Translations         map[string]models.YouTubeTranslation
	DesiredPrivacy       string
	PublishAt            *time.Time
}

func draftWorkspaceRig() *fakeWorkspaceStoreForSessionGet {
	ws := newFakeWorkspaceStoreForSessionGet()
	ws.rows[12] = &models.Workspace{ID: 12, OwnerID: 42, Name: "ws"}
	return ws
}

// TestPutDraft_PartialRenameKeepsOtherFields pins the exact
// behaviour the InstaEditor rename-pill sync depends on: a body with
// ONLY {"title"} must merge — description/tags/language/privacy keep
// their current draft values instead of being wiped.
func TestPutDraft_PartialRenameKeepsOtherFields(t *testing.T) {
	store, captured := draftStoreRig(t)
	ws := draftWorkspaceRig()

	w := runPutDraft(t, store, ws, &fakeAuthIdentity{uid: 42}, "ve_project-1", `{"title":"New Name"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if captured.Title != "New Name" {
		t.Errorf("title: want merged New Name, got %q", captured.Title)
	}
	if captured.Description != "Old Description" {
		t.Errorf("description: want preserved, got %q", captured.Description)
	}
	if len(captured.Tags) != 1 || captured.Tags[0] != "old" {
		t.Errorf("tags: want preserved [old], got %v", captured.Tags)
	}
	if captured.DefaultLanguage != "it" {
		t.Errorf("default_language: want preserved it, got %q", captured.DefaultLanguage)
	}
	if captured.DesiredPrivacy != "private" {
		t.Errorf("desired_privacy: want preserved private, got %q", captured.DesiredPrivacy)
	}
	if len(captured.Translations) != 1 || captured.Translations["en"].Title != "Old EN" {
		t.Errorf("translations: want preserved en/Old EN, got %v", captured.Translations)
	}
	if captured.PublishAt == nil || !captured.PublishAt.Equal(time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("publish_at: want preserved draft schedule, got %v", captured.PublishAt)
	}

	// Response echoes the merged draft (the SPA's indicator reads it
	// without a follow-up GET).
	var resp youTubeEditorSessionDraftResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, w.Body.String())
	}
	if resp.DraftTitle != "New Name" {
		t.Errorf("response draft_title: want New Name, got %q", resp.DraftTitle)
	}
	if resp.VeloxProjectID != "ve_project-1" {
		t.Errorf("response velox_project_id: want ve_project-1, got %q", resp.VeloxProjectID)
	}
	if resp.DraftUpdatedAt.IsZero() {
		t.Errorf("response draft_updated_at: want server-stamped time, got zero")
	}
}

// TestPutDraft_EmptyBodyIsNoOp verifies the empty-body contract: `{}`
// (or an entirely empty body) is a no-op — every absent field keeps
// its current draft value (previous behaviour wiped everything).
func TestPutDraft_EmptyBodyIsNoOp(t *testing.T) {
	store, captured := draftStoreRig(t)
	ws := draftWorkspaceRig()

	w := runPutDraft(t, store, ws, &fakeAuthIdentity{uid: 42}, "ve_project-1", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if captured.Title != "Old Title" {
		t.Errorf("title: want preserved Old Title, got %q", captured.Title)
	}
	if captured.Description != "Old Description" {
		t.Errorf("description: want preserved, got %q", captured.Description)
	}
	if captured.DefaultLanguage != "it" {
		t.Errorf("default_language: want preserved it, got %q", captured.DefaultLanguage)
	}
}

// TestPutDraft_ExplicitEmptyStringClearsField verifies that a
// PRESENT field with an empty string still writes the empty value
// (operator intent "I cleared the title") — distinct from an absent
// field, which keeps the current draft.
func TestPutDraft_ExplicitEmptyStringClearsField(t *testing.T) {
	store, captured := draftStoreRig(t)
	ws := draftWorkspaceRig()

	w := runPutDraft(t, store, ws, &fakeAuthIdentity{uid: 42}, "ve_project-1", `{"title":"","description":"New Description"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if captured.Title != "" {
		t.Errorf("title: want explicit empty (cleared), got %q", captured.Title)
	}
	if captured.Description != "New Description" {
		t.Errorf("description: want New Description, got %q", captured.Description)
	}
	// Absent fields still preserved.
	if captured.DefaultLanguage != "it" {
		t.Errorf("default_language: want preserved it, got %q", captured.DefaultLanguage)
	}
}

// TestPutDraft_FullPayloadOverwritesEverything verifies the legacy
// all-fields caller (SPA full form save, editor auto-save) still
// behaves identically: every supplied field is written verbatim.
func TestPutDraft_FullPayloadOverwritesEverything(t *testing.T) {
	store, captured := draftStoreRig(t)
	ws := draftWorkspaceRig()

	body := `{"title":"T","description":"D","tags":["a","b"],"default_language":"en","default_audio_language":"fr","desired_privacy":"public"}`
	w := runPutDraft(t, store, ws, &fakeAuthIdentity{uid: 42}, "ve_project-1", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if captured.Title != "T" || captured.Description != "D" {
		t.Errorf("title/description: want T/D, got %q/%q", captured.Title, captured.Description)
	}
	if len(captured.Tags) != 2 || captured.Tags[0] != "a" || captured.Tags[1] != "b" {
		t.Errorf("tags: want [a b], got %v", captured.Tags)
	}
	if captured.DefaultLanguage != "en" || captured.DefaultAudioLanguage != "fr" {
		t.Errorf("languages: want en/fr, got %q/%q", captured.DefaultLanguage, captured.DefaultAudioLanguage)
	}
	if captured.DesiredPrivacy != "public" {
		t.Errorf("desired_privacy: want public, got %q", captured.DesiredPrivacy)
	}
}

// TestPutDraft_ExplicitPublishAtOverwritesDraftSchedule verifies
// that a PRESENT publish_at replaces the draft's own scheduling
// value (the operator rescheduling from the form), while absent
// publish_at keeps it (covered by the partial-rename test).
func TestPutDraft_ExplicitPublishAtOverwritesDraftSchedule(t *testing.T) {
	store, captured := draftStoreRig(t)
	ws := draftWorkspaceRig()

	newTime := time.Date(2026, 8, 15, 9, 30, 0, 0, time.UTC).Format(time.RFC3339)
	w := runPutDraft(t, store, ws, &fakeAuthIdentity{uid: 42}, "ve_project-1", `{"title":"Renamed","publish_at":"`+newTime+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if captured.Title != "Renamed" {
		t.Errorf("title: want Renamed, got %q", captured.Title)
	}
	if captured.PublishAt == nil || !captured.PublishAt.Equal(time.Date(2026, 8, 15, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("publish_at: want explicit schedule 2026-08-15T09:30Z, got %v", captured.PublishAt)
	}
}

// TestPutDraft_MissingIdentity verifies the 401 gate.
func TestPutDraft_MissingIdentity(t *testing.T) {
	store, _ := draftStoreRig(t)
	ws := draftWorkspaceRig()
	w := runPutDraft(t, store, ws, nil, "ve_project-1", `{"title":"x"}`)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing identity: want 401, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestPutDraft_SessionNotFound verifies the 404 for an unknown
// project id (session lookup precedes the draft merge).
func TestPutDraft_SessionNotFound(t *testing.T) {
	store := &mockYouTubeVideoEditStore{
		findByProjectFn: func(_ context.Context, _ string) (*models.YouTubeVideoEdit, error) {
			return nil, nil
		},
	}
	ws := draftWorkspaceRig()
	w := runPutDraft(t, store, ws, &fakeAuthIdentity{uid: 42}, "ve_unknown", `{"title":"x"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown session: want 404, got %d body=%s", w.Code, w.Body.String())
	}
}
