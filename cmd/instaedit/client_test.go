package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientRequest_SendsJSONAndAPIKey(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/test" {
			t.Errorf("path = %s, want /test", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk_test_client" {
			t.Errorf("authorization = %q, want Bearer sk_test_client", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content-type = %q, want application/json", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("accept = %q, want application/json", got)
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["message"] != "hello" {
			t.Errorf("message = %q, want hello", body["message"])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	c := &client{
		baseURL: server.URL,
		apiKey:  "sk_test_client",
		http:    server.Client(),
	}
	var response struct {
		Status string `json:"status"`
	}
	if err := c.request(http.MethodPost, "/test", map[string]string{"message": "hello"}, &response); err != nil {
		t.Fatalf("request() error = %v", err)
	}
	if response.Status != "ok" {
		t.Errorf("status = %q, want ok", response.Status)
	}
}

func TestClientRequest_ReturnsAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	}))
	defer server.Close()

	c := &client{baseURL: server.URL, apiKey: "sk_test_client", http: server.Client()}
	err := c.request(http.MethodGet, "/forbidden", nil, nil)
	if err == nil {
		t.Fatal("request() error = nil, want API error")
	}

	apiErr, ok := err.(*apiError)
	if !ok {
		t.Fatalf("error type = %T, want *apiError", err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Errorf("status = %d, want %d", apiErr.Status, http.StatusForbidden)
	}
	if !strings.Contains(apiErr.Body, "forbidden") {
		t.Errorf("body = %q, want forbidden", apiErr.Body)
	}
}

func TestNewClient_RequiresEnvironment(t *testing.T) {
	t.Setenv("INSTAEDIT_URL", "")
	t.Setenv("INSTAEDIT_API_KEY", "")

	if _, err := newClient(); err == nil || !strings.Contains(err.Error(), "INSTAEDIT_URL") {
		t.Fatalf("newClient() error = %v, want missing INSTAEDIT_URL", err)
	}

	t.Setenv("INSTAEDIT_URL", "https://api.example.test/")
	if _, err := newClient(); err == nil || !strings.Contains(err.Error(), "INSTAEDIT_API_KEY") {
		t.Fatalf("newClient() error = %v, want missing INSTAEDIT_API_KEY", err)
	}
}
