package worker

import (
	"encoding/json"
	"strconv"
)

// publishSnapshotMetadata is the immutable, per-target part of the upload
// job metadata created by ContentPreparationWorker. Keeping this decoder in
// one place makes the upload and publish phases consume the same snapshot.
type publishSnapshotMetadata struct {
	Title                  string   `json:"title"`
	Description            string   `json:"description"`
	Tags                   []string `json:"tags,omitempty"`
	ThumbnailMediaID       string   `json:"thumbnail_media_id,omitempty"`
	CoverTemplateVersionID *int64   `json:"cover_template_version_id,omitempty"`
	Language               string   `json:"language,omitempty"`
	PrivacyStatus          string   `json:"privacy_status,omitempty"`
}

type publishJobMetadata struct {
	ContentPackageID int64                              `json:"content_package_id"`
	PublishSnapshots map[string]publishSnapshotMetadata `json:"publish_snapshots"`
}

func contentPackageIDFromMetadata(metadata []byte) (int64, bool) {
	if len(metadata) == 0 {
		return 0, false
	}
	var envelope publishJobMetadata
	if err := json.Unmarshal(metadata, &envelope); err != nil || envelope.ContentPackageID <= 0 {
		return 0, false
	}
	return envelope.ContentPackageID, true
}

func snapshotForAccount(metadata []byte, accountID int64) (publishSnapshotMetadata, bool) {
	if len(metadata) == 0 || accountID <= 0 {
		return publishSnapshotMetadata{}, false
	}
	var envelope publishJobMetadata
	if err := json.Unmarshal(metadata, &envelope); err != nil {
		return publishSnapshotMetadata{}, false
	}
	snapshot, ok := envelope.PublishSnapshots[strconv.FormatInt(accountID, 10)]
	return snapshot, ok
}
