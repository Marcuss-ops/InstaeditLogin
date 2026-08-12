package worker

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

type inboxScannerListerFake struct {
	listCalls int
}

func (f *inboxScannerListerFake) ListFolder(context.Context, string, string, string, string) ([]services.GoogleDriveFile, string, error) {
	f.listCalls++
	return []services.GoogleDriveFile{{ID: "drive-video-1", Name: "video.mp4", MimeType: "video/mp4", Size: "1048576", SHA256Checksum: "sha-1"}}, "cursor-next", nil
}

type inboxScannerDiscoveryFake struct {
	lister *inboxScannerListerFake
}

func (f *inboxScannerDiscoveryFake) ResolveFolderLister(context.Context, string, *int64) (services.DriveFolderLister, string, error) {
	return f.lister, "oauth-token", nil
}

type inboxScannerStoreFake struct {
	inbox       *models.DriveInbox
	items       map[string]*models.DriveInboxItem
	upsertCalls int
	cursor      string
}

func (f *inboxScannerStoreFake) ListEnabledInboxes(context.Context) ([]*models.DriveInbox, error) {
	return []*models.DriveInbox{f.inbox}, nil
}

func (f *inboxScannerStoreFake) MarkInboxScanned(_ context.Context, _ int64, cursor string) error {
	f.cursor = cursor
	return nil
}

func (f *inboxScannerStoreFake) UpsertInboxItem(_ context.Context, item *models.DriveInboxItem) error {
	f.upsertCalls++
	if f.items == nil {
		f.items = make(map[string]*models.DriveInboxItem)
	}
	copy := *item
	f.items[item.DriveFileID] = &copy
	return nil
}

func TestDriveInboxScanner_IsMetadataOnlyAndRediscoverySafe(t *testing.T) {
	lister := &inboxScannerListerFake{}
	store := &inboxScannerStoreFake{inbox: &models.DriveInbox{ID: 7, DriveAccountID: 8, FolderID: "folder-1", Enabled: true}}
	scanner := NewDriveInboxScanner(store, &inboxScannerDiscoveryFake{lister: lister}, DriveInboxScannerOptions{}, nil)
	if err := scanner.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := scanner.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lister.listCalls != 2 || store.upsertCalls != 2 || len(store.items) != 1 {
		t.Fatalf("discovery calls=%d upserts=%d unique_items=%d", lister.listCalls, store.upsertCalls, len(store.items))
	}
	if store.cursor != "cursor-next" {
		t.Fatalf("cursor checkpoint=%q", store.cursor)
	}
	if _, ok := store.items["drive-video-1"]; !ok {
		t.Fatal("discovered video was not persisted")
	}
}
