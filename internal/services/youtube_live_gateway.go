package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultYouTubeLiveAPIBaseURL = "https://www.googleapis.com/youtube/v3"

// YouTubeLiveErrorCode classifies failures from the YouTube Live Streaming
// API without exposing Google's response body to callers or logs.
type YouTubeLiveErrorCode string

const (
	YouTubeLiveAuthRequired         YouTubeLiveErrorCode = "AUTH_REQUIRED"
	YouTubeLiveInsufficientScope    YouTubeLiveErrorCode = "INSUFFICIENT_SCOPE"
	YouTubeLiveNotEnabled           YouTubeLiveErrorCode = "LIVE_NOT_ENABLED"
	YouTubeLivePermissionBlocked    YouTubeLiveErrorCode = "LIVE_PERMISSION_BLOCKED"
	YouTubeLiveRateLimited          YouTubeLiveErrorCode = "RATE_LIMITED"
	YouTubeLiveQuotaExceeded        YouTubeLiveErrorCode = "QUOTA_EXCEEDED"
	YouTubeLiveInvalidConfiguration YouTubeLiveErrorCode = "INVALID_CONFIGURATION"
	YouTubeLiveStateConflict        YouTubeLiveErrorCode = "STATE_CONFLICT"
	YouTubeLiveNotFound             YouTubeLiveErrorCode = "NOT_FOUND"
	YouTubeLiveTransientUpstream    YouTubeLiveErrorCode = "TRANSIENT_UPSTREAM"
)

// YouTubeLiveError is the safe, typed error returned by YouTubeLiveGateway.
// Reason is Google's stable error reason (for example quotaExceeded), never
// the provider's free-form message or response body.
type YouTubeLiveError struct {
	Code       YouTubeLiveErrorCode
	Operation  string
	Reason     string
	StatusCode int
	RetryAfter time.Duration
	Cause      error
}

func (e *YouTubeLiveError) Error() string {
	if e == nil {
		return "<nil YouTubeLiveError>"
	}
	if e.Operation == "" {
		return fmt.Sprintf("youtube live: %s", e.Code)
	}
	return fmt.Sprintf("youtube live %s: %s", e.Operation, e.Code)
}

func (e *YouTubeLiveError) Unwrap() error { return e.Cause }

// Is lets callers use errors.Is with another YouTubeLiveError carrying the
// same classification code, while errors.As remains available for details.
func (e *YouTubeLiveError) Is(target error) bool {
	other, ok := target.(*YouTubeLiveError)
	return ok && e != nil && other != nil && e.Code == other.Code
}

func YouTubeLiveErrorFor(code YouTubeLiveErrorCode) error {
	return &YouTubeLiveError{Code: code}
}

func IsYouTubeLiveErrorCode(err error, code YouTubeLiveErrorCode) bool {
	var liveErr *YouTubeLiveError
	return errors.As(err, &liveErr) && liveErr.Code == code
}

// YouTubeLiveGateway centralizes all YouTube Live Streaming API calls. The
// gateway owns HTTP resource paths and response/error decoding; workers only
// orchestrate the returned typed resources and never call YouTube directly.
type YouTubeLiveGateway interface {
	CreateStream(ctx context.Context, token string, input CreateStreamInput) (*YouTubeStream, error)
	CreateBroadcast(ctx context.Context, token string, input CreateBroadcastInput) (*YouTubeBroadcast, error)
	Bind(ctx context.Context, token, broadcastID, streamID string) error
	GetStream(ctx context.Context, token, streamID string) (*YouTubeStreamStatus, error)
	GetBroadcast(ctx context.Context, token, broadcastID string) (*YouTubeBroadcastStatus, error)
	Transition(ctx context.Context, token, broadcastID, target string) error
	Complete(ctx context.Context, token, broadcastID string) error
}

// CreateStreamInput describes the deterministic ingest profile sent to
// liveStreams.insert. Resolution and frame rate use YouTube's wire values.
type CreateStreamInput struct {
	Title         string
	Description   string
	IngestionType string
	Resolution    string
	FrameRate     string
	IsReusable    bool
}

