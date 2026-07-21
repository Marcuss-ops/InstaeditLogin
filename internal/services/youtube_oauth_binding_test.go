package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestYouTubeValidateChannelBinding_Match verifies that a single-
// channel grant returns nil when the channel id matches the expected
// id. exercises the happy path of services.YouTubeChannelBinder.
func TestYouTubeValidateChannelBinding_Match(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items: []youtubeChannel{
				{ID: "UCexpectedChanID", Snippet: youtubeChannelSnippet{Title: "My Channel"}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	if err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestYouTubeValidateChannelBinding_MultiChannelIncludesExpected
// verifies the multi-channel case (a single grant can manage up to
// 100 channels). The expected id is present in the set, so the check
// succeeds even though N=3 channels are returned.
func TestYouTubeValidateChannelBinding_MultiChannelIncludesExpected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items: []youtubeChannel{
				{ID: "UCmanagedByA", Snippet: youtubeChannelSnippet{Title: "First"}},
				{ID: "UCexpectedChanID", Snippet: youtubeChannelSnippet{Title: "Second"}},
				{ID: "UCmanagedByB", Snippet: youtubeChannelSnippet{Title: "Third"}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	if err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestYouTubeValidateChannelBinding_Mismatch verifies that a grant
// reporting only DIFFERENT channel ids returns ErrYouTubeChannelMismatch
// (detectable via errors.Is). The expected id is NOT in the returned set.
func TestYouTubeValidateChannelBinding_Mismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items: []youtubeChannel{
				{ID: "UCdifferentChanID", Snippet: youtubeChannelSnippet{Title: "Some Other Channel"}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID")
	if err == nil {
		t.Fatalf("expected ErrYouTubeChannelMismatch, got nil")
	}
	if !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("expected errors.Is to match ErrYouTubeChannelMismatch, got error: %v", err)
	}
	// Sanity: the diagnostic message includes BOTH the expected id and
	// the actual channel set so the operator sees what drifted.
	if !strings.Contains(err.Error(), "UCexpectedChanID") {
		t.Errorf("expected error message to include expected channel id, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "UCdifferentChanID") {
		t.Errorf("expected error message to include actual channel id, got %q", err.Error())
	}
}

// TestYouTubeValidateChannelBinding_ZeroChannels verifies that an
// empty channels.list response is treated as a structural mismatch
// (the grant has lost all bindings). Returns ErrYouTubeChannelMismatch.
func TestYouTubeValidateChannelBinding_ZeroChannels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{Items: []youtubeChannel{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID")
	if !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Fatalf("expected ErrYouTubeChannelMismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), "0 channels") {
		t.Errorf("expected message to mention 0 channels, got %q", err.Error())
	}
}

// TestYouTubeValidateChannelBinding_Transient5xx verifies that a
// non-200 response from channels.list returns a plain error WITHOUT
// wrapping ErrYouTubeChannelMismatch. The worker must treat this as
// transient and NOT flag the platform_account reauth_required.
func TestYouTubeValidateChannelBinding_Transient5xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "google internal error", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID")
	if err == nil {
		t.Fatalf("expected error on 5xx, got nil")
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("5xx must NOT wrap ErrYouTubeChannelMismatch (worker would flag reauth on transient); got %v", err)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected message to include status code 503, got %q", err.Error())
	}
}

// TestYouTubeValidateChannelBinding_PaginationAcrossThreePages
// verifies the pagination behaviour introduced by the rewrite of
// ValidateChannelBinding: a grant whose expected channel id lives
// ONLY on the third page of channels.list?mine=true (i.e. position
// >50 in the manager's channel set) is correctly recognized. The
// stateful httptest handler returns three pages:
//
//	page 1 (no pageToken)        : 50 channels (no expected)
//	page 2 (pageToken=page2t)    : 50 channels (no expected)
//	page 3 (pageToken=page3t)    : 10 channels, expected at index 5
//
// Without pagination the old single-GET path would have invisibly
// truncated at page 1 → ErrYouTubeChannelMismatch. With pagination,
// the loop follows nextPageToken through all three, finds the
// expected, and returns nil.
//
// Side-checks:
//   - handler.calls == 3 confirms the loop actually paged (and not
//     a single-shot GET that happened to find the expected by luck).
//   - The handler's last query string must carry pageToken=page3t —
//     guards against an off-by-one where pagination stops at page 2.
func TestYouTubeValidateChannelBinding_PaginationAcrossThreePages(t *testing.T) {
	const expected = "UCexpectedChanIDp3" // unique to page 3, index 5

	var handlerCalls int
	var lastPageToken string
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		lastPageToken = r.URL.Query().Get("pageToken")

		var (
			items         []youtubeChannel
			nextPageToken string
		)
		switch lastPageToken {
		case "":
			items = make([]youtubeChannel, 50)
			for i := range items {
				items[i] = youtubeChannel{ID: fmt.Sprintf("UCp1-%03d", i)}
			}
			nextPageToken = "page2t"
		case "page2t":
			items = make([]youtubeChannel, 50)
			for i := range items {
				items[i] = youtubeChannel{ID: fmt.Sprintf("UCp2-%03d", i)}
			}
			nextPageToken = "page3t"
		default: // page3t — final page
			items = make([]youtubeChannel, 10)
			for i := range items {
				if i == 5 {
					items[i] = youtubeChannel{ID: expected}
				} else {
					items[i] = youtubeChannel{ID: fmt.Sprintf("UCp3-%03d", i)}
				}
			}
			// nextPageToken deliberately empty — final page
		}

		_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items:         items,
			NextPageToken: nextPageToken,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	if err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", expected); err != nil {
		t.Fatalf("expected nil (expected channel is on page 3), got: %v", err)
	}
	if handlerCalls != 3 {
		t.Errorf("expected 3 server hits (page1 + page2 + page3), got %d", handlerCalls)
	}
	if lastPageToken != "page3t" {
		t.Errorf("expected last request to carry pageToken=page3t, got %q", lastPageToken)
	}
}

// TestYouTubeValidateChannelBinding_SafetyCapReachedAt200_ReturnsMismatch
// exercises the maxChannelsPerGrant (200) safety cap path. The
// production ValidateChannelBinding loop has a hard upper bound of
// 200 unique channel ids; exceeding it returns ErrYouTubeChannelMismatch
// wrapped with "safety cap reached" without making a 5th page request.
// Pre-2026 InstaEdit would silently act on the wrong channel past
// position 200; with pagination in place, the test pins the exact
// short-circuit behaviour so a regression that flips the cap off
// (infinite loop) or below 200 (premature rejection) is detectable.
//
// 4 pages x 50 channels = 200 unique ids. Expected ID NOT in any of
// them. Page 4 returns a non-empty nextPageToken (`tok-5`) so a
// regression that "trusts the empty token only" would still call page
// 5; the requestCount assertion catches that.
func TestYouTubeValidateChannelBinding_SafetyCapReachedAt200_ReturnsMismatch(t *testing.T) {
	// single-goroutine invariant: httptest.NewServer dispatches each request
	// on the call goroutine serially, so a plain int counter is race-free
	// WITHOUT locking. Same pattern is used by the sibling
	// TestYouTubeValidateChannelBinding_PaginationAcrossThreePages test.
	var handlerCalls int
	var lastPageToken string // closure-captured; asserted against the would-have-been-next token

	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, q *http.Request) {
		handlerCalls++
		lastPageToken = q.URL.Query().Get("pageToken")
		items := make([]map[string]string, 0, 50)
		for i := 0; i < 50; i++ {
			items = append(items, map[string]string{"id": fmt.Sprintf("UC-cap-%d-%d", handlerCalls, i)})
		}
		// Always return a non-empty nextPageToken to prove the loop
		// short-circuits BEFORE the 5th call, NOT on natural-empty-token.
		payload := map[string]any{
			"items":         items,
			"nextPageToken": fmt.Sprintf("tok-%d", handlerCalls+1),
		}
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(payload)
		w.Write(data)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	s := newTestYouTubeService(ts)
	err := s.ValidateChannelBinding(context.Background(), "fake-access-token", "UC-expected-but-never-present")
	if err == nil {
		t.Fatal("want ErrYouTubeChannelMismatch, got nil")
	}
	if !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("want ErrYouTubeChannelMismatch wrapped; got: %v", err)
	}
	// Typed-struct assertion (replaces the old strings.Contains
	// brittleness on "safety cap reached" + "first 200 unique
	// channel ids"). The struct's Cap field now carries the value
	// the operator-facing substring used to embed, so the test
	// pin moves from "the message string contains 200" to "the
	// typed Cap field is 200". A regression that flips the cap
	// off (infinite loop) or below 200 (premature rejection) is
	// detectable at the struct level rather than by message grep.
	var safetyCap *ErrChannelListSafetyCap
	if !errors.As(err, &safetyCap) {
		t.Errorf("want *ErrChannelListSafetyCap on the cap path; got %T: %v", err, err)
	} else if safetyCap.Cap != 200 {
		t.Errorf("want Cap=200 in typed safety cap error; got %d", safetyCap.Cap)
	} else if safetyCap.Expected != "UC-expected-but-never-present" {
		t.Errorf("want Expected=\"UC-expected-but-never-present\"; got %q", safetyCap.Expected)
	}
	// The loop MUST short-circuit BEFORE page 5; with 4 pages of 50
	// each, we expect exactly 4 HTTP calls, NOT 5+.
	if handlerCalls != 4 {
		t.Errorf("loop must short-circuit before page 5; want 4 page(s) requested, got %d", handlerCalls)
	}
	// Production reads `result.NextPageToken` from page N to drive page N+1.
	// The handler emits `nextPageToken=tok-%d` based on handlerCalls+1, so:
	//   request 1 in=""  -> out=tok-2; request 2 in=tok-2 -> out=tok-3;
	//   request 3 in=tok-3 -> out=tok-4; request 4 in=tok-4 -> out=tok-5.
	// After 4 the safety cap fires and the loop stops. The 4th request's
	// incoming pageToken is therefore "tok-4" -- NOT "tok-5" (which would
	// be the would-have-been 5th request's incoming, but the cap prevents it).
	// Mirrors the sibling PaginationAcrossThreePages test (lastPageToken
	// == "page3t" after 3 requests where request 3's incoming is "page3t").
	if lastPageToken != "tok-4" {
		t.Errorf("4th request pageToken: want tok-4 (cap short-circuit at request 4); got %q", lastPageToken)
	}
}

// TestYouTubeValidateChannelBinding_ExhaustedMismatch_ReturnsMismatch
// exercises the "ALREADY exhausted pages, expected ID missing" branch
// with the page count UNDER the safety cap (110 < 200). Same shape
// as the existing 3-page happy-path test, but with the expected ID
// deliberately absent, so the loop walks all 3 pages then returns
// ErrYouTubeChannelMismatch wrapping the full id list WITHOUT the
// "safety cap reached" mention (which would indicate the loop
// short-circuited prematurely).
func TestYouTubeValidateChannelBinding_ExhaustedMismatch_ReturnsMismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("pageToken")
		var items []map[string]string
		var next string
		switch tok {
		case "":
			for i := 0; i < 50; i++ {
				items = append(items, map[string]string{"id": fmt.Sprintf("UC-exh-p1-%d", i)})
			}
			next = "tok-2"
		case "tok-2":
			for i := 0; i < 50; i++ {
				items = append(items, map[string]string{"id": fmt.Sprintf("UC-exh-p2-%d", i)})
			}
			next = "tok-3"
		case "tok-3":
			for i := 0; i < 10; i++ {
				items = append(items, map[string]string{"id": fmt.Sprintf("UC-exh-p3-%d", i)})
			}
			next = "" // FINAL page (no more tokens)
		default:
			http.Error(w, `{"error":"unexpected pageToken"}`, http.StatusBadRequest)
			return
		}
		payload := map[string]any{"items": items, "nextPageToken": next}
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(payload)
		w.Write(data)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	s := newTestYouTubeService(ts)
	err := s.ValidateChannelBinding(context.Background(), "fake-access-token", "UC-expected-but-never-present")
	if err == nil {
		t.Fatal("want ErrYouTubeChannelMismatch on exhausted mismatch, got nil")
	}
	if !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("want ErrYouTubeChannelMismatch wrapped; got: %v", err)
	}
	// Explicitly NOT the cap path: we got here by walking pages to
	// exhaustion, NOT by hitting the safety cap. The typed struct
	// is the source-of-truth distinction; if ValidateChannelBinding
	// accidentally routes the exhaustion path through the safety-
	// cap return, errors.As(err, &safetyCap) will succeed here and
	// this assertion surfaces the regression as a typed mismatch
	// rather than brittle message-substring coupling.
	var safetyCap *ErrChannelListSafetyCap
	if errors.As(err, &safetyCap) {
		t.Errorf("want EXHAUSTION path (110 < 200), got *ErrChannelListSafetyCap (cap path); err=%v", err)
	}
	// The full id list lives in the message so the operator can
	// diagnose channel drift in one log line.
	for _, prefix := range []string{"UC-exh-p1-0", "UC-exh-p2-0", "UC-exh-p3-0"} {
		if !strings.Contains(err.Error(), prefix) {
			t.Errorf("error must contain at least one id from each page (e.g. %q) for operator diagnostics; got: %v", prefix, err)
		}
	}
}

// TestYouTubeValidateChannelBinding_FailureMidPagination verifies the
// transient contract is preserved across pagination boundaries: page 1
// succeeds with a non-matching set + nextPageToken, page 2 returns a
// transient 5xx. The function MUST return a plain wrapped error NOT
// wrapping ErrYouTubeChannelMismatch so the worker treats it as
// transient and does NOT flip the platform_account to reauth_required.
// The page-1 5xx case is already covered by
// TestYouTubeValidateChannelBinding_Transient5xx; this test covers the
// post-pagination failure path that did not exist before the
// pagination rewrite — a regression there would silently expand the
// failure surface into reauth territory.
//
// Also asserts handler.calls == 2 so a future off-by-one that issues
// a page-3 GET after page-2's 503 would be caught.
func TestYouTubeValidateChannelBinding_FailureMidPagination(t *testing.T) {
	var handlerCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		switch r.URL.Query().Get("pageToken") {
		case "": // page 1 — succeeds with 50 non-matching channels
			items := make([]youtubeChannel, 50)
			for i := range items {
				items[i] = youtubeChannel{ID: fmt.Sprintf("UCnomatch-%03d", i)}
			}
			_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{
				Items:         items,
				NextPageToken: "page2t",
			})
		default: // page 2 — transient 5xx
			http.Error(w, "google internal error", http.StatusServiceUnavailable)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID")
	if err == nil {
		t.Fatalf("expected error on mid-loop 5xx, got nil")
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("mid-loop 5xx MUST NOT wrap ErrYouTubeChannelMismatch (worker would flag reauth on transient); got %v", err)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected error message to include status code 503, got %q", err.Error())
	}
	if handlerCalls != 2 {
		t.Errorf("expected exactly 2 server hits (page1 + aborted page2), got %d", handlerCalls)
	}
}

// TestYouTubeValidateChannelBinding_EmptyExpectedChannelID verifies
// the empty-input guard: an empty expectedChannelID returns an error
// without any HTTP round-trip.
func TestYouTubeValidateChannelBinding_EmptyExpectedChannelID(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	if err := svc.ValidateChannelBinding(context.Background(), "token", ""); err == nil {
		t.Fatal("expected error on empty expectedChannelID, got nil")
	}
}

// ---------------------------------------------------------------------------
// BindGrantToChannel — provider-level tests for the P0 1-grant-1-channel rule
// (Taglio P0#1 — see github issue / repo discussion for context).
//
// What this proves at the provider layer: a YouTubeOAuthService wired
// with a fake channels.list endpoint enforces the same rule the HTTP
// handler already enforces end-to-end (see pkg/api/routes_test.go's
// TestHandleCallback_YouTube_*), so any future consumer of
// BindGrantToChannel (e.g. a per-channel reconnect endpoint, an
// admin re-bind tool) inherits the same safety guarantees without
// re-implementing the filter.
//
// The integration tests in pkg/api/routes_test.go already count the
// vault.Save calls; these unit tests count the *DiscoveredAccount
// returned and the sentinel surfaced. Together they prove the rule
// end-to-end at both layers.
// ---------------------------------------------------------------------------

// TestYouTubeBindGrantToChannel_OneChannel_NoExpected returns the
// single channel without any sentinel (canonical happy path for the
// most common operator: one Google account, one channel).
func TestYouTubeBindGrantToChannel_OneChannel_NoExpected(t *testing.T) {
	const channelID = "UCaaaaaaaaaaaaaaaaaaaaa1"
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mine") != "true" {
			t.Errorf("DiscoverAccounts must call channels.list with mine=true, got query=%q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []map[string]interface{}{
				{"id": channelID, "snippet": map[string]string{"title": "Solo Channel"}},
			},
			"pageInfo": map[string]int{"totalResults": 1, "resultsPerPage": 1},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "")
	if err != nil {
		t.Fatalf("BindGrantToChannel: %v", err)
	}
	if acc == nil {
		t.Fatal("expected a single *DiscoveredAccount, got nil")
	}
	if acc.Profile.PlatformUserID != channelID {
		t.Errorf("channelID: want %q, got %q", channelID, acc.Profile.PlatformUserID)
	}
}

// TestYouTubeBindGrantToChannel_MultipleChannels_NoExpected_Ambiguous
// returns ErrYouTubeAmbiguousAuthorization — the bearer token must
// NEVER be cloned across N>1 channels. The error message must
// surface the observed channel count so the operator's runbook can
// say "your Google account owns X channels, please re-authorize with
// expected_channel_id".
func TestYouTubeBindGrantToChannel_MultipleChannels_NoExpected_Ambiguous(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []map[string]interface{}{
				{"id": "UCaaaaaaaaaaaaaaaaaaaaa1", "snippet": map[string]string{"title": "Channel A"}},
				{"id": "UCaaaaaaaaaaaaaaaaaaaaa2", "snippet": map[string]string{"title": "Channel B"}},
			},
			"pageInfo": map[string]int{"totalResults": 2, "resultsPerPage": 2},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "")
	if err == nil {
		t.Fatalf("expected ErrYouTubeAmbiguousAuthorization, got nil and account=%+v", acc)
	}
	if acc != nil {
		t.Errorf("ambiguous: account must be nil, got %+v", acc)
	}
	if !errors.Is(err, ErrYouTubeAmbiguousAuthorization) {
		t.Errorf("errors.Is(err, ErrYouTubeAmbiguousAuthorization) = false; err=%v", err)
	}
	if !strings.Contains(err.Error(), "got 2 channels") {
		t.Errorf("error should report the observed channel count, got %q", err.Error())
	}
}

