// Package main implements the instaedit CLI: a thin, browserless
// client for the InstaEdit API driven by a workspace API key.
//
// Environment:
//
//	INSTAEDIT_URL      base URL of the InstaEdit API (e.g. https://api.instaedit.org)
//	INSTAEDIT_API_KEY  sk_test_... / sk_live_... workspace API key
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const cliUserAgent = "instaedit-cli/1.0"

// client is a minimal JSON HTTP client bound to one InstaEdit API key.
type client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func newClient() (*client, error) {
	baseURL := trimTrailingSlash(os.Getenv("INSTAEDIT_URL"))
	apiKey := os.Getenv("INSTAEDIT_API_KEY")
	if baseURL == "" {
		return nil, fmt.Errorf("INSTAEDIT_URL is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("INSTAEDIT_API_KEY is required")
	}
	return &client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// apiError is returned for any non-2xx API response.
type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("instaedit %d: %s", e.Status, e.Body)
}

// request performs a JSON request against baseURL+path. When body is
// non-nil it is JSON-marshalled as the request body; when out is
// non-nil the JSON response is decoded into it.
func (c *client) request(method, path string, body, out any) error {
	return c.requestWithHeaders(method, path, body, out, nil)
}

func (c *client) requestWithHeaders(method, path string, body, out any, headers map[string]string) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.baseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", cliUserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{Status: resp.StatusCode, Body: string(data)}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// putBytes uploads data to a presigned storage URL. It deliberately uses
// a separate client so the Authorization header is never sent to the
// storage backend — only the provided headers are forwarded.
func putBytes(url string, headers map[string]string, data []byte) error {
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build storage request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	cl := &http.Client{Timeout: 30 * time.Minute}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("storage upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("storage upload %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
