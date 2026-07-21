package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestYouTubeGetTokenInfo_HappyPath exercises the canonical path:
// mock Google's /oauth2/v3/tokeninfo to reply 200 with a fully-shaped
// JSON body, then assert every field on YouTubeTokenInfo is hydrated
// correctly AND the HasUpload / HasReadonly derived flags flip to true.
// This is the contract pkg/api/handlers.go::handleValidateAccount
// (step 2 of the 4-step YouTube OAuth readiness pipeline) relies on.
func TestYouTubeGetTokenInfo_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/v3/tokeninfo", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("access_token"); got != "fresh-access-token" {
			t.Errorf("tokeninfo endpoint got access_token=%q, want fresh-access-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"aud":"%s","azp":"%s","scope":"%s %s openid email profile","expires_in":3599,"access_type":"offline","email":"operator@example.com"}`,
			"test-youtube-client-id",
			"test-youtube-client-id",
			"https://www.googleapis.com/auth/youtube.upload",
			"https://www.googleapis.com/auth/youtube.readonly",
		)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	info, err := svc.GetTokenInfo(context.Background(), "fresh-access-token")
	if err != nil {
		t.Fatalf("GetTokenInfo happy-path: unexpected error: %v", err)
	}
	if info.Aud != "test-youtube-client-id" {
		t.Errorf("Aud: got %q, want test-youtube-client-id", info.Aud)
	}
	if info.Azp != "test-youtube-client-id" {
		t.Errorf("Azp: got %q, want test-youtube-client-id", info.Azp)
	}
	if !info.HasUpload {
		t.Errorf("HasUpload: want true, got false (Scope=%q)", info.Scope)
	}
	if !info.HasReadonly {
		t.Errorf("HasReadonly: want true, got false (Scope=%q)", info.Scope)
	}
	if info.ExpiresIn != 3599*time.Second {
		t.Errorf("ExpiresIn: got %s, want 3599s", info.ExpiresIn)
	}
	if info.Email != "operator@example.com" {
		t.Errorf("Email: got %q, want operator@example.com", info.Email)
	}
}

// TestYouTubeGetTokenInfo_MissingUploadScope asserts the SHAPE of the
// error the handler maps to 422 + reauth_required. The Hard-fail
// contract is: a token that scopes youtube.readonly but NOT
// youtube.upload MUST surface as a google-returned 400 (the endpoint
// does run; the scope-shape check is the handler's job). Both shapes
// are exercised here as separate subtests so a regression on the
// Google-response path doesn't accidentally over-trigger the
// HasUpload-false code path.
//
// This is the step-2 failure matrix handleValidateAccount depends on.
func TestYouTubeGetTokenInfo_SurfaceAllContractShapes(t *testing.T) {
	t.Run("empty_access_token_rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.NewServeMux())
		defer srv.Close()
		svc := newTestYouTubeService(srv)
		if _, err := svc.GetTokenInfo(context.Background(), ""); err == nil {
			t.Fatal("expected error on empty access token, got nil")
		}
	})

	t.Run("google_400_invalid_token_surfaces_wrapped_error", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth2/v3/tokeninfo", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"invalid_token","error_description":"Token expired or revoked"}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		_, err := svc.GetTokenInfo(context.Background(), "stale-token")
		if err == nil {
			t.Fatal("expected error on Google 400 response, got nil")
		}
		if !strings.Contains(err.Error(), "400") {
			t.Errorf("error message must contain the literal status code 400 (handler reads it for 422 mapping); got %v", err)
		}
		if !strings.Contains(err.Error(), "invalid_token") {
			t.Errorf("error message must contain Google's invalid_token payload for the audit log; got %v", err)
		}
	})

	t.Run("garbage_body_decode_error_is_plain_wrapped", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth2/v3/tokeninfo", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `[this is definitely not valid json`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		_, err := svc.GetTokenInfo(context.Background(), "good-shape-bad-body")
		if err == nil {
			t.Fatal("expected decode error on garbage body, got nil")
		}
		if !strings.Contains(err.Error(), "decode") {
			t.Errorf("non-token error must mention decode so handler classifies it transient (NOT reauth); got %v", err)
		}
	})

	t.Run("missing_upload_scope_keeps_HasUpload_false", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth2/v3/tokeninfo", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"aud":"test-youtube-client-id","azp":"test-youtube-client-id","scope":"https://www.googleapis.com/auth/youtube.readonly openid email profile","expires_in":300}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		info, err := svc.GetTokenInfo(context.Background(), "readonly-only-token")
		if err != nil {
			t.Fatalf("scope-missing: unexpected error: %v", err)
		}
		if info.HasUpload {
			t.Errorf("HasUpload on readonly-only token: want false, got true (handler would let it through step 2 incorrectly)")
		}
		if !info.HasReadonly {
			t.Errorf("HasReadonly on readonly-only token: want true, got false (would force-fail step 2 incorrectly)")
		}
	})

	t.Run("missing_readonly_scope_keeps_HasReadonly_false", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth2/v3/tokeninfo", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"aud":"test-youtube-client-id","scope":"https://www.googleapis.com/auth/youtube.upload openid email profile","expires_in":300}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		info, err := svc.GetTokenInfo(context.Background(), "upload-only-token")
		if err != nil {
			t.Fatalf("scope-missing: unexpected error: %v", err)
		}
		if !info.HasUpload {
			t.Errorf("HasUpload on upload-only token: want true, got false")
		}
		if info.HasReadonly {
			t.Errorf("HasReadonly on upload-only token: want false, got true (the handler needs readonly for channels.list)")
		}
	})
}

