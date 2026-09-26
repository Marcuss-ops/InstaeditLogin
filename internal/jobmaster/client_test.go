package jobmaster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientUsesConfigurableM2MContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("X-Client-ID"); got != "editor-01" {
			t.Errorf("client id = %q", got)
		}
		if r.URL.Path == typesPath {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"types":["script.generate","voiceover.tts"]}`))
			return
		}
		if r.URL.Path != jobsPath {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "video-01-script-001" {
			t.Errorf("idempotency key = %q", got)
		}
		var body SubmitRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Type != "script.generate" || body.Project != "video-01" || string(body.Payload) != `{"prompt":"hello"}` {
			t.Errorf("body = %+v", body)
		}
		_, _ = w.Write([]byte(`{"job_id":"job_123","status":"queued"}`))
	}))
	defer server.Close()

	c, err := New(Config{BaseURL: server.URL, Secret: "test-secret", ClientID: "editor-01", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListTypes(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := c.Submit(context.Background(), SubmitRequest{
		Type:           "script.generate",
		Project:        "video-01",
		IdempotencyKey: "video-01-script-001",
		Payload:        json.RawMessage(`{"prompt":"hello"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response), "job_123") {
		t.Fatalf("response = %s", response)
	}
}

func TestClientDoesNotAcceptAURLWithCredentials(t *testing.T) {
	_, err := New(Config{BaseURL: "https://user:pass@example.test", Secret: "secret"})
	if err == nil {
		t.Fatal("expected credentials in URL to be rejected")
	}
}

func TestClientRejectsPathTraversalJobID(t *testing.T) {
	c, err := New(Config{BaseURL: "http://example.test", Secret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "../secret"); err != ErrInvalidJobID {
		t.Fatalf("error = %v, want ErrInvalidJobID", err)
	}
}

func TestClientCancelUsesScopedJobControlRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/jobs/job-42/cancel" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("authorization = %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"job_id":"job-42","status":"CANCELLED"}`))
	}))
	defer server.Close()
	c, err := New(Config{BaseURL: server.URL, Secret: "test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Cancel(context.Background(), "job-42"); err != nil {
		t.Fatal(err)
	}
}

func TestClientReadsMediaCatalogWithEscapedSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/media/assets" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		if r.URL.Query().Get("source") != "youtube" || r.URL.Query().Get("search") != "Mike Tyson & training" || r.URL.Query().Get("limit") != "12" {
			t.Fatalf("query = %v", r.URL.Query())
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	c, err := New(Config{BaseURL: server.URL, Secret: "test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SearchMedia(context.Background(), "Mike Tyson & training", 12); err != nil {
		t.Fatal(err)
	}
}
