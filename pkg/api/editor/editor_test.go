package editor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

type fakeProjectProxy struct {
	called    bool
	projectID string
	path      string
	scopes    []string
	response  *http.Response
}

func (f *fakeProjectProxy) Proxy(context.Context, string, string, int64, int64, io.Reader, string, []string) (*http.Response, error) {
	panic("unscoped proxy must not be used")
}

func (f *fakeProjectProxy) ProxyForProject(_ context.Context, method, path string, _ int64, _ int64, projectID string, _ io.Reader, _ string, scopes []string) (*http.Response, error) {
	f.called = true
	f.projectID = projectID
	f.path = path
	f.scopes = append([]string(nil), scopes...)
	if f.response != nil {
		return f.response, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("{}")),
	}, nil
}

func identityRequest(req *http.Request) *http.Request {
	identity := auth.NewUserIdentity(7, 42, 99)
	return req.WithContext(auth.WithIdentity(req.Context(), identity))
}

func TestProjectIDFromEditorPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{path: "/projects/ve_abc123", want: "ve_abc123"},
		{path: "/projects/ve_abc123/document", want: "ve_abc123"},
		{path: "/projects/not-a-velox-project", want: ""},
		{path: "/projects/ve_bad%2F/document", want: ""},
		{path: "/projects/ve_bad\n/document", want: ""},
		{path: "/groups/ve_abc123", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := projectIDFromEditorPath(tc.path); got != tc.want {
				t.Fatalf("projectIDFromEditorPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestScopesForPathUsesEditorScopeOnly(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		got := scopesForPath(method, "/projects/ve_abc123")
		if len(got) != 1 || got[0] != veloxcontract.ScopeVeloxEditorRead {
			t.Fatalf("%s scopes = %v, want [%q]", method, got, veloxcontract.ScopeVeloxEditorRead)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		got := scopesForPath(method, "/projects/ve_abc123/document")
		if len(got) != 1 || got[0] != veloxcontract.ScopeVeloxEditorWrite {
			t.Fatalf("%s scopes = %v, want [%q]", method, got, veloxcontract.ScopeVeloxEditorWrite)
		}
	}
}

func TestProxyHandlerAuthorizesProjectBeforeProxy(t *testing.T) {
	proxy := &fakeProjectProxy{}
	authorizeCalls := 0
	module := NewEditorBFFModule(Deps{
		Client: proxy,
		AuthorizeProject: func(_ context.Context, userID, workspaceID int64, projectID string, write bool) error {
			authorizeCalls++
			if userID != 7 || workspaceID != 42 || projectID != "ve_abc123" || write {
				t.Fatalf("unexpected authorization input: user=%d workspace=%d project=%q", userID, workspaceID, projectID)
			}
			return nil
		},
	})

	req := identityRequest(httptest.NewRequest(http.MethodGet, "/api/v1/editor/projects/ve_abc123/document", nil))
	recorder := httptest.NewRecorder()
	module.proxyHandler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if authorizeCalls != 1 || !proxy.called {
		t.Fatalf("authorization/proxy calls = %d/%v, want 1/true", authorizeCalls, proxy.called)
	}
	if proxy.projectID != "ve_abc123" || proxy.path != "/projects/ve_abc123/document" {
		t.Fatalf("proxy context = project %q path %q", proxy.projectID, proxy.path)
	}
	if len(proxy.scopes) != 1 || proxy.scopes[0] != veloxcontract.ScopeVeloxEditorRead {
		t.Fatalf("proxy scopes = %v, want read scope", proxy.scopes)
	}
}

func TestProxyHandlerDeniesUnauthorizedProjectWithoutProxy(t *testing.T) {
	proxy := &fakeProjectProxy{}
	module := NewEditorBFFModule(Deps{
		Client: proxy,
		AuthorizeProject: func(context.Context, int64, int64, string, bool) error {
			return context.Canceled
		},
	})

	req := identityRequest(httptest.NewRequest(http.MethodPut, "/api/v1/editor/projects/ve_other/document", nil))
	recorder := httptest.NewRecorder()
	module.proxyHandler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if proxy.called {
		t.Fatal("proxy was called for an unauthorized project")
	}
}

func TestProxyHandlerRejectsUnscopedEditorPath(t *testing.T) {
	proxy := &fakeProjectProxy{}
	module := NewEditorBFFModule(Deps{Client: proxy, AuthorizeProject: func(context.Context, int64, int64, string, bool) error {
		t.Fatal("authorization must not run without project context")
		return nil
	}})

	req := identityRequest(httptest.NewRequest(http.MethodGet, "/api/v1/editor/groups", nil))
	recorder := httptest.NewRecorder()
	module.proxyHandler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if proxy.called {
		t.Fatal("proxy was called for an unscoped editor path")
	}
}