// TestYouTubeCanaryUpload_HappyPath mocks the full canary pipeline on
// a single httptest server (testClient re-routes every Google URL
// through the same mux):
//
//  1. POST /upload/youtube/v3/videos?uploadType=resumable returns a
//     Location: /canary-session header. The mock also asserts the
//     metadata body contains the INSTAEDIT-OAUTH-CANARY-{channel}-{ts}
//     title prefix, the expected channel id embedded, and the
//     status.privacyStatus="private" guard.
//  2. PUT /canary-session returns 200 + a synthesized
//     {"id":"<videoID>"} terminal body so putChunk reports the
//     video id.
//  3. GET /youtube/v3/videos?id=<videoID>&part=snippet,status,processingDetails
//     returns the same video id with snippet.channelId equal to
//     the expected channel — the post-upload reconcile that
//     step-4 uses as the source of truth.
//
// Asserts: err == nil, result.VideoID == <videoID>,
// result.UploadedChannelID == "UCexpectedChannelID".
func TestYouTubeCanaryUpload_HappyPath(t *testing.T) {
	const (
		expectedChannel = "UCexpectedChannelID"
		canaryVideoID   = "viabc123def4"
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("uploadType") != "resumable" {
			t.Errorf("canary init: uploadType got %q, want resumable", r.URL.Query().Get("uploadType"))
		}
		body, _ := io.ReadAll(r.Body)
		var meta map[string]interface{}
		if jerr := json.Unmarshal(body, &meta); jerr != nil {
			t.Errorf("canary init: metadata is not JSON: %v / body=%s", jerr, string(body))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		snippet, _ := meta["snippet"].(map[string]interface{})
		if snippet == nil {
			t.Errorf("canary init: missing snippet block in metadata; got %+v", meta)
		} else {
			title, _ := snippet["title"].(string)
			if !strings.HasPrefix(title, "INSTAEDIT-OAUTH-CANARY-") {
				t.Errorf("canary init: title %q does not start with INSTAEDIT-OAUTH-CANARY-", title)
			}
			if !strings.Contains(title, expectedChannel) {
				t.Errorf("canary init: title %q must embed the expected channel id %s", title, expectedChannel)
			}
		}
		status, _ := meta["status"].(map[string]interface{})
		if status == nil {
			t.Errorf("canary init: missing status block in metadata")
		} else if ps, _ := status["privacyStatus"].(string); ps != "private" {
			t.Errorf("canary init: privacyStatus got %q, want private (the canary must NOT land on the operator's public tab)", ps)
		}
		w.Header().Set("Location", "/canary-session")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/canary-session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("canary session: expected PUT, got %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Content-Range") == "" {
			t.Errorf("canary session: missing Content-Range header on final PUT")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"%s"}`, canaryVideoID)
	})
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != canaryVideoID {
			t.Errorf("videos.list: id got %q, want %s", r.URL.Query().Get("id"), canaryVideoID)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"items":[{"id":"%s","snippet":{"channelId":"%s","title":"canary"}}]}`, canaryVideoID, expectedChannel)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	res, err := svc.CanaryUpload(context.Background(), "fresh-access-token", expectedChannel)
	if err != nil {
		t.Fatalf("canary happy-path: unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("canary happy-path: result is nil")
		return
	}
	if res.VideoID != canaryVideoID {
		t.Errorf("canary VideoID: got %q, want %s", res.VideoID, canaryVideoID)
	}
	if res.UploadedChannelID != expectedChannel {
		t.Errorf("canary UploadedChannelID: got %q, want %q", res.UploadedChannelID, expectedChannel)
	}
}

// TestYouTubeCanaryUpload_AllContractShapes exercises the four
// failure surfaces the handler maps to its 422 / transient
// classification:
//
//   - bind-mismatch (snippet.channelId != expected) → wrap
//     ErrYouTubeChannelMismatch → handler → 422 + reauth_required.
//   - initiate 4xx (POST /upload returns 400) → wrap
//     ErrYouTubeCanaryRejected (the "canary upload rejected by
//     videos.insert" sentinel) → handler → 422 + reauth_required.
//   - PUT 5xx transient → plain wrapped (NOT a sentinel) → handler
//     treats as transient, next-sync retry.
//   - empty input fields → fast-fail with no network round trip.
//
// The bind-mismatch case is the canonical P2 hardening scenario:
// the OAuth grant produced a video on the WRONG channel (you can
// reach videos.insert successfully but the grant was silently
// re-bound to a Brand Account the operator didn't intend). The
// worker MUST escalate to reauth_required so the operator picks up
// the drift before the next upload-attempt lands their content
// somewhere unauthorised.
func TestYouTubeCanaryUpload_AllContractShapes(t *testing.T) {
	t.Run("bind_mismatch_returns_typed_sentinel", func(t *testing.T) {
		const (
			expectedChannel = "UCexpectedChannelID"
			canaryVideoID   = "vibindmismatch01"
			actualChannel   = "UCdifferentChannelID"
		)
		mux := http.NewServeMux()
		mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/canary-session")
		})
		mux.HandleFunc("/canary-session", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"%s"}`, canaryVideoID)
		})
		mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			// snippet.channelId intentionally ≠ expected — bind-mismatch.
			fmt.Fprintf(w, `{"items":[{"id":"%s","snippet":{"channelId":"%s","title":"canary"}}]}`, canaryVideoID, actualChannel)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		_, err := svc.CanaryUpload(context.Background(), "fresh-access-token", expectedChannel)
		if err == nil {
			t.Fatal("canary bind-mismatch: expected error, got nil")
		}
		if !errors.Is(err, ErrYouTubeChannelMismatch) {
			t.Errorf("bind-mismatch MUST wrap services.ErrYouTubeChannelMismatch so the handler routes it to 422 + reauth; got %v", err)
		}
		if !strings.Contains(err.Error(), actualChannel) {
			t.Errorf("bind-mismatch error must surface the actually-uploaded channel for the operator log; got %v", err)
		}
	})

	t.Run("initiate_4xx_returns_canary_rejected_sentinel", func(t *testing.T) {
		const expectedChannel = "UCexpectedChannelID"
		mux := http.NewServeMux()
		mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"invalid video metadata"}}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		_, err := svc.CanaryUpload(context.Background(), "fresh-access-token", expectedChannel)
		if err == nil {
			t.Fatal("canary init 4xx: expected error, got nil")
		}
		if !errors.Is(err, ErrYouTubeCanaryRejected) {
			t.Errorf("init 4xx MUST wrap ErrYouTubeCanaryRejected so handler routes to 422; got %v", err)
		}
	})

	t.Run("put_5xx_returns_plain_wrapped_transient", func(t *testing.T) {
		const expectedChannel = "UCexpectedChannelID"
		mux := http.NewServeMux()
		mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/canary-session")
		})
		mux.HandleFunc("/canary-session", func(w http.ResponseWriter, r *http.Request) {
			// 503 — transient. putChunk formats this as
			// 'server error (status 503)' which the canary
			// helper explicitly treats as transient.
			http.Error(w, "google internal error", http.StatusServiceUnavailable)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		_, err := svc.CanaryUpload(context.Background(), "fresh-access-token", expectedChannel)
		if err == nil {
			t.Fatal("canary PUT 5xx: expected error, got nil")
		}
		if errors.Is(err, ErrYouTubeCanaryRejected) {
			t.Errorf("put 5xx MUST NOT wrap ErrYouTubeCanaryRejected (next-sync retry should handle it); got %v", err)
		}
		if errors.Is(err, ErrYouTubeChannelMismatch) {
			t.Errorf("put 5xx MUST NOT wrap ErrYouTubeChannelMismatch (channel drift is unrelated to a 503); got %v", err)
		}
		if !strings.Contains(err.Error(), "503") && !strings.Contains(err.Error(), "server error") {
			t.Errorf("put 5xx error must preserve the underlying transient signal for the handler log; got %v", err)
		}
	})

	// post_upload_videos_list_5xx_transient: the videos.list call
	// CanaryUpload issues AFTER the PUT chunk succeeds can return 5xx
	// (typically because the freshly-uploaded video row has not yet
	// been indexed by Google). This must stay on the transient branch
	// — next-sync retry handles it; flagging reauth for a 503 here
	// would lock out the operator for an indexing blip, not a grant
	// drift. Closes the test gap that the canary's happy/bind/put-
	// rejection coverage doesn't capture (those cover the upload
	// legs, not the post-upload reconcile leg).
	t.Run("post_upload_videos_list_5xx_transient", func(t *testing.T) {
		const (
			expectedChannel = "UCexpectedChannelID"
			canaryVideoID   = "vipostupload503"
		)
		mux := http.NewServeMux()
		mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/canary-session")
		})
		mux.HandleFunc("/canary-session", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"%s"}`, canaryVideoID)
		})
		mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "transient indexing lag", http.StatusServiceUnavailable)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		_, err := svc.CanaryUpload(context.Background(), "fresh-access-token", expectedChannel)
		if err == nil {
			t.Fatal("videos.list 5xx post-upload: expected error, got nil")
		}
		if errors.Is(err, ErrYouTubeCanaryRejected) {
			t.Errorf("post-upload videos.list 5xx MUST NOT escalate to ErrYouTubeCanaryRejected (videos are indexed async; next-sync retry is correct); got %v", err)
		}
		if errors.Is(err, ErrYouTubeChannelMismatch) {
			t.Errorf("post-upload videos.list 5xx MUST NOT escalate to ErrYouTubeChannelMismatch (channel drift is unrelated); got %v", err)
		}
		if !strings.Contains(err.Error(), "videos.list") {
			t.Errorf("post-upload videos.list 5xx error must mention the videos.list call site for the handler log; got %v", err)
		}
	})

	t.Run("empty_inputs_fast_fail_without_network", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			t.Error("empty-input canary MUST NOT issue any outbound request")
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		if _, err := svc.CanaryUpload(context.Background(), "valid-token", ""); err == nil {
			t.Errorf("empty expectedChannelID: expected error, got nil")
		}
		if _, err := svc.CanaryUpload(context.Background(), "", "UCexpected"); err == nil {
			t.Errorf("empty accessToken: expected error, got nil")
		}
	})
}

