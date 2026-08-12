package services

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type resolverStoreFake struct {
	pkg      *models.ContentPackage
	metadata *models.ContentMetadataRevision
	targets  []*models.ContentPackageTarget
	bundle   *models.TranslationBundle
	entries  map[string]*models.TranslationEntry
}

func (f *resolverStoreFake) FindPackage(context.Context, int64, int64) (*models.ContentPackage, error) {
	return f.pkg, nil
}
func (f *resolverStoreFake) ListTargets(context.Context, int64) ([]*models.ContentPackageTarget, error) {
	return f.targets, nil
}
func (f *resolverStoreFake) FindCurrentMetadata(context.Context, int64) (*models.ContentMetadataRevision, error) {
	return f.metadata, nil
}
func (f *resolverStoreFake) ResolveTranslationEntries(context.Context, int64, int64, []string) (*models.TranslationBundle, map[string]*models.TranslationEntry, error) {
	return f.bundle, f.entries, nil
}

func TestPublicationResolverFallsBackToPackageTemplate(t *testing.T) {
	cover := "package-cover"
	templateVersion := int64(21)
	fake := &resolverStoreFake{
		pkg:      &models.ContentPackage{ID: 1, WorkspaceID: 7, DriveFileID: "drive-1", SourceLanguage: "it", CurrentCoverMediaID: &cover, CurrentCoverTemplateVersionID: &templateVersion},
		metadata: &models.ContentMetadataRevision{ID: 3, SourceLanguage: "it", Title: "Titolo", Description: "Descrizione"},
		targets:  []*models.ContentPackageTarget{{ID: 8, ContentPackageID: 1, PlatformAccountID: 22, Language: "it", PrivacyStatus: "public", Enabled: true}},
	}
	resolved, err := NewPublicationResolver(fake).Resolve(context.Background(), 7, 1, 8)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.CoverTemplateVersionID == nil || *resolved.CoverTemplateVersionID != templateVersion {
		t.Fatalf("package template fallback was not selected: %+v", resolved.CoverTemplateVersionID)
	}
}

func TestPublicationResolverUsesSourceForSameLanguage(t *testing.T) {
	cover := "cover-1"
	fake := &resolverStoreFake{
		pkg:      &models.ContentPackage{ID: 1, WorkspaceID: 7, DriveFileID: "drive-1", SourceLanguage: "it", CurrentCoverMediaID: &cover},
		metadata: &models.ContentMetadataRevision{ID: 3, SourceLanguage: "it", Title: "Titolo", Description: "Descrizione", Tags: json.RawMessage(`["boxe"]`)},
		targets:  []*models.ContentPackageTarget{{ID: 8, ContentPackageID: 1, PlatformAccountID: 22, Language: "it", PrivacyStatus: "public", Enabled: true}},
	}
	resolved, err := NewPublicationResolver(fake).Resolve(context.Background(), 7, 1, 8)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Ready() || resolved.Title != "Titolo" || resolved.TranslationBundleID != nil {
		t.Fatalf("unexpected resolution: %+v", resolved)
	}
}

func TestPublicationResolverBlocksMissingTranslationAndCover(t *testing.T) {
	fake := &resolverStoreFake{
		pkg:      &models.ContentPackage{ID: 1, WorkspaceID: 7, DriveFileID: "drive-1", SourceLanguage: "it"},
		metadata: &models.ContentMetadataRevision{ID: 3, SourceLanguage: "it", Title: "Titolo", Description: "Descrizione"},
		targets:  []*models.ContentPackageTarget{{ID: 8, ContentPackageID: 1, PlatformAccountID: 22, Language: "es", PrivacyStatus: "public", Enabled: true}},
		entries:  map[string]*models.TranslationEntry{},
	}
	resolved, err := NewPublicationResolver(fake).Resolve(context.Background(), 7, 1, 8)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Ready() {
		t.Fatal("missing translation and cover must block publication")
	}
	codes := map[string]bool{}
	for _, blocker := range resolved.Blockers {
		codes[blocker.Code] = true
	}
	if !codes["translation_missing"] || !codes["cover_missing"] {
		t.Fatalf("blockers=%v", codes)
	}
}

func TestPublicationResolverManualEntryWinsWhenBundleIsResolved(t *testing.T) {
	cover := "cover-1"
	fake := &resolverStoreFake{
		pkg:      &models.ContentPackage{ID: 1, WorkspaceID: 7, DriveFileID: "drive-1", SourceLanguage: "it", CurrentCoverMediaID: &cover},
		metadata: &models.ContentMetadataRevision{ID: 3, SourceLanguage: "it", Title: "IT", Description: "IT"},
		targets:  []*models.ContentPackageTarget{{ID: 8, ContentPackageID: 1, PlatformAccountID: 22, Language: "en", PrivacyStatus: "public", Enabled: true}},
		bundle:   &models.TranslationBundle{ID: 11},
		entries:  map[string]*models.TranslationEntry{"en": {Language: "en", Title: "Manual title", Description: "Manual description", Origin: "manual"}},
	}
	resolved, err := NewPublicationResolver(fake).Resolve(context.Background(), 7, 1, 8)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Ready() || resolved.Title != "Manual title" || resolved.TranslationBundleID == nil || *resolved.TranslationBundleID != 11 {
		t.Fatalf("unexpected resolution: %+v", resolved)
	}
}