// CreateBroadcastInput describes a scheduled YouTube broadcast.
type CreateBroadcastInput struct {
	Title              string
	Description        string
	PrivacyStatus      string
	ScheduledStartTime time.Time
	EnableAutoStart    bool
	EnableAutoStop     bool
	EnableDVR          bool
	RecordFromStart    bool
	MonitorStream      bool
	LatencyPreference  string
	MadeForKids        bool
}

type YouTubeStream struct {
	ID            string
	Title         string
	IngestionType string
	Resolution    string
	FrameRate     string
	IngestionInfo YouTubeIngestionInfo
	StreamStatus  string
}

// YouTubeIngestionInfo contains runtime-only ingest credentials returned by
// YouTube. It must never be serialized, logged, persisted in livestream
// configuration, or returned through an API response.
type YouTubeIngestionInfo struct {
	IngestionAddress       string `json:"-"`
	BackupIngestionAddress string `json:"-"`
	StreamName             string `json:"-"`
}

// String deliberately redacts all ingest credentials from fmt/slog output.
func (YouTubeIngestionInfo) String() string { return "<redacted YouTube ingest credentials>" }

// String and LogValue keep the containing stream safe when callers attach it
// to fmt or slog. json:"-" alone is insufficient because worker diagnostics
// may format the runtime resource directly.
func (s YouTubeStream) String() string {
	return fmt.Sprintf("YouTubeStream{ID:%q Title:%q IngestionType:%q Resolution:%q FrameRate:%q StreamStatus:%q IngestionInfo:<redacted>}", s.ID, s.Title, s.IngestionType, s.Resolution, s.FrameRate, s.StreamStatus)
}

func (s YouTubeStream) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", s.ID),
		slog.String("title", s.Title),
		slog.String("ingestion_type", s.IngestionType),
		slog.String("resolution", s.Resolution),
		slog.String("frame_rate", s.FrameRate),
		slog.String("stream_status", s.StreamStatus),
		slog.String("ingestion_info", "<redacted>"),
	)
}

type YouTubeBroadcast struct {
	ID               string
	Title            string
	PrivacyStatus    string
	ScheduledStartAt time.Time
	LifeCycleStatus  string
	BoundStreamID    string
}

type YouTubeStreamStatus struct {
	ID                  string
	StreamStatus        string
	ConfigurationIssues []string
}

type YouTubeBroadcastStatus struct {
	ID              string
	LifeCycleStatus string
	BoundStreamID   string
}

// YouTubeLiveGatewayOptions allows tests and local proxies to override the
// API base URL while production defaults to Google's v3 endpoint.
type YouTubeLiveGatewayOptions struct {
	HTTPClient *http.Client
	BaseURL    string
	Clock      func() time.Time
}

type youtubeLiveGateway struct {
	httpClient *http.Client
	baseURL    string
	clock      func() time.Time
}

// NewYouTubeLiveGateway creates the production gateway. A nil HTTP client
// uses the shared provider HTTP client factory; callers should pass the
// provider's configured client when they need custom transport behavior.
func NewYouTubeLiveGateway(options YouTubeLiveGatewayOptions) YouTubeLiveGateway {
	client := options.HTTPClient
	if client == nil {
		client = NewHTTPClient()
	}
	base := strings.TrimRight(options.BaseURL, "/")
	if base == "" {
		base = defaultYouTubeLiveAPIBaseURL
	}
	clock := options.Clock
	if clock == nil {
		clock = time.Now
	}
	return &youtubeLiveGateway{httpClient: client, baseURL: base, clock: clock}
}

// NewYouTubeLiveGatewayForService reuses the HTTP client configured on the
// existing YouTube OAuth service, avoiding a second transport configuration.
func NewYouTubeLiveGatewayForService(service *YouTubeOAuthService) YouTubeLiveGateway {
	if service == nil {
		return NewYouTubeLiveGateway(YouTubeLiveGatewayOptions{})
	}
	return NewYouTubeLiveGateway(YouTubeLiveGatewayOptions{HTTPClient: service.httpClient, Clock: service.clock})
}

