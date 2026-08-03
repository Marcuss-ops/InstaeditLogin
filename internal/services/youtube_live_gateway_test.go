package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestYouTubeLiveGateway_CreateStreamAndBroadcast(t *testing.T) {
	var requests []string
	var payloads []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		} else {
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("decode payload: %v", err)
			} else {
				payloads = append(payloads, payload)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/liveStreams") {
			_, _ = w.Write([]byte(`{"id":"stream-1","snippet":{"title":"Stream"},"cdn":{"ingestionType":"rtmp","resolution":"1080p","frameRate":"30fps","ingestionInfo":{"ingestionAddress":"rtmp://example","streamName":"secret"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"broadcast-1","snippet":{"title":"Broadcast","scheduledStartTime":"2026-08-03T12:00:00Z"},"status":{"privacyStatus":"unlisted","lifeCycleStatus":"ready"}}`))
	}))
	defer srv.Close()

	gateway := NewYouTubeLiveGateway(YouTubeLiveGatewayOptions{BaseURL: srv.URL})
	stream, err := gateway.CreateStream(context.Background(), "access-token", CreateStreamInput{
		Title: "Stream", IngestionType: "rtmp", Resolution: "1080p", FrameRate: "30fps",
	})
	if err != nil || stream == nil || stream.ID != "stream-1" || stream.IngestionInfo.StreamName != "secret" {
		t.Fatalf("stream=%#v err=%v", stream, err)
	}
	broadcast, err := gateway.CreateBroadcast(context.Background(), "access-token", CreateBroadcastInput{
		Title: "Broadcast", PrivacyStatus: "unlisted", ScheduledStartTime: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
	})
	if err != nil || broadcast == nil || broadcast.ID != "broadcast-1" {
		t.Fatalf("broadcast=%#v err=%v", broadcast, err)
	}
	if len(requests) != 2 || !strings.Contains(requests[0], "part=snippet%2Ccdn%2CcontentDetails") || !strings.Contains(requests[1], "part=snippet%2Cstatus%2CcontentDetails") {
		t.Fatalf("requests=%v", requests)
	}
	if len(payloads) != 2 {
		t.Fatalf("payload count = %d", len(payloads))
	}
	streamSnippet, ok := payloads[0]["snippet"].(map[string]any)
	if !ok || streamSnippet["title"] != "Stream" || streamSnippet["description"] != "" {
		t.Fatalf("stream payload snippet = %#v", payloads[0]["snippet"])
	}
	cdn, ok := payloads[0]["cdn"].(map[string]any)
	if !ok || cdn["ingestionType"] != "rtmp" || cdn["resolution"] != "1080p" || cdn["frameRate"] != "30fps" {
		t.Fatalf("stream payload cdn = %#v", payloads[0]["cdn"])
	}
	content, ok := payloads[1]["contentDetails"].(map[string]any)
	if !ok {
		t.Fatalf("broadcast contentDetails = %#v", payloads[1]["contentDetails"])
	}
	monitor, ok := content["monitorStream"].(map[string]any)
	if !ok || monitor["enableMonitorStream"] != false {
		t.Fatalf("monitorStream = %#v", content["monitorStream"])
	}
	status, ok := payloads[1]["status"].(map[string]any)
	if !ok || status["privacyStatus"] != "unlisted" {
		t.Fatalf("broadcast status = %#v", payloads[1]["status"])
	}
}

func TestYouTubeLiveGateway_OperationsAndFake(t *testing.T) {
	var calls []string
	var transitionQuery url.Values
	var transitionBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/liveBroadcasts/transition" {
			transitionQuery = r.URL.Query()
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &transitionBody)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/liveStreams":
			_, _ = w.Write([]byte(`{"items":[{"id":"s1","status":{"streamStatus":"active","configurationIssues":[{"type":"gopSizeLong"}]}}]}`))
		case "/liveBroadcasts":
			_, _ = w.Write([]byte(`{"items":[{"id":"b1","status":{"lifeCycleStatus":"live"},"contentDetails":{"boundStreamId":"s1"}}]}`))
		default:
			_, _ = w.Write([]byte(`{"id":"ok"}`))
		}
	}))
	defer srv.Close()

	g := NewYouTubeLiveGateway(YouTubeLiveGatewayOptions{BaseURL: srv.URL})
	if err := g.Bind(context.Background(), "tok", "b1", "s1"); err != nil {
		t.Fatal(err)
	}
	stream, err := g.GetStream(context.Background(), "tok", "s1")
	if err != nil || stream.StreamStatus != "active" || len(stream.ConfigurationIssues) != 1 {
		t.Fatalf("stream=%#v err=%v", stream, err)
	}
	broadcast, err := g.GetBroadcast(context.Background(), "tok", "b1")
	if err != nil || broadcast.LifeCycleStatus != "live" || broadcast.BoundStreamID != "s1" {
		t.Fatalf("broadcast=%#v err=%v", broadcast, err)
	}
	if err := g.Transition(context.Background(), "tok", "b1", "testing"); err != nil {
		t.Fatal(err)
	}
	if err := g.Complete(context.Background(), "tok", "b1"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 5 {
		t.Fatalf("calls=%v", calls)
	}
	if transitionQuery.Get("id") != "b1" || transitionQuery.Get("broadcastStatus") != "complete" || transitionQuery.Get("part") != "status" {
		t.Fatalf("transition query = %v", transitionQuery)
	}
	if len(transitionBody) != 0 {
		t.Fatalf("transition unexpectedly had a JSON body: %#v", transitionBody)
	}

	fake := &FakeYouTubeLiveGateway{}
	if _, err := fake.CreateStream(context.Background(), "fake-token", CreateStreamInput{}); err != nil {
		t.Fatal(err)
	}
	if err := fake.Complete(context.Background(), "fake-token", "b1"); err != nil {
		t.Fatal(err)
	}
	if fake.CreateStreamCalls != 1 || fake.CompleteCalls != 1 || fake.LastToken != "fake-token" {
		t.Fatalf("fake=%+v", fake)
	}
}

