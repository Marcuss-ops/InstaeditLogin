package api

import (
	"encoding/json"
	"testing"
)

func TestArtifactReferenceVideoCreateResult(t *testing.T) {
	raw := json.RawMessage(`{"status":"SUCCEEDED","result":{"video_id":"video_123","final_video":{"asset_id":"asset_456","media_url":"https://master.example/api/v1/artifacts/asset_456/download","size_bytes":12345}}}`)
	gotURL, gotSize, err := artifactReference(raw)
	if err != nil {
		t.Fatal(err)
	}
	if gotURL != "https://master.example/api/v1/artifacts/asset_456/download" || gotSize != 12345 {
		t.Fatalf("artifactReference() = (%q, %d), want final_video URL and size", gotURL, gotSize)
	}
}
