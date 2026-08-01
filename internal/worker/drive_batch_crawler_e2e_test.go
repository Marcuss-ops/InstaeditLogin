package worker

// End-to-end crawler test — files.list URL contract. Extracted from
// drive_batch_crawler_test.go (Task 7/10) because it is the only test
// here that wires the REAL *GoogleDriveOAuthService through an
// httptest.Server instead of the recording fakes.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestProcessBatch_FilesListIntegration_CorporaAndDriveIdInQuery —
// the END-TO-END verification of the user's spec line "verifica del
// parametro passato a files.list":
//
//   - The capRouter is wired with a REAL *GoogleDriveOAuthService
//     (not a recordingLister).
//   - The service is hydrated via NewGoogleDriveOAuthService +
//     a redirectingRoundTripper that re-points every URL to an
//     httptest.Server that pretends to be Drive's v3 API.
//   - The httptest.Server captures every request's URL + Authorization
//     header + replies with the share-drive JSON (folder metadata) →
//     2 pages of files.list (1 video on each page, then
//     nextPageToken="").
//
// After processBatch returns, the captured URLs are the ACTUAL
// files.list URLs the service sent. We assert:
//
//  1. files.get was called once for the folder (Task 6/10 resolve).
//  2. files.list was called twice (per-page iteration).
//  3. BOTH files.list URLs contain corpora=drive AND driveId=<resolved>
//     AND pageSize=200 (the underlying contracts the user spec is
//     locking down).
//  4. BOTH files.list requests carried the Authorization Bearer header
//     with the vault-supplied token.
//  5. The folder metadata response was honored end-to-end (the
//     driveId resolved from that response is what flows into the URL).
//
// This is the highest-fidelity test in this file — it doesn't fake
// the real lister's URL-building path, so a future regression that
// drops driveId plumbing inside ListFolder would surface here as a
// missing parameter in the recorded URL.
func TestProcessBatch_FilesListIntegration_CorporaAndDriveIdInQuery(t *testing.T) {
	const (
		folderID      = "shared-folder-e2e"
		sharedDriveID = "0ABCshared-e2e-folder"
		accessToken   = "ya29.crawler-e2e-fake-token"
	)

	var filesListURLs []string
	var filesListAuthHeaders []string
	var folderGetURL string
	var folderGetAuthHeader string
	var folderGetCalls int
	var urlMu sync.Mutex

	driveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		// files.get for the folder — fired by GetFileMetadata during resolve.
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/"+folderID):
			urlMu.Lock()
			folderGetURL = req.URL.String()
			folderGetAuthHeader = req.Header.Get("Authorization")
			folderGetCalls++
			urlMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{
				"id": %q,
				"name": "shared",
				"mimeType": "application/vnd.google-apps.folder",
				"driveId": %q
			}`, folderID, sharedDriveID)
		// files.list endpoint — fired by ListFolder on every page.
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/drive/v3/files"):
			urlMu.Lock()
			filesListURLs = append(filesListURLs, req.URL.String())
			filesListAuthHeaders = append(filesListAuthHeaders, req.Header.Get("Authorization"))
			urlMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if req.URL.Query().Get("pageToken") == "" {
				_, _ = w.Write([]byte(`{
					"files": [{"id": "v1", "name": "v1.mp4", "mimeType": "video/mp4"}],
					"nextPageToken": "p2"
				}`))
			} else {
				_, _ = w.Write([]byte(`{
					"files": [{"id": "v2", "name": "v2.mp4", "mimeType": "video/mp4"}],
					"nextPageToken": ""
				}`))
			}
		default:
			t.Logf("unexpected request: %s %s", req.Method, req.URL.String())
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer driveSrv.Close()

	driveSrvURL, err := url.Parse(driveSrv.URL)
	if err != nil {
		t.Fatalf("parse driveSrv URL: %v", err)
	}

	// Real *services.GoogleDriveOAuthService with a redirecting
	// RoundTripper that re-points every URL to the httptest.Server.
	// We construct via the public constructor + ProviderDependencies,
	// so this test compiles despite living in package worker (the
	// service's internal fields stay unexported on our side).
	realSvc, err := services.NewGoogleDriveOAuthService(
		&config.Config{
			Auth: config.AuthConfig{
				GoogleDriveClientID:     "test-client",
				GoogleDriveClientSecret: "test-secret",
			},
		},
		services.ProviderDependencies{
			HTTPClient: &http.Client{
				Transport: &driveBatchE2ERoundTripper{target: driveSrvURL},
			},
		},
	)
	if err != nil || realSvc == nil {
		t.Fatalf("NewGoogleDriveOAuthService returned nil: err=%v", err)
	}

	// Verify the service satisfies both interfaces the crawler needs.
	if _, ok := any(realSvc).(services.DriveFolderLister); !ok {
		t.Fatal("realSvc does not implement DriveFolderLister")
	}
	if _, ok := any(realSvc).(services.DriveFolderInspector); !ok {
		t.Fatal("realSvc does not implement DriveFolderInspector")
	}

	batchRepo := newFakeBatchStore()
	uploadRepo := &fakeUploadRepo{}
	vault := newFakeVault(accessToken)

	router := services.NewCapabilityRouter()
	router.Register("google_drive", realSvc)

	batch := makeBatch(t)
	batch.ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	batch.SourceFolderID = folderID
	batchRepo.seedBatch(batch)

	c := newCrawlerForSharedDriveTests(batchRepo, uploadRepo, vault, router)
	c.processBatch(context.Background(), batch, testWorkerID)

	urlMu.Lock()
	defer urlMu.Unlock()

	// files.get fired exactly once for the folder.
	if folderGetCalls != 1 {
		t.Errorf("folder files.get call count: want 1 (resolver fires once), got %d", folderGetCalls)
	}
	if folderGetURL == "" {
		t.Fatalf("folder files.get URL: want non-empty (resolver must call GetFileMetadata once), got empty")
	}
	if !strings.Contains(folderGetURL, "supportsAllDrives=true") {
		t.Errorf("folder files.get URL: want supportsAllDrives=true, got %q", folderGetURL)
	}
	if folderGetAuthHeader != "Bearer "+accessToken {
		t.Errorf("folder files.get Authorization header: want %q, got %q", "Bearer "+accessToken, folderGetAuthHeader)
	}

	// files.list fired TWICE (per-page iteration), with the resolved
	// driveId threaded in BOTH calls — the user's spec line expressed
	// as a URL assertion.
	if len(filesListURLs) != 2 {
		t.Fatalf("files.list URL captures: want 2 (pages), got %d: %v", len(filesListURLs), filesListURLs)
	}
	for i, rawURL := range filesListURLs {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			t.Errorf("page %d URL parse: %v (raw=%s)", i, err, rawURL)
			continue
		}
		q := parsed.Query()
		// 1. supportsAllDrives + includeItemsFromAllDrives are the
		//    base Shared-Drive compatibility flags (always-on).
		if q.Get("supportsAllDrives") != "true" {
			t.Errorf("page %d: supportsAllDrives: want true, got %q", i, q.Get("supportsAllDrives"))
		}
		if q.Get("includeItemsFromAllDrives") != "true" {
			t.Errorf("page %d: includeItemsFromAllDrives: want true, got %q", i, q.Get("includeItemsFromAllDrives"))
		}
		// 2. corpora=drive is the corpus-scoped LIST contract — the
		//    user's "corpora=Shared Drive" requirement made literal.
		if q.Get("corpora") != "drive" {
			t.Errorf("page %d: corpora: want \"drive\", got %q (this means the resolved driveId didn't flow into ListFolder)",
				i, q.Get("corpora"))
		}
		// 3. driveId=<resolved> is the Shared Drive scoping.
		if q.Get("driveId") != sharedDriveID {
			t.Errorf("page %d: driveId: want %q (resolved from folder metadata), got %q",
				i, sharedDriveID, q.Get("driveId"))
		}
		// 4. pageSize is the production page-cap invariant (200).
		if q.Get("pageSize") != "200" {
			t.Errorf("page %d: pageSize: want \"200\" (production page cap), got %q", i, q.Get("pageSize"))
		}
		// 5. access_token (the vault-supplied bearer) is present in
		//    the URL query (production also adds Authorization header
		//    — see assertion 6).
		if q.Get("access_token") != accessToken {
			t.Errorf("page %d: access_token: want %q, got %q", i, accessToken, q.Get("access_token"))
		}
		// 6. Authorization Bearer header carries the same token —
		//    locks the dual-channel (URL + header) contract.
		if filesListAuthHeaders[i] != "Bearer "+accessToken {
			t.Errorf("page %d: Authorization header: want %q, got %q",
				i, "Bearer "+accessToken, filesListAuthHeaders[i])
		}
		// 7. pageToken flows correctly across pages.
		if i == 0 && q.Get("pageToken") != "" {
			t.Errorf("page 0: pageToken: want empty (initial), got %q", q.Get("pageToken"))
		}
		if i == 1 && q.Get("pageToken") != "p2" {
			t.Errorf("page 1: pageToken: want \"p2\" (loop-carried), got %q", q.Get("pageToken"))
		}
	}

	// Successful terminal transition.
	if batchRepo.markCompletedCalls != 1 {
		t.Errorf("MarkCompleted: want 1, got %d", batchRepo.markCompletedCalls)
	}
	if len(batchRepo.markFailedCalls) != 0 {
		t.Errorf("MarkFailed: want 0 (success), got: %v", batchRepo.markFailedCalls)
	}
	if got := len(uploadRepo.created); got != 2 {
		t.Errorf("upload_jobs created: want 2 (1 file per page), got %d", got)
	}
}

// driveBatchE2ERoundTripper is the worker's test-side replacement for
// the services package's `redirectingRoundTripper` (which lives in
// internal/services/google_drive_oauth_resolve_test.go and isn't
// exported). Rewrites scheme + host to the test server while keeping
// path + query intact so production code's URL-building runs
// unchanged.
type driveBatchE2ERoundTripper struct {
	target *url.URL
}

func (r *driveBatchE2ERoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}
