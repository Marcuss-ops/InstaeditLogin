package api

import (
	"testing"
	"time"
)

func TestPrepareAtForPublishUsesLeadWindow(t *testing.T) {
	publishAt := time.Now().Add(30 * time.Minute)
	prepareAt := prepareAtForPublish(publishAt)
	if !prepareAt.Before(publishAt) {
		t.Fatalf("prepare_at %s is not before publish_at %s", prepareAt, publishAt)
	}
	if got := publishAt.Sub(prepareAt); got < 14*time.Minute || got > 16*time.Minute {
		t.Fatalf("preparation window = %s, want approximately 15m", got)
	}
}

func TestPrepareAtForPublishDoesNotScheduleInThePast(t *testing.T) {
	prepareAt := prepareAtForPublish(time.Now().Add(2 * time.Minute))
	if prepareAt.Before(time.Now().Add(-time.Second)) {
		t.Fatalf("prepare_at %s is in the past", prepareAt)
	}
}
