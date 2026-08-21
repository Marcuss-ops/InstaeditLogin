package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadFile_RejectsUnsupportedMIMEBeforeHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.avi")
	if err := os.WriteFile(path, []byte("not supported"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := uploadFile(nil, path)
	if err == nil || !strings.Contains(err.Error(), "unsupported MIME") {
		t.Fatalf("uploadFile() error = %v, want unsupported MIME error", err)
	}
}

func TestUploadFile_RejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.mp4")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := uploadFile(nil, path)
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("uploadFile() error = %v, want empty-file error", err)
	}
}

func TestUploadFile_RejectsFilesOver200MiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.mp4")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	if err := file.Truncate(maxMediaUploadBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("truncate fixture: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	_, err = uploadFile(nil, path)
	if err == nil || !strings.Contains(err.Error(), "maximum is 200 MiB") {
		t.Fatalf("uploadFile() error = %v, want 200 MiB limit error", err)
	}
}

func TestUploadBytes_RejectsInvalidMIMEAndEmptyData(t *testing.T) {
	if _, err := uploadBytes(nil, "video.mp4", "video/avi", []byte("data")); err == nil || !strings.Contains(err.Error(), "unsupported MIME") {
		t.Fatalf("invalid MIME error = %v, want unsupported MIME", err)
	}
	if _, err := uploadBytes(nil, "video.mp4", "video/mp4", nil); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty data error = %v, want empty-file error", err)
	}
}
