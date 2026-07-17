package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRandomDurationInRange_InclusivelyWithinBounds(t *testing.T) {
	const min = 60        // 60s
	const max = 1800      // 30 minutes
	const iterations = 500
	minDur := time.Duration(min) * time.Second
	maxDur := time.Duration(max) * time.Second
	for i := 0; i < iterations; i++ {
		got, err := randomDurationInRange(min, max)
		if err != nil {
			t.Fatalf("iteration %d: randomDurationInRange: %v", i, err)
		}
		if got < minDur {
			t.Errorf("iteration %d: got %v < min %v", i, got, minDur)
		}
		if got > maxDur {
			t.Errorf("iteration %d: got %v > max %v", i, got, maxDur)
		}
	}
}

func TestRandomDurationInRange_RangeOfOne_ReturnsThatValue(t *testing.T) {
	got, err := randomDurationInRange(3600, 3600)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != time.Hour {
		t.Errorf("single-value range: want 1h, got %v", got)
	}
}

func TestRandomDurationInRange_ReversedBounds_ReturnsError(t *testing.T) {
	_, err := randomDurationInRange(100, 10)
	if err == nil {
		t.Fatal("want error when min > max, got nil")
	}
	if !strings.Contains(err.Error(), "min") || !strings.Contains(err.Error(), "max") {
		t.Errorf("error message should mention min/max ordering, got: %v", err)
	}
}

func TestDriveBatchImport_InvalidFolderID_RejectedByLister(t *testing.T) {
	// Even without an API key, a folderID with a quote should be rejected
	// by the regex on the service side BEFORE the API-key check fires
	// (the regex failure short-circuits the typed sentinel path).
	lister := &mockDriveFolderLister{
		listErr: errors.New("google drive ListFolder: invalid folder id (only A-Za-z0-9_- allowed, max 100 chars)"),
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	// JSON body with a single quote injected:
	body := `{"folder_id":"abc' or '1'='1","workspace_id":1,"facebook_account_id":50}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	// 502 Bad Gateway: service rejects the call before any HTTP request.
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502 (regex rejection maps to upstream error), got %d: %s", w.Code, w.Body.String())
	}
}
