package veloxclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Proxy forwards an arbitrary HTTP request to the Velox master under
// /api/v1/instaedit/editor{path}. It signs a fresh control JWT with
// the user and workspace identity, copies the request body, and
// returns the upstream response verbatim. The caller is responsible
// for closing resp.Body.
//
// This is used by the EditorBFFModule so the Dark Editor SPA can talk
// to the Velox private master through InstaEdit's authenticated BFF
// without ever seeing the control secret.
//
// SCOPE CONTRACT (architect verdict Q2):
//   - When the caller supplies a non-empty `scopes` slice, the JWT is
//     signed with EXACTLY those scopes. The Velox middleware enforces
//     the operation-grained grant and 403s on mismatch.
//
//   - When the caller passes `scopes == nil` (or empty), the BFF
//     falls back to allScopesSuperset (the union of the four editor
//     scopes). This is a transitional safeguard for the EditorBFFModule
//     call sites that have not yet been wired to declare their
//     per-operation scopes.
//
// REMOVAL PLAN: once every EditorBFFModule callsite passes an
// explicit per-operation []string{...} (TODO followup commit), the
// fallback and the `scopes == nil` default should be removed so a
// bare `Proxy(...)` call fails closed instead of silently widening
// to superset.
func (c *Client) Proxy(ctx context.Context, method, path string, userID, workspaceID int64, body io.Reader, contentType string, scopes []string) (*http.Response, error) {
	if len(scopes) == 0 {
		// Transitional fallback — see architect verdict Q2.
		scopes = allScopesSuperset
	}
	token, err := signControlToken(c.secret, userID, workspaceID, scopes)
	if err != nil {
		return nil, fmt.Errorf("veloxclient: sign token: %w", err)
	}

	// path is the part after /api/v1/editor on the InstaEdit side.
	// Forward it under /api/v1/instaedit/editor on the Velox side.
	dest := "/api/v1/instaedit/editor" + path
	urlStr := c.baseURL + dest
	if _, err := url.Parse(urlStr); err != nil {
		return nil, fmt.Errorf("veloxclient: invalid proxy url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, fmt.Errorf("veloxclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// Strip any browser-origin header before forwarding to Velox.
	// Velox's internal security guard rejects requests carrying an
	// Origin header (it is an internal API), so the BFF must not
	// forward the browser's Origin.
	req.Header.Del("Origin")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("veloxclient: proxy %s %s: %w", method, dest, err)
	}
	return resp, nil
}
