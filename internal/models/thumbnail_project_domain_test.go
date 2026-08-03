package models

import (
	"testing"
)

func TestThumbnailProjectAssetNormalizeAndValidate(t *testing.T) {
	objectID := " text-1 "
	asset := &ThumbnailProjectAsset{
		ProjectID: " project-1 ",
		MediaID:   "00000000-0000-0000-0000-000000000001",
		Role:      " LOGO ",
		ObjectID:  &objectID,
	}
	if err := asset.NormalizeAndValidate(); err != nil {
		t.Fatalf("NormalizeAndValidate: %v", err)
	}
	if asset.ProjectID != "project-1" || asset.Role != ThumbnailProjectAssetRoleLogo || *asset.ObjectID != "text-1" {
		t.Fatalf("asset was not normalized: %+v", asset)
	}
}

func TestThumbnailProjectAssetRejectsInvalidRoleOrMedia(t *testing.T) {
	for name, asset := range map[string]*ThumbnailProjectAsset{
		"bad role":  {ProjectID: "p", MediaID: "00000000-0000-0000-0000-000000000001", Role: "image"},
		"bad media": {ProjectID: "p", MediaID: "not-a-uuid", Role: ThumbnailProjectAssetRoleLogo},
	} {
		t.Run(name, func(t *testing.T) {
			if err := asset.NormalizeAndValidate(); err == nil {
				t.Fatal("invalid asset unexpectedly accepted")
			}
		})
	}
}

func TestThumbnailExportNormalizeAndValidate(t *testing.T) {
	export := &ThumbnailExport{
		ProjectID: "p", RevisionID: "r", MediaID: "00000000-0000-0000-0000-000000000001",
		ContentType: ThumbnailProjectExportContentTypePNG, Width: 1920, Height: 1080,
		FileSize: 10, SHA256: make([]byte, 32), RendererVersion: " renderer-1 ",
	}
	if err := export.NormalizeAndValidate(); err != nil {
		t.Fatalf("NormalizeAndValidate: %v", err)
	}
	if export.Status != ThumbnailProjectExportStatusRendering || export.RendererVersion != "renderer-1" {
		t.Fatalf("export defaults/normalization incorrect: %+v", export)
	}
}

func TestThumbnailAssignmentNormalizeAndValidate(t *testing.T) {
	assignment := &ThumbnailAssignment{
		WorkspaceID: 7, ProjectID: "p", ExportID: "e", PlatformAccountID: 9,
		YouTubeVideoID: " video-1 ",
	}
	if err := assignment.NormalizeAndValidate(); err != nil {
		t.Fatalf("NormalizeAndValidate: %v", err)
	}
	if assignment.Platform != "youtube" || assignment.Status != ThumbnailProjectAssignmentStatusDraft || assignment.YouTubeVideoID != "video-1" {
		t.Fatalf("assignment defaults/normalization incorrect: %+v", assignment)
	}
}

func TestThumbnailAssignmentRejectsNonYouTubeAndInvalidStatus(t *testing.T) {
	for name, assignment := range map[string]*ThumbnailAssignment{
		"platform": {ProjectID: "p", ExportID: "e", YouTubeVideoID: "v", Platform: "tiktok"},
		"status":   {ProjectID: "p", ExportID: "e", YouTubeVideoID: "v", Status: "unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := assignment.NormalizeAndValidate(); err == nil {
				t.Fatal("invalid assignment unexpectedly accepted")
			}
		})
	}
}
