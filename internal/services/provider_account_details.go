package services

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// AccountDetailsProvider is implemented by providers that can fetch
// rich details about a connected account (channel statistics, profile
// data, branding, etc.). The CapabilityRouter dispatches
// GetAccountDetails to the correct provider; the handler uses it to
// build the GET /api/v1/accounts/{id} response.
type AccountDetailsProvider interface {
	NameProvider

	// GetAccountDetails retrieves the current state of the remote
	// resource identified by platformUserID. The accessToken is used
	// for API calls; the provider may refresh it internally.
	GetAccountDetails(
		ctx context.Context,
		accessToken string,
		platformUserID string,
	) (*models.AccountDetails, error)
}

// AccountContentProvider is implemented by providers that can list
// content items (videos, posts, reels) belonging to a connected
// account. The CapabilityRouter dispatches ListAccountContent to the
// correct provider; the handler uses it to build the
// GET /api/v1/accounts/{id}/content response.
type AccountContentProvider interface {
	NameProvider

	// ListAccountContent returns a paginated list of content items
	// for the account identified by platformUserID. cursor is an
	// opaque provider-defined cursor for pagination; empty string
	// means "start from the beginning". limit caps the number of
	// items returned.
	ListAccountContent(
		ctx context.Context,
		accessToken string,
		platformUserID string,
		cursor string,
		limit int,
	) (*models.AccountContentPage, error)
}
