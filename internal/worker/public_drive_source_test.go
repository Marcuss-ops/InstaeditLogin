package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestPublicDriveSource_Name — registry-key contract: must return
// UploadJobSourcePublicDrive or legacy rows misroute.
func TestPublicDriveSource_Name(t *testing.T) {
	s := NewPublicDriveSource()
	if got := s.Name(); got != models.UploadJobSourcePublicDrive {
		t.Fatalf("Name() = %q; want %q", got, models.UploadJobSourcePublicDrive)
	}
}

// TestPublicDriveSource_Open_DeprecationMessage — the deprecation
// error message is operator-actionable; the test pins a few
// substrings operators would search log lines for, so a future copy
// tweak that drops them gets caught at PR time.
func TestPublicDriveSource_Open_DeprecationMessage(t *testing.T) {
	s := NewPublicDriveSource()
	_, err := s.Open(context.Background(), &models.UploadJob{ID: 1})
	if err == nil {
		t.Fatal("Open should return deprecation error")
	}
	for _, want := range []string{
		"public_drive",
		"removed",
		"authenticated Drive",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err.Error(), want)
		}
	}
}

// TestPublicDriveSource_Inspect_DeprecationMessage — same
// deprecation message as Open so the operator sees the same
// guidance regardless of which entry point surfaced the rejection.
func TestPublicDriveSource_Inspect_DeprecationMessage(t *testing.T) {
	s := NewPublicDriveSource()
	_, err := s.Inspect(context.Background(), &models.UploadJob{ID: 1})
	if err == nil {
		t.Fatal("Inspect should return deprecation error")
	}
	if !strings.Contains(err.Error(), "removed") {
		t.Fatalf("Inspect error %q should contain 'removed'", err.Error())
	}
}
