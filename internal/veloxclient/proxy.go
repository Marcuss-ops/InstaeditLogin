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
func (c *Client) Proxy(ctx context.Context, method, path string, userID, workspaceID int64, body io.Reader, contentType string) (*http.Response, error) {
	token, err := signControlToken(c.secret, userID, workspaceID)
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

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("veloxclient: proxy %s %s: %w", method, dest, err)
	}
	return resp, nil
}