// TestIsHardRejection4xxStatus_UpstreamFormatLocked documents AND
// locks the two upstream methods' error-message format that
// isHardRejection4xxStatus parses. The classifier is regex-based on
// err.Error() (a deliberate scope trade-off documented in
// isHardRejection4xxStatus's godoc); a future refactor of
// initiateResumableSession or putChunk's format string would
// silently break the classifier without a compile error. This test
// pins the upstream shape for every status code the canary cares
// about — 400, 401, 403, 404, 408, 409, 410, 422 (reauth-bound),
// 429 + 423 + 5xx (transient, stay on next-sync retry), and 451
// (reauth). Adding a new enum value to the helper requires adding a
// matching case here AND a code-site change; the production logic
// is derived from this test via the matching table.
//
// The test exercises BOTH upstream message shapes the classifier
// could encounter:
//
//   - initiateResumableSession: "init session failed (status N): %s"
//   - putChunk: "unexpected PUT response (status N): %s" /
//     "rate limited (status N, retry_after=%s)" /
//     "server error (status N, retry_after=%s)" or
//     "server error (status N)" /
//     "failed to parse upload completion response: %w"
//
// If Google adds new shapes (or upstream flips the format verb),
// the closure to ADD cases is here, not in the helper's doc —
// production code stays ignorant of the testbed.
func TestIsHardRejection4xxStatus_UpstreamFormatLocked(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// reauth-bound — explicit enumerated table
		{"initiate_400_bad_metadata", fmt.Errorf("init session failed (status 400): bad metadata"), true},
		{"initiate_401_token", fmt.Errorf("init session failed (status 401): bad token"), true},
		{"initiate_403_forbidden", fmt.Errorf("init session failed (status 403): forbidden"), true},
		{"initiate_404_gone", fmt.Errorf("init session failed (status 404): resource not found"), true},
		{"initiate_408_timeout", fmt.Errorf("init session failed (status 408): request timeout"), true},
		{"initiate_409_conflict", fmt.Errorf("init session failed (status 409): channel state conflict"), true},
		{"initiate_410_permanent_gone", fmt.Errorf("init session failed (status 410): channel deleted"), true},
		{"initiate_422_unprocessable", fmt.Errorf("init session failed (status 422): metadata refused"), true},
		{"initiate_451_legal_block", fmt.Errorf("init session failed (status 451): jurisdictional unavailable"), true},

		// putChunk 4xx matched against the SAME table
		{"put_400_unexpected", fmt.Errorf("unexpected PUT response (status 400): bad request body"), true},
		{"put_401_unexpected", fmt.Errorf("unexpected PUT response (status 401): token revoked mid-upload"), true},
		{"put_403_unexpected", fmt.Errorf("unexpected PUT response (status 403): forbidden"), true},
		{"put_404_unexpected", fmt.Errorf("unexpected PUT response (status 404): session uri dead"), true},
		{"put_422_unexpected", fmt.Errorf("unexpected PUT response (status 422): metadata refused"), true},

		// transient — explicitly NOT in the table, must stay on next-sync retry
		{"initiate_429_rate_limit", fmt.Errorf("init session failed (status 429): quota exceeded"), false},
		{"initiate_423_locked", fmt.Errorf("init session failed (status 423): channel locked"), false},
		{"initiate_500_internal", fmt.Errorf("init session failed (status 500): google internal"), false},
		{"initiate_502_bad_gateway", fmt.Errorf("init session failed (status 502): upstream bad"), false},
		{"initiate_503_unavailable", fmt.Errorf("init session failed (status 503): unavailable"), false},
		{"initiate_504_gateway_timeout", fmt.Errorf("init session failed (status 504): upstream timeout"), false},

		{"put_429_rate_limit", fmt.Errorf("rate limited (status 429, retry_after=2s)"), false},
		{"put_503_server_error_with_retry_after", fmt.Errorf("server error (status 503, retry_after=5s)"), false},
		{"put_500_server_error_bare", fmt.Errorf("server error (status 500)"), false},
		{"put_429_unexpected_put_response", fmt.Errorf("unexpected PUT response (status 429): malformed"), false},

		// no (status N) substring — decode / network / ctx-cancellation
		{"put_200_decode_body", fmt.Errorf("failed to parse upload completion response: %w", fmt.Errorf("bad json")), false},
		{"network_dial_failure", fmt.Errorf("dial tcp: lookup google: no such host"), false},
		{"ctx_canceled", fmt.Errorf("context canceled"), false},
		{"nil_error_safety", nil, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := isHardRejection4xxStatus(tc.err); got != tc.want {
				if tc.err != nil {
					t.Errorf("isHardRejection4xxStatus(%q) = %v, want %v", tc.err.Error(), got, tc.want)
				} else {
					t.Errorf("isHardRejection4xxStatus(nil) = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
