package services

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type coverOverrideResolverStore struct{}

func (coverOverrideResolverStore) FindPackage(context.Context, int64, int64) (*models.ContentPackage, error) {
	defaultCover := "default-cover"
	defaultTemplate := int64(12)
	return &models.ContentPackage{ID: 1, WorkspaceID: 7, DriveFileID: "drive-1", SourceLanguage: "it", CurrentCoverMediaID: &defaultCover, CurrentCoverTemplateVersionID: &defaultTemplate}, nil
}
func (coverOverrideResolverStore) ListTargets(context.Context, int64) ([]*models.ContentPackageTarget, error) {
	override := "english-cover"
	overrideTemplate := int64(13)
	return []*models.ContentPackageTarget{{ID: 9, ContentPackageID: 1, PlatformAccountID: 99, Language: "en", PrivacyStatus: "public", Enabled: true, CoverMediaID: &override, CoverTemplateVersionID: &overrideTemplate}}, nil
}
func (coverOverrideResolverStore) FindCurrentMetadata(context.Context, int64) (*models.ContentMetadataRevision, error) {
	return &models.ContentMetadataRevision{ID: 3, SourceLanguage: "it", Title: "Title", Description: "Description"}, nil
}
func (coverOverrideResolverStore) ResolveTranslationEntries(context.Context, int64, int64, []string) (*models.TranslationBundle, map[string]*models.TranslationEntry, error) {
	return &models.TranslationBundle{ID: 4}, map[string]*models.TranslationEntry{"en": {Language: "en", Title: "English title", Description: "English description"}}, nil
}

func TestPublicationResolverTargetCoverOverrideWins(t *testing.T) {
	resolved, err := NewPublicationResolver(coverOverrideResolverStore{}).Resolve(context.Background(), 7, 1, 9)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ThumbnailMediaID == nil || *resolved.ThumbnailMediaID != "english-cover" {
		t.Fatalf("target cover override was not selected: %+v", resolved.ThumbnailMediaID)
	}
	if resolved.CoverTemplateVersionID == nil || *resolved.CoverTemplateVersionID != 13 {
		t.Fatalf("target template override was not selected: %+v", resolved.CoverTemplateVersionID)
	}
	if !resolved.Ready() {
		t.Fatalf("override should keep publication ready: %+v", resolved.Blockers)
	}
}
