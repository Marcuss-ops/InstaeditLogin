package worker

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// resolveFolderLister returns the Drive folder lister + access
// token for the batch. Today only "google_drive" is supported;
// a future Dropbox source registers here.
//
// For authenticated access (batch.SourceDriveAccountID != nil) we
// fetch the long-lived OAuth bearer token from the vault. For
// public folders the lister's ListFolder-with-empty-accessToken
// path uses the server-side GOOGLE_DRIVE_API_KEY via the service
// implementation; the handler verified the configuration exists at
// user-OAuth time so we surface a typed error here if not.
func (c *DriveBatchCrawler) resolveFolderLister(ctx context.Context, batch *models.ImportBatch) (services.DriveFolderLister, string, error) {
	provider, ok := c.capRouter.Get(batch.SourceProvider)
	if !ok {
		return nil, "", fmt.Errorf("source_provider %q not configured", batch.SourceProvider)
	}
	lister, ok := provider.(services.DriveFolderLister)
	if !ok {
		return nil, "", fmt.Errorf("source_provider %q does not implement DriveFolderLister", batch.SourceProvider)
	}
	if batch.SourceDriveAccountID == nil {
		// Public folder path — lister uses the server's GOOGLE_DRIVE_API_KEY
		// when access_token is empty.
		return lister, "", nil
	}
	importer, ok := provider.(services.DriveImporter)
	if !ok {
		return nil, "", fmt.Errorf("source_provider %q does not implement DriveImporter (needed to read the bearer token)", batch.SourceProvider)
	}
	token, err := c.vault.Renew(ctx, *batch.SourceDriveAccountID, models.TokenTypeBearer,
		func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
			return importer.RefreshOAuthToken(ctx, refreshToken)
		})
	if err != nil {
		return nil, "", fmt.Errorf("refresh drive bearer token: %w", err)
	}
	return lister, token.AccessToken, nil
}
