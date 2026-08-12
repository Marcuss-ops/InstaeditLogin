package worker

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

type DriveInboxStore interface {
	ListEnabledInboxes(ctx context.Context) ([]*models.DriveInbox, error)
	MarkInboxScanned(ctx context.Context, inboxID int64, cursor string) error
	UpsertInboxItem(ctx context.Context, item *models.DriveInboxItem) error
}

type DriveFolderDiscoveryAPI interface {
	ResolveFolderLister(ctx context.Context, providerName string, driveAccountID *int64) (services.DriveFolderLister, string, error)
}

type DriveInboxScannerOptions struct {
	Interval time.Duration
}

type DriveInboxScanner struct {
	store     DriveInboxStore
	discovery DriveFolderDiscoveryAPI
	interval  time.Duration
	logger    *slog.Logger
}

func NewDriveInboxScanner(store DriveInboxStore, discovery DriveFolderDiscoveryAPI, opts DriveInboxScannerOptions, logger *slog.Logger) *DriveInboxScanner {
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &DriveInboxScanner{store: store, discovery: discovery, interval: opts.Interval, logger: logger}
}

func (s *DriveInboxScanner) ScanOnce(ctx context.Context) error {
	inboxes, err := s.store.ListEnabledInboxes(ctx)
	if err != nil {
		return err
	}
	for _, inbox := range inboxes {
		if err := s.scanInbox(ctx, inbox); err != nil {
			s.logger.Error("drive inbox scan failed", "inbox_id", inbox.ID, "error", err)
		}
	}
	return nil
}

func (s *DriveInboxScanner) scanInbox(ctx context.Context, inbox *models.DriveInbox) error {
	lister, token, err := s.discovery.ResolveFolderLister(ctx, "google-drive", &inbox.DriveAccountID)
	if err != nil {
		return err
	}
	pageToken := ""
	if inbox.Cursor != nil {
		pageToken = *inbox.Cursor
	}
	files, next, err := lister.ListFolder(ctx, inbox.FolderID, "", token, pageToken)
	if err != nil {
		return err
	}
	for _, file := range files {
		size := (*int64)(nil)
		if parsed, parseErr := strconv.ParseInt(file.Size, 10, 64); parseErr == nil && parsed >= 0 {
			size = &parsed
		}
		modified := (*time.Time)(nil)
		if file.ModifiedTime != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, file.ModifiedTime); parseErr == nil {
				parsed = parsed.UTC()
				modified = &parsed
			}
		}
		if err := s.store.UpsertInboxItem(ctx, &models.DriveInboxItem{InboxID: inbox.ID, DriveFileID: file.ID, Filename: file.Name, MimeType: file.MimeType, SizeBytes: size, ModifiedTime: modified, Fingerprint: file.SHA256Checksum}); err != nil {
			return err
		}
	}
	return s.store.MarkInboxScanned(ctx, inbox.ID, next)
}

func (s *DriveInboxScanner) Run(ctx context.Context) error {
	if err := s.ScanOnce(ctx); err != nil {
		s.logger.Error("initial drive inbox scan failed", "error", err)
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.ScanOnce(ctx); err != nil {
				s.logger.Error("drive inbox scan failed", "error", err)
			}
		}
	}
}

var _ services.DriveFolderLister = (*services.GoogleDriveOAuthService)(nil)
