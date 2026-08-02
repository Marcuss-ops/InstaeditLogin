package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestHandleGetInternalDelivery_Spec8_PublishStatus_MappingExhaustive(t *testing.T) {
	cases := []struct {
		in   models.ExternalDeliveryStatus
		want string
	}{
		{models.ExternalDeliveryStatusAccepted, "waiting_thumbnail"},
		{models.ExternalDeliveryStatusDownloading, "waiting_thumbnail"},
		{models.ExternalDeliveryStatusArtifactVerified, "waiting_thumbnail"},
		{models.ExternalDeliveryStatusIngestCompleted, "waiting_thumbnail"},
		{models.ExternalDeliveryStatusPublishing, "waiting_thumbnail"},
		{models.ExternalDeliveryStatusQueued, "scheduled"},
		{models.ExternalDeliveryStatusRetryWait, "retry_wait"},
		{models.ExternalDeliveryStatusBlockedAuth, "blocked"},
		{models.ExternalDeliveryStatusPublished, "published"},
		{models.ExternalDeliveryStatusFailed, "failed"},
		{models.ExternalDeliveryStatusDeadLetter, "failed"},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			if got := mapExternalDeliveryStatusToPublishStatus(tc.in); got != tc.want {
				t.Errorf("ExternalDeliveryStatus(%q) → publish_status want %q, got %q",
					tc.in, tc.want, got)
			}
		})
	}
}

// TestHandleGetInternalDelivery_Spec8_ThumbnailStatus_MappingExhaustive
// pins the 11-value → 3-value (= pending|applied|failed) mapping
// every (status, thumbnail_status) pair.
func TestHandleGetInternalDelivery_Spec8_ThumbnailStatus_MappingExhaustive(t *testing.T) {
	cases := []struct {
		in   models.ExternalDeliveryStatus
		want string
	}{
		{models.ExternalDeliveryStatusAccepted, "pending"},
		{models.ExternalDeliveryStatusDownloading, "pending"},
		{models.ExternalDeliveryStatusArtifactVerified, "pending"},
		{models.ExternalDeliveryStatusIngestCompleted, "pending"},
		{models.ExternalDeliveryStatusPublishing, "pending"},
		{models.ExternalDeliveryStatusQueued, "pending"},
		{models.ExternalDeliveryStatusRetryWait, "pending"},
		{models.ExternalDeliveryStatusBlockedAuth, "failed"},
		{models.ExternalDeliveryStatusPublished, "applied"},
		{models.ExternalDeliveryStatusFailed, "failed"},
		{models.ExternalDeliveryStatusDeadLetter, "failed"},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			if got := mapExternalDeliveryStatusToThumbnailStatus(tc.in); got != tc.want {
				t.Errorf("ExternalDeliveryStatus(%q) → thumbnail_status want %q, got %q",
					tc.in, tc.want, got)
			}
		})
	}
}