func (g *youtubeLiveGateway) CreateStream(ctx context.Context, token string, input CreateStreamInput) (*YouTubeStream, error) {
	if err := validateLiveToken(token); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Title) == "" || input.IngestionType == "" || input.Resolution == "" || input.FrameRate == "" {
		return nil, newLiveClientError("create_stream", YouTubeLiveInvalidConfiguration, "missing stream profile")
	}
	payload := map[string]any{
		"snippet":        map[string]string{"title": input.Title, "description": input.Description},
		"cdn":            map[string]string{"ingestionType": input.IngestionType, "resolution": input.Resolution, "frameRate": input.FrameRate},
		"contentDetails": map[string]bool{"isReusable": input.IsReusable},
	}
	var resource youtubeStreamWire
	if err := g.doJSON(ctx, token, http.MethodPost, "/liveStreams", url.Values{"part": {"snippet,cdn,contentDetails"}}, payload, &resource, "create_stream"); err != nil {
		return nil, err
	}
	return resource.toModel(), nil
}

func (g *youtubeLiveGateway) CreateBroadcast(ctx context.Context, token string, input CreateBroadcastInput) (*YouTubeBroadcast, error) {
	if err := validateLiveToken(token); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Title) == "" || input.ScheduledStartTime.IsZero() {
		return nil, newLiveClientError("create_broadcast", YouTubeLiveInvalidConfiguration, "missing broadcast title or start time")
	}
	privacy := strings.ToLower(strings.TrimSpace(input.PrivacyStatus))
	if privacy != "private" && privacy != "unlisted" && privacy != "public" {
		return nil, newLiveClientError("create_broadcast", YouTubeLiveInvalidConfiguration, "invalid privacy status")
	}
	payload := map[string]any{
		"snippet": map[string]any{
			"title": input.Title, "description": input.Description,
			"scheduledStartTime": input.ScheduledStartTime.UTC().Format(time.RFC3339),
		},
		"status": map[string]any{"privacyStatus": privacy, "selfDeclaredMadeForKids": input.MadeForKids},
		"contentDetails": map[string]any{
			"enableAutoStart": input.EnableAutoStart, "enableAutoStop": input.EnableAutoStop,
			"enableDvr": input.EnableDVR, "recordFromStart": input.RecordFromStart,
			"monitorStream":     map[string]bool{"enableMonitorStream": input.MonitorStream},
			"latencyPreference": input.LatencyPreference,
		},
	}
	var resource youtubeBroadcastWire
	if err := g.doJSON(ctx, token, http.MethodPost, "/liveBroadcasts", url.Values{"part": {"snippet,status,contentDetails"}}, payload, &resource, "create_broadcast"); err != nil {
		return nil, err
	}
	return resource.toModel(), nil
}

func (g *youtubeLiveGateway) Bind(ctx context.Context, token, broadcastID, streamID string) error {
	if err := validateLiveToken(token); err != nil {
		return err
	}
	if broadcastID == "" || streamID == "" {
		return newLiveClientError("bind", YouTubeLiveInvalidConfiguration, "missing resource id")
	}
	return g.doJSON(ctx, token, http.MethodPost, "/liveBroadcasts/bind", url.Values{"part": {"id,contentDetails"}, "id": {broadcastID}, "streamId": {streamID}}, nil, nil, "bind")
}

func (g *youtubeLiveGateway) GetStream(ctx context.Context, token, streamID string) (*YouTubeStreamStatus, error) {
	if err := validateLiveToken(token); err != nil {
		return nil, err
	}
	if streamID == "" {
		return nil, newLiveClientError("get_stream", YouTubeLiveInvalidConfiguration, "missing stream id")
	}
	var list youtubeStreamListWire
	if err := g.doJSON(ctx, token, http.MethodGet, "/liveStreams", url.Values{"part": {"status"}, "id": {streamID}}, nil, &list, "get_stream"); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, newLiveClientError("get_stream", YouTubeLiveNotFound, "stream not found")
	}
	return list.Items[0].statusModel(), nil
}