// TestYouTubeBindGrantToChannel_MultipleChannels_ExpectedMatches
// returns the matching channel from a multi-channel grant (canonical
// expected_channel_id happy path).
func TestYouTubeBindGrantToChannel_MultipleChannels_ExpectedMatches(t *testing.T) {
	const expectedID = "UCaaaaaaaaaaaaaaaaaaaaa2"
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []map[string]interface{}{
				{"id": "UCaaaaaaaaaaaaaaaaaaaaa1", "snippet": map[string]string{"title": "Channel A"}},
				{"id": expectedID, "snippet": map[string]string{"title": "Channel B"}},
				{"id": "UCaaaaaaaaaaaaaaaaaaaaa3", "snippet": map[string]string{"title": "Channel C"}},
			},
			"pageInfo": map[string]int{"totalResults": 3, "resultsPerPage": 3},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", expectedID)
	if err != nil {
		t.Fatalf("BindGrantToChannel: %v", err)
	}
	if acc == nil {
		t.Fatal("expected the matching channel, got nil")
	}
	if acc.Profile.PlatformUserID != expectedID {
		t.Errorf("channelID: want %q, got %q", expectedID, acc.Profile.PlatformUserID)
	}
}

// TestYouTubeBindGrantToChannel_OneChannel_ExpectedNoMatch_Mismatch
// returns a wrapped ErrYouTubeChannelMismatch — the operator
// authenticated the wrong Google account (or imported a Brand
// Account id that no longer exists).
func TestYouTubeBindGrantToChannel_OneChannel_ExpectedNoMatch_Mismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []map[string]interface{}{
				{"id": "UCaaaaaaaaaaaaaaaaaaaaa1", "snippet": map[string]string{"title": "Channel A"}},
			},
			"pageInfo": map[string]int{"totalResults": 1, "resultsPerPage": 1},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "UCaaaaaaaaaaaaaaaaaaaaaZ")
	if err == nil {
		t.Fatalf("expected ErrYouTubeChannelMismatch, got nil and account=%+v", acc)
	}
	if acc != nil {
		t.Errorf("mismatch: account must be nil, got %+v", acc)
	}
	if !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("errors.Is(err, ErrYouTubeChannelMismatch) = false; err=%v", err)
	}
	if !strings.Contains(err.Error(), "UCaaaaaaaaaaaaaaaaaaaaaZ") {
		t.Errorf("error should quote the expected channel id, got %q", err.Error())
	}
}

