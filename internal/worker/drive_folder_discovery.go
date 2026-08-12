package worker

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// DriveFolderDiscovery is the shared provider/token boundary for every
// metadata-only or publishing Drive folder consumer. It deliberately knows
// nothing about UploadJob or InboxItem rows.
type DriveFolderDiscovery struct {
	vault     credentials.VaultAPI
	capRouter *services.CapabilityRouter
}

func NewDriveFolderDiscovery(vault credentials.VaultAPI, capRouter *services.CapabilityRouter) *DriveFolderDiscovery {
	return &DriveFolderDiscovery{vault: vault, capRouter: capRouter}
}

func (d *DriveFolderDiscovery) ResolveFolderLister(ctx context.Context, providerName string, driveAccountID *int64) (services.DriveFolderLister, string, error) {
	providerKey := models.NormalizePlatformIdentifier(providerName)
	provider, ok := d.capRouter.Get(providerKey)
	if !ok && providerKey != providerName {
		provider, ok = d.capRouter.Get(providerName)
	}
	if !ok {
		return nil, "", fmt.Errorf("source_provider %q not configured", providerName)
	}
	lister, ok := provider.(services.DriveFolderLister)
	if !ok {
		return nil, "", fmt.Errorf("source_provider %q does not implement DriveFolderLister", providerName)
	}
	if driveAccountID == nil {
		return lister, "", nil
	}
	importer, ok := provider.(services.DriveImporter)
	if !ok {
		return nil, "", fmt.Errorf("source_provider %q does not implement DriveImporter", providerName)
	}
	token, err := d.vault.Renew(ctx, *driveAccountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return importer.RefreshOAuthToken(ctx, refreshToken)
	})
	if err != nil {
		return nil, "", fmt.Errorf("refresh drive bearer token: %w", err)
	}
	return lister, token.AccessToken, nil
}
