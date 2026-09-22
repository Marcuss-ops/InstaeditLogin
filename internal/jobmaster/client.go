// Package jobmaster is the server-side adapter for the generic remote job
// master. It deliberately knows nothing about a particular worker machine:
// the master URL, M2M credential and optional client identity are runtime
// configuration.
package jobmaster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	typesPath = "/api/v1/jobs/types"
	jobsPath  = "/api/v1/jobs"
)

var (
	ErrNotConfigured = errors.New("job master is not configured")
	ErrInvalidJobID  = errors.New("invalid job id")
)

// Config is intentionally transport-level configuration. It does not contain
// an IP address or a worker name, so the same InstaEdit process can point at a
// local process, a private service, or a pool front door.
type Config struct {
	BaseURL      string
	Secret       string
	ClientID     string
	Timeout      time.Duration
	PollInterval time.Duration
	PollTimeout  time.Duration
}

// API is the narrow contract consumed by the authenticated BFF routes.
type API interface {
	ListTypes(context.Context) (json.RawMessage, error)
	Submit(context.Context, SubmitRequest) (json.RawMessage, error)
	Get(context.Context, string) (json.RawMessage, error)
}

// SubmitRequest is the generic job envelope exposed by the remote Master.
// Payload remains opaque here so new handlers (stock, TTS, overlays, render,
// and future capabilities) do not require a redeploy of this adapter.
type SubmitRequest struct {
	Type           string          `json:"type"`
	Project        string          `json:"project"`
	IdempotencyKey string          `json:"idempotency_key"`
	Payload        json.RawMessage `json:"payload"`
}

// HTTPError preserves the upstream status for safe HTTP mapping without
// exposing the response body to logs or browser callers by default.
type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("job master returned HTTP %d", e.StatusCode)
}

// Client calls the Master with a server-only M2M Bearer credential.
type Client struct {
	baseURL      string
	secret       string
	clientID     string
	http         *http.Client
	pollInterval time.Duration
	pollTimeout  time.Duration
}

var _ API = (*Client)(nil)

// New returns nil when the integration is disabled. It validates the URL
// shape here so a malformed deployment fails at startup instead of on the
// first user click.
func New(cfg Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" && strings.TrimSpace(cfg.Secret) == "" {
		return nil, nil
	}
	if baseURL == "" || strings.TrimSpace(cfg.Secret) == "" {
		return nil, ErrNotConfigured
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("job master URL must be an http(s) URL without credentials, query, or fragment")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = 3 * time.Second
	}
	pollTimeout := cfg.PollTimeout
	if pollTimeout <= 0 {
		pollTimeout = 30 * time.Minute
	}
	return &Client{
		baseURL:      baseURL,
		secret:       cfg.Secret,
		clientID:     strings.TrimSpace(cfg.ClientID),
		http:         &http.Client{Timeout: timeout},
		pollInterval: pollInterval,
		pollTimeout:  pollTimeout,
	}, nil
}

func (c *Client) ListTypes(ctx context.Context) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, typesPath, nil, "")
}

func (c *Client) Submit(ctx context.Context, input SubmitRequest) (json.RawMessage, error) {
	if strings.TrimSpace(input.Type) == "" || strings.TrimSpace(input.Project) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return nil, fmt.Errorf("type, project, and idempotency_key are required")
	}
	if len(input.Payload) == 0 {
		input.Payload = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode job submission: %w", err)
	}
	return c.do(ctx, http.MethodPost, jobsPath, bytes.NewReader(body), input.IdempotencyKey)
}

func (c *Client) Get(ctx context.Context, jobID string) (json.RawMessage, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" || len(jobID) > 256 || strings.ContainsAny(jobID, "/\\\r\n") {
		return nil, ErrInvalidJobID
	}
	return c.do(ctx, http.MethodGet, jobsPath+"/"+url.PathEscape(jobID), nil, "")
}

// Wait polls one job until the Master reports a terminal state or the caller
// context/configured timeout expires. The BFF normally exposes Get for browser
// polling; this method is for future server-side orchestration and CLI paths.
func (c *Client) Wait(ctx context.Context, jobID string) (json.RawMessage, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	deadline := time.NewTimer(c.pollTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		result, err := c.Get(ctx, jobID)
		if err != nil {
			return nil, err
		}
		if isTerminal(result) {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("job master polling timed out")
		case <-ticker.C:
		}
	}
}

func isTerminal(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	if nested, ok := value["job"]; ok {
		return isTerminal(nested)
	}
	var status string
	for _, key := range []string{"status", "state", "overall_status"} {
		if json.Unmarshal(value[key], &status) == nil && strings.TrimSpace(status) != "" {
			break
		}
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "succeeded", "success", "failed", "error", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, idempotencyKey string) (json.RawMessage, error) {
	if c == nil || c.http == nil {
		return nil, ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("build job master request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Accept", "application/json")
	if c.clientID != "" {
		req.Header.Set("X-Client-ID", c.clientID)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("job master request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &HTTPError{StatusCode: resp.StatusCode}
	}
	result, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read job master response: %w", err)
	}
	if len(bytes.TrimSpace(result)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var value json.RawMessage
	if err := json.Unmarshal(result, &value); err != nil {
		return nil, fmt.Errorf("decode job master response: %w", err)
	}
	return value, nil
}
