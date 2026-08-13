package worker

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// resolveFolderLister delegates provider lookup and OAuth token resolution to
// DriveFolderDiscovery, which is also used by the Drive Inbox scanner.
func (c *DriveBatchCrawler) resolveFolderLister(ctx context.Context, batch *models.ImportBatch) (services.DriveFolderLister, string, error) {
	return NewDriveFolderDiscovery(c.vault, c.capRouter).ResolveFolderLister(ctx, batch.SourceProvider, batch.SourceDriveAccountID)
}
