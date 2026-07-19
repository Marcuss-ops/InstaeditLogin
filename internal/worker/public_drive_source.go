package worker

import (
	"context"
	"fmt"
	"io"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// PublicDriveSource is the DEPRECATED ArtifactSource implementation
// for the upload worker's public Drive ingest path.
//
// The unauthenticated `drive.google.com/uc` export endpoint with HTML
// confirmation-token scraping was REMOVED from the Drive service as
// of the Blocco #2.1 hardening refactor. The source still exists in
// the registry so any upload_job row carrying this legacy
// SourceType is routed through a clean error path instead of the
// generic "unsupported source type" fallback — operators see the
// actionable "re-import via authenticated Drive" guidance.
//
// Open is the only load-bearing method. Inspect is implemented as a
// typed error so the worker's pre-flight does not silently treat
// this source as live; the message is identical to Open's so the
// operator gets the same guidance regardless of which entry point
// surfaced the deprecation.
type PublicDriveSource struct{}

// NewPublicDriveSource constructs the deprecated stub. No state.
// Kept as a constructor (rather than a zero-value struct) so the
// registry registration reads symmetrically across all sources:
// registry.Register(NewPublicDriveSource()).
func NewPublicDriveSource() *PublicDriveSource {
	return &PublicDriveSource{}
}

// Name implements ArtifactSource. Returns UploadJobSourcePublicDrive
// so the resolver routes public_drive rows here.
func (s *PublicDriveSource) Name() models.UploadJobSource {
	return models.UploadJobSourcePublicDrive
}

// Inspect returns the same deprecation error as Open — there is no
// useful artefact metadata to surface for a source whose download
// path is gone.
func (s *PublicDriveSource) Inspect(ctx context.Context, job *models.UploadJob) (*SourceMetadata, error) {
	return nil, s.deprecationError()
}

// Open returns the deprecation error verbatim. The message matches
// the historical error string from the legacy switch in
// processIngestJob so operator logs and dashboards see identical
// guidance pre and post the registry refactor.
func (s *PublicDriveSource) Open(ctx context.Context, job *models.UploadJob) (io.ReadCloser, error) {
	return nil, s.deprecationError()
}

// deprecationError centralises the operator-facing message so a
// future copy tweak happens once. The fmt.Errorf shape (not a
// PermanentError) is intentional: the row is "the source type is
// gone, please re-import" — transient-or-permanent framing doesn't
// apply; it's a hard-stop on operator action.
func (s *PublicDriveSource) deprecationError() error {
	return fmt.Errorf(
		"unsupported source type %q: the public_drive download path was removed in the Drive pipeline hardening refactor; re-import this file via the authenticated Drive flow (POST /api/v1/media/import/drive with a connected Drive account)",
		models.UploadJobSourcePublicDrive,
	)
}

// Compile-time assertion that PublicDriveSource satisfies
// ArtifactSource. Cheap insurance against a future interface
// drift breaking the registry wiring without a build-time signal.
var _ ArtifactSource = (*PublicDriveSource)(nil)