// TestYouTubeBindGrantToChannel_NoChannels_PreservesZeroChannelError
// preserves the existing 0-channel behaviour: DiscoverAccounts
// returns a typed error (the grant is bound to zero channels),
// BindGrantToChannel wraps but does NOT reclassify it as
// ErrYouTubeAmbiguousAuthorization. A zero-channel grant is a
// structurally distinct class of failure (likely a revoked Brand
// Account, not an ambiguous multi-channel one) and must not be
// misrouted by the caller — the handler would otherwise map it to
// 409 Conflict with the wrong operator runbook.
func TestYouTubeBindGrantToChannel_NoChannels_PreservesZeroChannelError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Mimics the Google-side response when the OAuth grant is
		// bound to zero channels (revoked Brand Account, etc.).
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items":    []map[string]interface{}{},
			"pageInfo": map[string]int{"totalResults": 0, "resultsPerPage": 0},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "")
	if err == nil {
		t.Fatalf("expected error on 0 channels, got nil and account=%+v", acc)
	}
	if acc != nil {
		t.Errorf("0 channels: account must be nil, got %+v", acc)
	}
	if errors.Is(err, ErrYouTubeAmbiguousAuthorization) {
		t.Errorf("0 channels must not be misclassified as ErrYouTubeAmbiguousAuthorization (would misroute to reauth path): %v", err)
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("0 channels must not be misclassified as ErrYouTubeChannelMismatch either: %v", err)
	}
	if !strings.Contains(err.Error(), "no YouTube channel") {
		t.Errorf("error should preserve the upstream 'no YouTube channel' message for operator diagnostics, got %q", err.Error())
	}
}

