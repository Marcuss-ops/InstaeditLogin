package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// AuthenticatedDriveSource is the Google Drive-backed ArtifactSource
// implementation for the upload worker's authenticated ingest path.
// Holds the bounded (importer + vault) pair required to refresh
// the per-account OAuth token + DownloadFile against
// /drive/v3/files/{id}?alt=media.
//
// Construct once, register once, share across the worker pool.
// DriveImporter carries its own http.Client so concurrent Open
// calls are safe per goroutine.
type AuthenticatedDriveSource struct {
	importer services.DriveImporter
	vault    credentials.VaultAPI
}

// NewAuthenticatedDriveSource wires the per-account dependency
// pair. importer nil or vault nil returns a typed error — the
// caller (bootstrap chain) should fail-loud rather than boot with
// a half-wired worker pool.
func NewAuthenticatedDriveSource(importer services.DriveImporter, vault credentials.VaultAPI) (*AuthenticatedDriveSource, error) {
	if importer == nil {
		return nil, fmt.Errorf("authenticated drive source: nil DriveImporter")
	}
	if vault == nil {
		return nil, fmt.Errorf("authenticated drive source: nil VaultAPI")
	}
	return &AuthenticatedDriveSource{
		importer: importer,
		vault:    vault,
	}, nil
}

// Name implements ArtifactSource.
func (s *AuthenticatedDriveSource) Name() models.UploadJobSource {
	return models.UploadJobSourceAuthenticatedDrive
}

// Inspect implements ArtifactSource. Issues GetFileMetadata against
// the Drive API so the worker has size + mime + etag for the pre-
// flight. Drive API returns Size as a string per its JSON schema;
// we ParseInt before exposing. SHA256Checksum is intentionally
// surfaced as the ETag only — the worker re-derives via TeeReader
// on the Open path (defense-in-depth: never trust upstream-declared
// hashes per the VeloxSource pattern).
//
// job.DriveAccountID nil → typed error so the worker's pre-flight
// surfaces an actionable message ("this row has no drive account —
// re-link or escalate") rather than a confusing 401 from Drive.
func (s *AuthenticatedDriveSource) Inspect(ctx context.Context, job *models.UploadJob) (*SourceMetadata, error) {
	if job == nil {
		return nil, fmt.Errorf("authenticated drive inspect: nil job")
	}
	if job.DriveAccountID == nil {
		return nil, fmt.Errorf("authenticated drive inspect: drive_account_id is null on job %d", job.ID)
	}
	oauthToken, err := s.vault.Renew(ctx, *job.DriveAccountID, models.TokenTypeBearer,
		func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
			return s.importer.RefreshOAuthToken(ctx, refreshToken)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("authenticated drive inspect: refresh token: %w", err)
	}
	md, err := s.importer.GetFileMetadata(ctx, oauthToken.AccessToken, job.SourceID)
	if err != nil {
		return nil, fmt.Errorf("authenticated drive inspect: get metadata: %w", err)
	}
	if md == nil {
		return nil, fmt.Errorf("authenticated drive inspect: file %s 返回ed nil metadata", job.SourceID)
	}
	// Task 5/10 — defense-in-depth: reject canDownload=false at the
	// source layer too, not just at the HTTP handler. Symmetric
	// with pkg/api/drive_import.go's check; the worker pull-path
	// (folder crawler + future ops tools) goes through Inspect and
	// would otherwise leak a non-downloadable row into the publish
	// pool that 403s mid-download. An ABSENT Capabilities field
	// mirrors the HTTP-layer policy: legacy files where the API
	// cannot determine the boolean are NOT rejected (matches the
	// godoc on google_drive_oauth.go::Capabilities).
	//
	// The returned error is intentionally multi-target via
	// errors.Join so the upstream worker routing code can match on
	// EITHER sentinel without parsing the message blob:
	//   - errors.Is(err, services.ErrDriveNotDownloadable) — true
	//     for the existing operator-triage dashboard grouping
	//     (the MUST-be-rejected-but-MUST-not-be-MarkReadied contract).
	//   - errors.Is(err, ErrPermanent) — true so the upload_worker's
	//     handleProcessingError fast-paths to MarkDeadLetter on the
	//     FIRST call, bypassing the retry budget. A canDownload=false
	//     file is not going to become downloadable on retry — the
	//     retry loop would only burn the attempt_count clock for the
	//     ~5 min × 8 attempts the budget stretches to before
	//     dead-letter kicks in anyway.
	if md.Capabilities != nil && !md.Capabilities.CanDownload {
		return nil, errors.Join(
			fmt.Errorf("%w: file %s (Drive reported capabilities.canDownload=false; check DLP rules / IRM / share-settings)", services.ErrDriveNotDownloadable, job.SourceID),
			PermanentError{
				Code:    CodeDriveNotDownloadable,
				Message: fmt.Sprintf("drive file %s reported capabilities.canDownload=false; cannot be ingested", job.SourceID),
			},
		)
	}
	size, err := strconv.ParseInt(md.Size, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("authenticated drive inspect: parse Drive Size %q: %w", md.Size, err)
	}
	return &SourceMetadata{
		SizeBytes: size,
		MimeType:  md.MimeType,
		ETag:      md.SHA256Checksum, // Drive API surfaces SHA-256; we use it as cache validator
		// Task 4/10: surface Drive-declared SHA so the worker's
		// ArtifactVerificationPolicy includes RequireSHA=true when
		// Drive returned a sha256Checksum. Absent → RequireSHA=false
		// (compute-and-persist local SHA only, no upstream comparison).
		// Defense-in-depth is preserved: artifactVerifyReader STILL
		// hashes the streamed bytes for ActualSHA256Hex even when
		// upstream-declared SHA is absent or mismatches.
		SHA256Hex: strings.ToLower(md.SHA256Checksum),
	}, nil
}

// Open implements ArtifactSource. Refreshes the OAuth token via the
// Vault, calls importer.DownloadFile, and returns the response body
// verbatim as io.ReadCloser. The worker layer wraps the body in
// io.TeeReader + io.LimitReader (via veloxVerifyReader — Velox
// only) for the single-pass SHA compute + size-guard.
//
// Pre-flight invariants enforced (fail loud BEFORE Drive round-trip):
//   - job.DriveAccountID nil → actionable error mentioning the row id
//
// The DriveImporter.DownloadFile contract returns *http.Response with
// the raw body. The worker MUST Close the ReadCloser (it closes
// resp.Body); double-close is harmless.
func (s *AuthenticatedDriveSource) Open(ctx context.Context, job *models.UploadJob) (io.ReadCloser, error) {
	if job == nil {
		return nil, fmt.Errorf("authenticated drive open: nil job")
	}
	if job.DriveAccountID == nil {
		return nil, fmt.Errorf("authenticated drive open: drive_account_id is null on job %d", job.ID)
	}
	oauthToken, err := s.vault.Renew(ctx, *job.DriveAccountID, models.TokenTypeBearer,
		func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
			return s.importer.RefreshOAuthToken(ctx, refreshToken)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("authenticated drive open: refresh token: %w", err)
	}
	resp, err := s.importer.DownloadFile(ctx, oauthToken.AccessToken, job.SourceID)
	if err != nil {
		return nil, fmt.Errorf("authenticated drive open: download: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("authenticated drive open: importer returned nil response")
	}
	return resp.Body, nil
}

// Compile-time assertion that AuthenticatedDriveSource satisfies
// ArtifactSource. Cheap insurance against a future interface drift.
var _ ArtifactSource = (*AuthenticatedDriveSource)(nil)