func (g *youtubeLiveGateway) GetBroadcast(ctx context.Context, token, broadcastID string) (*YouTubeBroadcastStatus, error) {
	if err := validateLiveToken(token); err != nil {
		return nil, err
	}
	if broadcastID == "" {
		return nil, newLiveClientError("get_broadcast", YouTubeLiveInvalidConfiguration, "missing broadcast id")
	}
	var list youtubeBroadcastListWire
	if err := g.doJSON(ctx, token, http.MethodGet, "/liveBroadcasts", url.Values{"part": {"status,contentDetails"}, "id": {broadcastID}}, nil, &list, "get_broadcast"); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, newLiveClientError("get_broadcast", YouTubeLiveNotFound, "broadcast not found")
	}
	return list.Items[0].statusModel(), nil
}

func (g *youtubeLiveGateway) Transition(ctx context.Context, token, broadcastID, target string) error {
	if err := validateLiveToken(token); err != nil {
		return err
	}
	if broadcastID == "" {
		return newLiveClientError("transition", YouTubeLiveInvalidConfiguration, "missing broadcast id")
	}
	target = strings.ToLower(strings.TrimSpace(target))
	if target != "testing" && target != "live" && target != "complete" {
		return newLiveClientError("transition", YouTubeLiveInvalidConfiguration, "invalid target state")
	}
	return g.doJSON(ctx, token, http.MethodPost, "/liveBroadcasts/transition", url.Values{"part": {"status"}, "id": {broadcastID}, "broadcastStatus": {target}}, nil, nil, "transition")
}

func (g *youtubeLiveGateway) Complete(ctx context.Context, token, broadcastID string) error {
	return g.Transition(ctx, token, broadcastID, "complete")
}

func validateLiveToken(token string) error {
	if strings.TrimSpace(token) == "" {
		return newLiveClientError("request", YouTubeLiveAuthRequired, "empty access token")
	}
	return nil
}

func newLiveClientError(operation string, code YouTubeLiveErrorCode, reason string) error {
	return &YouTubeLiveError{Code: code, Operation: operation, Reason: reason}
}

func (g *youtubeLiveGateway) doJSON(ctx context.Context, token, method, path string, query url.Values, payload any, out any, operation string) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("youtube live %s: encode request: %w", operation, err)
		}
		body = bytes.NewReader(encoded)
	}
	reqURL := strings.TrimRight(g.baseURL, "/") + path
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return fmt.Errorf("youtube live %s: create request: %w", operation, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return &YouTubeLiveError{Code: YouTubeLiveTransientUpstream, Operation: operation, Cause: err}
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if readErr != nil {
		return &YouTubeLiveError{Code: YouTubeLiveTransientUpstream, Operation: operation, StatusCode: resp.StatusCode, Cause: readErr}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classifyYouTubeLiveHTTPError(operation, resp.StatusCode, responseBody, resp.Header)
	}
	if out == nil || len(responseBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return &YouTubeLiveError{Code: YouTubeLiveTransientUpstream, Operation: operation, StatusCode: resp.StatusCode, Cause: err}
	}
	return nil
}

func classifyYouTubeLiveHTTPError(operation string, status int, body []byte, headers http.Header) error {
	reason := youtubeLiveReason(body)
	code := YouTubeLiveTransientUpstream
	switch {
	case status == http.StatusUnauthorized || reason == "authError" || reason == "required":
		code = YouTubeLiveAuthRequired
	case reason == "insufficientPermissions" || reason == "insufficientScope":
		code = YouTubeLiveInsufficientScope
	case reason == "quotaExceeded" || reason == "dailyLimitExceeded" || reason == "userRateLimitExceeded":
		code = YouTubeLiveQuotaExceeded
	case reason == "liveStreamingNotEnabled":
		code = YouTubeLiveNotEnabled
	case reason == "livePermissionBlocked" || reason == "livePermissionDenied" || reason == "forbidden" || status == http.StatusForbidden:
		code = YouTubeLivePermissionBlocked
	case status == http.StatusTooManyRequests || reason == "rateLimitExceeded":
		code = YouTubeLiveRateLimited
	case status == http.StatusNotFound || reason == "notFound":
		code = YouTubeLiveNotFound
	case reason == "invalidTransition" || reason == "broadcastInconsistent" || reason == "conflict":
		code = YouTubeLiveStateConflict
	case status == http.StatusBadRequest || reason == "invalidParameter" || reason == "badRequest":
		code = YouTubeLiveInvalidConfiguration
	case status >= 500:
		code = YouTubeLiveTransientUpstream
	}
	err := &YouTubeLiveError{Code: code, Operation: operation, Reason: reason, StatusCode: status, RetryAfter: ParseThrottleHeaders(headers)}
	return err
}