// TestYouTubeBindGrantToChannel_ExpectedAndZeroChannels_PreservesUpstreamError
// covers the structural edge case: the operator supplied an
// expected_channel_id at /auth/youtube/login but the OAuth grant
// bound to ZERO channels (Brand Account revoked, account
// re-rotated, etc.). BindGrantToChannel must NOT wrap this as
// ErrYouTubeChannelMismatch — a 0-channel grant and a non-matching
// grant are structurally distinct failures with different operator
// runbooks ("re-authorize" vs "the channel id is wrong"), and the
// upstream error already communicates the right diagnostic.
func TestYouTubeBindGrantToChannel_ExpectedAndZeroChannels_PreservesUpstreamError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items":    []map[string]interface{}{},
			"pageInfo": map[string]int{"totalResults": 0, "resultsPerPage": 0},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "UCaaaaaaaaaaaaaaaaaaaaaX")
	if err == nil {
		t.Fatalf("expected error on 0 channels with expected set, got nil and account=%+v", acc)
	}
	if acc != nil {
		t.Errorf("0 channels: account must be nil, got %+v", acc)
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("0 channels must NOT be misclassified as ErrYouTubeChannelMismatch — different runbook (re-authorize vs id mismatch): %v", err)
	}
	if errors.Is(err, ErrYouTubeAmbiguousAuthorization) {
		t.Errorf("0 channels must NOT be misclassified as ErrYouTubeAmbiguousAuthorization either: %v", err)
	}
	if !strings.Contains(err.Error(), "no YouTube channel") {
		t.Errorf("error should preserve the upstream 'no YouTube channel' message, got %q", err.Error())
	}
}

// TestYouTubeBindGrantToChannel_Transient5xx_NoSentinelMisclassification
// proves that transient (5xx) errors from channels.list do NOT
// carry either sentinel — the worker / future reconnect callers
// must retry rather than misclassify a 502 as a reauth-required
// state. This is the reverse of the existing TestYouTubeValidateChannelBinding_5xx_NoSentinel
// pattern in this file: it asserts the same property on the
// BindGrantToChannel path.
func TestYouTubeBindGrantToChannel_Transient5xx_NoSentinelMisclassification(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":502,"message":"Upstream Google error"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "")
	if err == nil {
		t.Fatalf("expected error on 5xx, got nil and account=%+v", acc)
	}
	if acc != nil {
		t.Errorf("5xx: account must be nil, got %+v", acc)
	}
	if errors.Is(err, ErrYouTubeAmbiguousAuthorization) {
		t.Errorf("transient 5xx must NOT wrap ErrYouTubeAmbiguousAuthorization (worker would misroute to reauth): %v", err)
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("transient 5xx must NOT wrap ErrYouTubeChannelMismatch (worker would misroute to reauth): %v", err)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error should surface the upstream status for the worker's logged breadcrumb, got %q", err.Error())
	}
}