// TestHandleGetInternalDelivery_Spec8_Target_Resolved exercises
// the full FK chain: external_deliveries → external_destinations →
// platform_accounts → workspace_channels. Asserts that all four
// target fields are populated correctly.
func TestHandleGetInternalDelivery_Spec8_Target_Resolved(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRowExt(
		"sdel_01JABC", models.ExternalDeliveryStatusAccepted,
		"extdst_12_381",
		"", "", "", "", nil,
		nil, // no metadata → privacy=""
	)

	destinations := &fakeDestinationStorageExtended{
		rows: map[string]*models.ExternalDestination{
			"extdst_12_381": {
				ID:                "extdst_12_381",
				WorkspaceID:       12,
				PlatformAccountID: 381,
				Enabled:           true,
			},
		},
	}
	userStore := &fakeUserStoreSpec8{
		rows: map[int64]*models.PlatformAccount{
			381: {
				ID:             381,
				Platform:       models.PlatformYouTube,
				PlatformUserID: "UCxxxxxxxx",
				Username:       "Wrestling Discovery",
				Status:         models.AccountStatusActive,
			},
		},
	}
	workspaceStore := &fakeWorkspaceStoreSpec8{
		workspaces: map[int64]*models.Workspace{
			12: {ID: 12, OwnerID: 1},
		},
		bindings: map[string]*models.WorkspaceChannel{
			wsKey(12, 381): {
				WorkspaceID:       12,
				PlatformAccountID: 381,
				Enabled:           true,
			},
		},
	}

	r := newVeloxTestRouterWithDeps(t, store, destinations, userStore, workspaceStore, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("target resolved: want 200, got %d (body=%q)", w.Code, w.Body.String())
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Target.PlatformAccountID != 381 {
		t.Errorf("Target.PlatformAccountID: want 381, got %d", got.Target.PlatformAccountID)
	}
	if got.Target.ChannelID != "UCxxxxxxxx" {
		t.Errorf("Target.ChannelID: want UCxxxxxxxx, got %q", got.Target.ChannelID)
	}
	if got.Target.ChannelName != "Wrestling Discovery" {
		t.Errorf("Target.ChannelName: want Wrestling Discovery, got %q", got.Target.ChannelName)
	}
	if !got.Target.Enabled {
		t.Errorf("Target.Enabled: want true, got false")
	}
}

// TestHandleGetInternalDelivery_Spec8_Target_PartialResolution
// pins the partial-fidelity behaviour: destination exists but
// platform_account row missing → target resolves only to
// platform_account_id; channel_id/channel_name stay empty;
// operator dashboard surfaces "binding missing; reconcile needed".
func TestHandleGetInternalDelivery_Spec8_Target_PartialResolution(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRowExt(
		"sdel_01JABC", models.ExternalDeliveryStatusAccepted,
		"extdst_12_381",
		"", "", "", "", nil,
		nil,
	)

	destinations := &fakeDestinationStorageExtended{
		rows: map[string]*models.ExternalDestination{
			"extdst_12_381": {ID: "extdst_12_381", WorkspaceID: 12, PlatformAccountID: 381, Enabled: true},
		},
	}
	userStore := &fakeUserStoreSpec8{
		rows: map[int64]*models.PlatformAccount{}, // empty → missing row
	}
	workspaceStore := &fakeWorkspaceStoreSpec8{
		bindings: map[string]*models.WorkspaceChannel{}, // empty → missing binding
	}

	r := newVeloxTestRouterWithDeps(t, store, destinations, userStore, workspaceStore, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("partial resolution: want 200 (handler tolerates partial chain), got %d", w.Code)
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Target.PlatformAccountID != 381 {
		t.Errorf("Target.PlatformAccountID: want 381 (resolved via destination), got %d", got.Target.PlatformAccountID)
	}
	if got.Target.ChannelID != "" {
		t.Errorf("Target.ChannelID: want empty (platform_account row missing), got %q", got.Target.ChannelID)
	}
	if got.Target.ChannelName != "" {
		t.Errorf("Target.ChannelName: want empty (platform_account row missing), got %q", got.Target.ChannelName)
	}
	if got.Target.Enabled {
		t.Errorf("Target.Enabled: want false (binding missing), got true")
	}
}

// TestHandleGetInternalDelivery_Spec8_PrivacyFromMetadata pins
// that a JSONB metadata block with privacy_status="private" is
// surfaced verbatim on the response. Uses newVeloxTestRouter
// (no FK-chain fixtures) because the privacy helper doesn't
// reach them — keeps the test minimal.
func TestHandleGetInternalDelivery_Spec8_PrivacyFromMetadata(t *testing.T) {
	store := newFakeDeliveryStorage()
	meta := json.RawMessage(`{"privacy_status":"private","title":"Sample Title","description":"Sample Desc"}`)
	store.seedRowExt("sdel_01JABC", models.ExternalDeliveryStatusQueued,
		"", // no external_destination_id → target empty
		"", "", "", "", nil,
		meta,
	)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("privacy from metadata: want 200, got %d", w.Code)
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Privacy != "private" {
		t.Errorf("Privacy: want private, got %q", got.Privacy)
	}
	if got.PublishStatus != "scheduled" {
		t.Errorf("PublishStatus: want scheduled (queued in 11-status → §8 scheduled), got %q", got.PublishStatus)
	}
	if got.ThumbnailStatus != "pending" {
		t.Errorf("ThumbnailStatus: want pending (queued is in-flight), got %q", got.ThumbnailStatus)
	}
}

// TestHandleGetInternalDelivery_Spec8_Privacy_MalformedMetadata
// pins the lenient parser: malformed JSON → privacy=""
// (NOT a 500). Mirrors the operator-recover contract.
func TestHandleGetInternalDelivery_Spec8_Privacy_MalformedMetadata(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRowExt("sdel_01JABC", models.ExternalDeliveryStatusAccepted,
		"", "", "", "", "", nil,
		json.RawMessage(`{this is not valid json`),
	)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("malformed metadata: want 200 (lenient parser), got %d", w.Code)
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Privacy != "" {
		t.Errorf("Privacy: want empty (malformed metadata), got %q", got.Privacy)
	}
}

// TestHandleGetInternalDelivery_Spec8_PublishedAlias_YoutubeVideoID
// pins that published rows populate BOTH the new youtube_video_id
// field AND the legacy platform_media_id field with the same
// value (they alias the same source column).
func TestHandleGetInternalDelivery_Spec8_PublishedAlias_YoutubeVideoID(t *testing.T) {
	completedAt := time.Date(2026, 7, 29, 9, 4, 12, 0, time.UTC)
	store := newFakeDeliveryStorage()
	store.seedRowExt(
		"sdel_01JABC", models.ExternalDeliveryStatusPublished,
		"",
		"", "", "AbCd1234", "https://www.youtube.com/watch?v=AbCd1234",
		&completedAt,
		nil,
	)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("published alias: want 200, got %d", w.Code)
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.YouTubeVideoID != "AbCd1234" {
		t.Errorf("YouTubeVideoID: want AbCd1234, got %q", got.YouTubeVideoID)
	}
	if got.PlatformMediaID != "AbCd1234" {
		t.Errorf("PlatformMediaID (legacy alias): want AbCd1234, got %q", got.PlatformMediaID)
	}
	if got.PlatformMediaID != got.YouTubeVideoID {
		t.Errorf("PlatformMediaID (%q) and YouTubeVideoID (%q) must alias the same value",
			got.PlatformMediaID, got.YouTubeVideoID)
	}
	if got.PublishStatus != "published" {
		t.Errorf("PublishStatus: want published, got %q", got.PublishStatus)
	}
	if got.ThumbnailStatus != "applied" {
		t.Errorf("ThumbnailStatus: want applied, got %q", got.ThumbnailStatus)
	}
	if got.PublishedAt == nil || !got.PublishedAt.Equal(completedAt) {
		t.Errorf("PublishedAt: want %v (from completedAt), got %v", completedAt, got.PublishedAt)
	}
}

// TestHandleGetInternalDelivery_Spec8_FailedBlockedStatus ensures
// blocked_auth / failed / dead_letter rows map to the correct
// (publish_status, thumbnail_status) pair.
func TestHandleGetInternalDelivery_Spec8_FailedBlockedStatus(t *testing.T) {
	cases := []struct {
		name          string
		status        models.ExternalDeliveryStatus
		wantPublish   string
		wantThumbnail string
	}{
		{"blocked_auth",
			models.ExternalDeliveryStatusBlockedAuth, "blocked", "failed"},
		{"failed",
			models.ExternalDeliveryStatusFailed, "failed", "failed"},
		{"dead_letter",
			models.ExternalDeliveryStatusDeadLetter, "failed", "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeDeliveryStorage()
			store.seedRowExt("sdel_01JABC", tc.status,
				"", "", "", "", "", nil, nil)

			r := newVeloxTestRouter(t, store, "secret-token")
			w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")
			if w.Code != http.StatusOK {
				t.Fatalf("status: want 200, got %d", w.Code)
			}
			var got VeloxGetDeliveryResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.PublishStatus != tc.wantPublish {
				t.Errorf("PublishStatus: want %q, got %q", tc.wantPublish, got.PublishStatus)
			}
			if got.ThumbnailStatus != tc.wantThumbnail {
				t.Errorf("ThumbnailStatus: want %q, got %q", tc.wantThumbnail, got.ThumbnailStatus)
			}
		})
	}
}