func youtubeLiveReason(body []byte) string {
	var envelope struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Error.Errors) == 0 {
		return ""
	}
	return envelope.Error.Errors[0].Reason
}

type youtubeStreamWire struct {
	ID      string `json:"id"`
	Snippet struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"snippet"`
	CDN struct {
		IngestionType string `json:"ingestionType"`
		Resolution    string `json:"resolution"`
		FrameRate     string `json:"frameRate"`
		IngestionInfo struct {
			IngestionAddress       string `json:"ingestionAddress"`
			BackupIngestionAddress string `json:"backupIngestionAddress"`
			StreamName             string `json:"streamName"`
		} `json:"ingestionInfo"`
	} `json:"cdn"`
	Status struct {
		StreamStatus        string `json:"streamStatus"`
		ConfigurationIssues []struct {
			Type string `json:"type"`
		} `json:"configurationIssues"`
	} `json:"status"`
}

func (w youtubeStreamWire) toModel() *YouTubeStream {
	return &YouTubeStream{
		ID: w.ID, Title: w.Snippet.Title, IngestionType: w.CDN.IngestionType,
		Resolution: w.CDN.Resolution, FrameRate: w.CDN.FrameRate,
		IngestionInfo: YouTubeIngestionInfo{
			IngestionAddress:       w.CDN.IngestionInfo.IngestionAddress,
			BackupIngestionAddress: w.CDN.IngestionInfo.BackupIngestionAddress,
			StreamName:             w.CDN.IngestionInfo.StreamName,
		},
		StreamStatus: w.Status.StreamStatus,
	}
}

func (w youtubeStreamWire) statusModel() *YouTubeStreamStatus {
	issues := make([]string, 0, len(w.Status.ConfigurationIssues))
	for _, i := range w.Status.ConfigurationIssues {
		issues = append(issues, i.Type)
	}
	return &YouTubeStreamStatus{ID: w.ID, StreamStatus: w.Status.StreamStatus, ConfigurationIssues: issues}
}

type youtubeStreamListWire struct {
	Items []youtubeStreamWire `json:"items"`
}

type youtubeBroadcastWire struct {
	ID      string `json:"id"`
	Snippet struct {
		Title              string    `json:"title"`
		Description        string    `json:"description"`
		ScheduledStartTime time.Time `json:"scheduledStartTime"`
	} `json:"snippet"`
	Status struct {
		PrivacyStatus   string `json:"privacyStatus"`
		LifeCycleStatus string `json:"lifeCycleStatus"`
	} `json:"status"`
	ContentDetails struct {
		BoundStreamID string `json:"boundStreamId"`
	} `json:"contentDetails"`
}

func (w youtubeBroadcastWire) toModel() *YouTubeBroadcast {
	return &YouTubeBroadcast{ID: w.ID, Title: w.Snippet.Title, PrivacyStatus: w.Status.PrivacyStatus, ScheduledStartAt: w.Snippet.ScheduledStartTime, LifeCycleStatus: w.Status.LifeCycleStatus, BoundStreamID: w.ContentDetails.BoundStreamID}
}
func (w youtubeBroadcastWire) statusModel() *YouTubeBroadcastStatus {
	return &YouTubeBroadcastStatus{ID: w.ID, LifeCycleStatus: w.Status.LifeCycleStatus, BoundStreamID: w.ContentDetails.BoundStreamID}
}

type youtubeBroadcastListWire struct {
	Items []youtubeBroadcastWire `json:"items"`
}

var _ YouTubeLiveGateway = (*youtubeLiveGateway)(nil)