func TestYouTubeLiveGateway_ReusesYouTubeServiceHTTPClient(t *testing.T) {
	client := &http.Client{}
	service := &YouTubeOAuthService{httpClient: client}
	gateway, ok := NewYouTubeLiveGatewayForService(service).(*youtubeLiveGateway)
	if !ok {
		t.Fatal("gateway has unexpected concrete type")
	}
	if gateway.httpClient != client {
		t.Fatal("gateway did not reuse the YouTube service HTTP client")
	}
}

func TestYouTubeLiveGateway_RedactsRuntimeStreamSecrets(t *testing.T) {
	stream := YouTubeStream{ID: "s1", Title: "title", IngestionInfo: YouTubeIngestionInfo{
		IngestionAddress: "rtmps://secret-address", StreamName: "secret-key",
	}}
	encoded, err := json.Marshal(stream)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{string(encoded), stream.String(), fmt.Sprintf("%+v", stream)} {
		if strings.Contains(output, "secret-key") || strings.Contains(output, "secret-address") {
			t.Fatalf("formatted stream leaked ingest secret: %s", output)
		}
	}
}

func TestYouTubeLiveGateway_ClassifiesErrorsWithoutLeakingBody(t *testing.T) {
	cases := []struct {
		status int
		reason string
		want   YouTubeLiveErrorCode
	}{
		{401, "", YouTubeLiveAuthRequired},
		{403, "insufficientPermissions", YouTubeLiveInsufficientScope},
		{403, "liveStreamingNotEnabled", YouTubeLiveNotEnabled},
		{403, "livePermissionBlocked", YouTubeLivePermissionBlocked},
		{403, "quotaExceeded", YouTubeLiveQuotaExceeded},
		{404, "notFound", YouTubeLiveNotFound},
		{409, "invalidTransition", YouTubeLiveStateConflict},
		{400, "invalidParameter", YouTubeLiveInvalidConfiguration},
		{429, "", YouTubeLiveRateLimited},
		{503, "backendError", YouTubeLiveTransientUpstream},
	}
	for _, tc := range cases {
		t.Run(string(tc.want), func(t *testing.T) {
			secret := "sensitive-provider-body"
			body := []byte(`{"error":{"errors":[{"reason":"` + tc.reason + `","message":"` + secret + `"}]}}`)
			err := classifyYouTubeLiveHTTPError("test", tc.status, body, http.Header{})
			if !IsYouTubeLiveErrorCode(err, tc.want) {
				t.Fatalf("err=%#v want=%s", err, tc.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked body: %v", err)
			}
		})
	}
}

func TestYouTubeLiveGateway_InputErrorsAndSentinel(t *testing.T) {
	gateway := NewYouTubeLiveGateway(YouTubeLiveGatewayOptions{BaseURL: "http://127.0.0.1:1"})
	_, err := gateway.CreateStream(context.Background(), "", CreateStreamInput{})
	if !IsYouTubeLiveErrorCode(err, YouTubeLiveAuthRequired) {
		t.Fatalf("err=%v", err)
	}
	_, err = gateway.GetBroadcast(context.Background(), "tok", "")
	if !IsYouTubeLiveErrorCode(err, YouTubeLiveInvalidConfiguration) {
		t.Fatalf("err=%v", err)
	}
	if !errors.Is(err, YouTubeLiveErrorFor(YouTubeLiveInvalidConfiguration)) {
		t.Fatalf("errors.Is failed: %v", err)
	}
}
