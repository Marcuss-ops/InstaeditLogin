package veloxclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	veloxapi "github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

// Compile-time assertion that *Client satisfies veloxapi.Client.
// A signature drift between this implementation and the interface
// (e.g. a new method added to veloxapi.Client without a matching
// implementation here) surfaces at compile time, not at the first
// BFF request.
var _ veloxapi.Client = (*Client)(nil)

// veloxAPIPrefix is the protected route prefix on the Velox master.
// The InstaEdit BFF group is mounted under /api/v1/instaedit and
// requires a valid InstaEdit control JWT.
const veloxAPIPrefix = "/api/v1/instaedit"

// Client calls the Velox master with a per-request signed JWT. It
// implements veloxapi.Client (internal/veloxcontract). Construct
// once at bootstrap via New() and inject via api.WithVeloxBFFClient.
//
// The base URL MUST NOT have a trailing slash — do() joins paths
// with a leading slash. A trailing slash would produce double
// slashes (//api/v1/jobs) which some reverse proxies reject.
type Client struct {
	baseURL string
	secret  []byte
	http    *http.Client
}

// New builds a Client from VELOX_CONTROL_URL + VELOX_CONTROL_JWT_SECRET.
// When either is empty the returned Client is nil and the BFF routes
// are not mounted (the Router's nil-guard pattern). The HTTP client
// reuses the project's shared transport (retry + logging round
// trippers) so Velox calls get the same observability as OAuth calls.
func New(baseURL, jwtSecret string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" || jwtSecret == "" {
		return nil
	}
	// NOTE: we deliberately do NOT reuse services.NewHTTPClient()
	// here. That shared client's retryRoundTripper retries 404
	// responses for idempotent methods (GET), which would convert
	// a definitive "not found" from Velox into a retryableHTTPError
	// — preventing do() from mapping 404 to veloxapi.ErrNotFound.
	// The Velox BFF needs raw status codes, so we use a plain
	// http.Client with a conservative timeout instead.
	return &Client{
		baseURL: baseURL,
		secret:  []byte(jwtSecret),
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// do signs a fresh control JWT for (userID, workspaceID) using ONLY
// the supplied scopes, builds the HTTP request, sends it, and decodes
// the JSON response into out. Returns a typed error when Velox returns
// 404 (not found) or 403 (workspace mismatch) so the BFF handler can
// map to 404. The Velox middleware maps a 403 caused by an
// insufficient-scope JWT to its own clearer 403 body — the BFF's
// ErrWorkspaceMismatch path therefore only fires when the JWT's
// workspace_id does not own the resource.
//
// CALLER CONTRACT: every public API wrapper on Client MUST pass a
// non-empty scopes slice per the per-method table in the package
// godoc of auth.go. An empty slice is rejected by signControlToken
// so the package refuses to silently widen privileges.
func (c *Client) do(ctx context.Context, method, path string, userID, workspaceID int64, scopes []string, body io.Reader, out interface{}) error {
	token, err := signControlToken(c.secret, userID, workspaceID, scopes)
	if err != nil {
		return fmt.Errorf("veloxclient: sign token: %w", err)
	}
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("veloxclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("veloxclient: call %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return veloxapi.ErrNotFound
	}
	if resp.StatusCode == http.StatusForbidden {
		return veloxapi.ErrWorkspaceMismatch
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("veloxclient: %s %s: status %d: %s", method, path, resp.StatusCode, string(raw))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("veloxclient: decode response: %w", err)
	}
	return nil
}

// doNoBody is do() for calls that have no request body and no
// response payload (e.g. CancelJob → 204). Equivalent to do() with
// body=nil and out=nil but kept as a named helper for readability.
func (c *Client) doNoBody(ctx context.Context, method, path string, userID, workspaceID int64, scopes []string) error {
	return c.do(ctx, method, path, userID, workspaceID, scopes, nil, nil)
}
