package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestParsePostListPage_Contract(t *testing.T) {
	created := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	cursor := encodeListCursorForContext("posts", "workspace_id=7", created, "42")

	limit, afterTime, afterID, hasCursor, err := parsePostListPage(url.Values{
		"limit":  []string{"25"},
		"cursor": []string{cursor},
	}, "workspace_id=7")
	if err != nil {
		t.Fatalf("parsePostListPage: %v", err)
	}
	if limit != 25 || !hasCursor || afterID != 42 || !afterTime.Equal(created) {
		t.Fatalf("unexpected page: limit=%d time=%v id=%d hasCursor=%v", limit, afterTime, afterID, hasCursor)
	}

	if _, _, _, _, err := parsePostListPage(url.Values{"cursor": []string{cursor}}, "workspace_id=8"); err == nil {
		t.Fatal("cursor with different filter context must fail")
	}

	nullCursor := encodeListCursorForContext("posts", "workspace_id=7", time.Time{}, "42")
	if _, _, _, _, err := parsePostListPage(url.Values{"cursor": []string{nullCursor}}, "workspace_id=7"); err == nil {
		t.Fatal("cursor without timestamp must fail")
	}
}

func TestParsePostListPage_EmptyAndInvalidLimit(t *testing.T) {
	limit, afterTime, afterID, hasCursor, err := parsePostListPage(url.Values{}, "workspace_id=7")
	if err != nil || limit != 50 || afterTime != nil || afterID != 0 || hasCursor {
		t.Fatalf("empty page: limit=%d time=%v id=%d hasCursor=%v err=%v", limit, afterTime, afterID, hasCursor, err)
	}
	if _, _, _, _, err := parsePostListPage(url.Values{"limit": []string{"101"}}, "workspace_id=7"); err == nil {
		t.Fatal("limit above endpoint maximum must fail")
	}
}

func TestPostWorkspaceOwnedByUser_ClosedOnErrorsAndCrossOwner(t *testing.T) {
	post := &models.Post{WorkspaceID: 7}
	cases := []struct {
		name string
		ws   WorkspaceStore
		want bool
	}{
		{
			name: "owner",
			ws: &mockWorkspaceStore{findByIDFn: func(int64) (*models.Workspace, error) {
				return &models.Workspace{ID: 7, OwnerID: 9}, nil
			}},
			want: true,
		},
		{
			name: "cross_owner",
			ws: &mockWorkspaceStore{findByIDFn: func(int64) (*models.Workspace, error) {
				return &models.Workspace{ID: 7, OwnerID: 10}, nil
			}},
		},
		{
			name: "lookup_error",
			ws: &mockWorkspaceStore{findByIDFn: func(int64) (*models.Workspace, error) {
				return nil, errors.New("workspace unavailable")
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Router{workspaceStore: tc.ws}
			if got := r.postWorkspaceOwnedByUser(post, 9); got != tc.want {
				t.Fatalf("ownership=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestPostIDFromURL_UsesChiIDParameter(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/posts/42", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "42")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	id, err := postIDFromURL(req)
	if err != nil || id != 42 {
		t.Fatalf("id=%d err=%v", id, err)
	}
}
