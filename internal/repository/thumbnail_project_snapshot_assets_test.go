package repository

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestSnapshotAssetRefs(t *testing.T) {
	objectID := "img-1"
	tests := []struct {
		name string
		json string
		want []models.ThumbnailProjectAsset
	}{
		{
			name: "image object maps to foreground with object id",
			json: `{"canvas":{},"objects":[{"id":"img-1","type":"image","media_id":"00000000-0000-4000-8000-000000000001"}]}`,
			want: []models.ThumbnailProjectAsset{{MediaID: "00000000-0000-4000-8000-000000000001", Role: models.ThumbnailProjectAssetRoleForeground, ObjectID: &objectID}},
		},
		{
			name: "font object maps to font role",
			json: `{"objects":[{"id":"f1","type":"font","media_id":"00000000-0000-4000-8000-000000000002"}]}`,
			want: []models.ThumbnailProjectAsset{{MediaID: "00000000-0000-4000-8000-000000000002", Role: models.ThumbnailProjectAssetRoleFont, ObjectID: &[]string{"f1"}[0]}},
		},
		{
			name: "duplicate media id is deduplicated by role",
			json: `{"objects":[{"id":"a","type":"image","media_id":"00000000-0000-4000-8000-000000000003"},{"id":"b","type":"image","media_id":"00000000-0000-4000-8000-000000000003"}]}`,
			want: []models.ThumbnailProjectAsset{{MediaID: "00000000-0000-4000-8000-000000000003", Role: models.ThumbnailProjectAssetRoleForeground, ObjectID: &[]string{"a"}[0]}},
		},
		{
			name: "objects without media id are skipped",
			json: `{"objects":[{"id":"t1","type":"text","text":"x"},{"id":"i1","type":"image","media_id":""}]}`,
			want: nil,
		},
		{
			name: "non-uuid media id is skipped (never fails the save)",
			json: `{"objects":[{"id":"bad","type":"image","media_id":"not-a-uuid"}]}`,
			want: nil,
		},
		{
			name: "empty snapshot yields no links",
			json: `{"canvas":{},"objects":[]}`,
			want: nil,
		},
		{
			name: "malformed json yields no links",
			json: `not-json`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := snapshotAssetRefs(json.RawMessage(tt.json))
			if len(got) != len(tt.want) {
				t.Fatalf("len=%d want=%d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				want := tt.want[i]
				if got[i].MediaID != want.MediaID || got[i].Role != want.Role {
					t.Fatalf("asset %d = %+v, want media=%s role=%s", i, got[i], want.MediaID, want.Role)
				}
				if (got[i].ObjectID == nil) != (want.ObjectID == nil) {
					t.Fatalf("asset %d object_id presence mismatch: %+v", i, got[i])
				}
				if want.ObjectID != nil && *got[i].ObjectID != *want.ObjectID {
					t.Fatalf("asset %d object_id=%q want %q", i, *got[i].ObjectID, *want.ObjectID)
				}
			}
		})
	}
}
