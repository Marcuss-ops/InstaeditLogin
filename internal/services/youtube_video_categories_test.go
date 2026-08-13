package services

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListVideoCategories_HappyPath(t *testing.T) {
	var gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"items": [
				{"id": "24", "snippet": {"title": "Intrattenimento"}},
				{"id": "17", "snippet": {"title": "Sport"}},
				{"id": "", "snippet": {"title": "blank id must be dropped"}}
			]
		}`))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	categories, err := svc.ListVideoCategories(t.Context(), "token", "it")
	if err != nil {
		t.Fatalf("ListVideoCategories: %v", err)
	}
	if gotAuth != "Bearer token" {
		t.Errorf("authorization: want Bearer token, got %q", gotAuth)
	}
	// part + hl are fixed; regionCode is uppercased.
	if !strings.Contains(gotQuery, "part=snippet") || !strings.Contains(gotQuery, "hl=it") || !strings.Contains(gotQuery, "regionCode=IT") {
		t.Errorf("query: want part=snippet, hl=it, regionCode=IT; got %q", gotQuery)
	}
	want := []YouTubeVideoCategory{
		{ID: "24", Label: "Intrattenimento"},
		{ID: "17", Label: "Sport"},
	}
	if len(categories) != len(want) {
		t.Fatalf("categories: want %d, got %d (%+v)", len(want), len(categories), categories)
	}
	for i := range want {
		if categories[i] != want[i] {
			t.Errorf("categories[%d]: want %+v, got %+v", i, want[i], categories[i])
		}
	}
}

func TestListVideoCategories_NoRegionOmitsRegionCode(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items": []}`))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	categories, err := svc.ListVideoCategories(t.Context(), "token", "")
	if err != nil {
		t.Fatalf("ListVideoCategories: %v", err)
	}
	if strings.Contains(gotQuery, "regionCode") {
		t.Errorf("query: regionCode must be omitted for the global default; got %q", gotQuery)
	}
	if categories == nil || len(categories) != 0 {
		t.Errorf("categories: want empty non-nil slice, got %#v", categories)
	}
}

func TestListVideoCategories_ErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		wantCode int
		wantCat  string
	}{
		{"rate limited", http.StatusTooManyRequests, http.StatusTooManyRequests, "rate_limit"},
		{"unauthorized", http.StatusUnauthorized, http.StatusUnauthorized, "auth"},
		{"forbidden", http.StatusForbidden, http.StatusForbidden, "auth"},
		{"server error", http.StatusInternalServerError, http.StatusInternalServerError, "server_error"},
		{"unexpected", http.StatusTeapot, http.StatusTeapot, "unexpected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			svc := newTestYouTubeService(srv)
			_, err := svc.ListVideoCategories(t.Context(), "token", "IT")
			if err == nil {
				t.Fatal("expected error")
			}
			var apiErr *YouTubeAPIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("want *YouTubeAPIError, got %T: %v", err, err)
			}
			if apiErr.StatusCode != tc.wantCode || apiErr.Category != tc.wantCat {
				t.Errorf("want status %d category %q, got %d %q", tc.wantCode, tc.wantCat, apiErr.StatusCode, apiErr.Category)
			}
		})
	}
}

func TestListVideoCategories_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	svc := newTestYouTubeService(srv)
	srv.Close() // any request now fails at the transport level

	_, err := svc.ListVideoCategories(t.Context(), "token", "IT")
	if err == nil {
		t.Fatal("expected network error")
	}
	var apiErr *YouTubeAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *YouTubeAPIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 0 || apiErr.Category != "network" {
		t.Errorf("want status 0 category network, got %d %q", apiErr.StatusCode, apiErr.Category)
	}
}

func TestListVideoCategories_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	_, err := svc.ListVideoCategories(t.Context(), "token", "IT")
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("want decode error, got %v", err)
	}
}
