package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestUploadBytes_PresignStoragePutComplete(t *testing.T) {
	const (
		apiKey   = "sk_test_upload"
		assetID  = "asset_test_123"
		filename = "video.mp4"
		mimeType = "video/mp4"
	)
	payload := []byte("video bytes used by the upload test")
	wantSHA := sha256Hex(payload)

	var storageMu sync.Mutex
	var storageHeaders http.Header
	var storageBody []byte
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("storage method = %s, want PUT", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("storage authorization = %q, want empty", got)
		}
		if got := r.Header.Get("Content-Type"); got != mimeType {
			t.Errorf("storage content-type = %q, want %q", got, mimeType)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read storage body: %v", err)
		}
		storageMu.Lock()
		storageHeaders = r.Header.Clone()
		storageBody = body
		storageMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer storage.Close()

	var presignBody presignRequest
	var completeCalled bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Errorf("api authorization = %q, want Bearer %s", got, apiKey)
		}
		if r.URL.Path == "/api/v1/media/presign" {
			if r.Method != http.MethodPost {
				t.Errorf("presign method = %s, want POST", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&presignBody); err != nil {
				t.Fatalf("decode presign body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(presignResponse{
				AssetID:       assetID,
				UploadURL:     storage.URL + "/upload",
				UploadMethod:  http.MethodPut,
				UploadHeaders: map[string]string{"Content-Type": mimeType},
			})
			return
		}
		if r.URL.Path == "/api/v1/media/"+assetID+"/complete" {
			if r.Method != http.MethodPost {
				t.Errorf("complete method = %s, want POST", r.Method)
			}
			completeCalled = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mediaAsset{
				ID:          assetID,
				Status:      "ready",
				ContentType: mimeType,
				SizeBytes:   int64(len(payload)),
				SHA256:      wantSHA,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer api.Close()

	c := &client{baseURL: api.URL, apiKey: apiKey, http: api.Client()}
	gotID, err := uploadBytes(c, filename, mimeType, payload)
	if err != nil {
		t.Fatalf("uploadBytes() error = %v", err)
	}
	if gotID != assetID {
		t.Fatalf("asset id = %q, want %q", gotID, assetID)
	}
	if !completeCalled {
		t.Fatal("complete endpoint was not called")
	}
	if presignBody.Filename != filename {
		t.Errorf("presign filename = %q, want %q", presignBody.Filename, filename)
	}
	if presignBody.ContentType != mimeType {
		t.Errorf("presign content type = %q, want %q", presignBody.ContentType, mimeType)
	}
	if presignBody.SizeBytes != int64(len(payload)) {
		t.Errorf("presign size = %d, want %d", presignBody.SizeBytes, len(payload))
	}
	if presignBody.SHA256 != wantSHA {
		t.Errorf("presign sha256 = %q, want %q", presignBody.SHA256, wantSHA)
	}

	storageMu.Lock()
	defer storageMu.Unlock()
	if !bytes.Equal(storageBody, payload) {
		t.Errorf("storage body = %q, want %q", storageBody, payload)
	}
	if got := storageHeaders.Get("Authorization"); got != "" {
		t.Errorf("captured storage authorization = %q, want empty", got)
	}
}

func TestUploadFile_MapsExtensionAndReadsFile(t *testing.T) {
	t.Parallel()

	payload := []byte("png file contents")
	var gotRequest presignRequest
	var complete bool
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read storage body: %v", err)
		}
		if !bytes.Equal(body, payload) {
			t.Errorf("storage body = %q, want %q", body, payload)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer storage.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/media/presign":
			if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
				t.Fatalf("decode presign body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(presignResponse{
				AssetID:       "asset_png",
				UploadURL:     storage.URL,
				UploadHeaders: map[string]string{"Content-Type": "image/png"},
			})
		case "/api/v1/media/asset_png/complete":
			complete = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mediaAsset{ID: "asset_png", Status: "ready"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	path := filepath.Join(t.TempDir(), "cover.PNG")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	c := &client{baseURL: api.URL, apiKey: "sk_test_upload", http: api.Client()}
	gotID, err := uploadFile(c, path)
	if err != nil {
		t.Fatalf("uploadFile() error = %v", err)
	}
	if gotID != "asset_png" || !complete {
		t.Fatalf("upload result = (%q, complete=%v), want (asset_png, true)", gotID, complete)
	}
	if gotRequest.Filename != "cover.PNG" || gotRequest.ContentType != "image/png" {
		t.Errorf("presign request = %+v, want cover.PNG/image/png", gotRequest)
	}
}
