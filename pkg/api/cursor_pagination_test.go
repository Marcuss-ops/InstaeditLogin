package api

import (
	"net/url"
	"testing"
	"time"
)

func TestListCursorRoundTripAndScope(t *testing.T) {
	when := time.Date(2026, 8, 6, 12, 34, 56, 123000000, time.UTC)
	raw := encodeListCursor("media", when, "asset-7")
	gotTime, gotID, err := decodeListCursor(raw, "media")
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if !gotTime.Equal(when) || gotID != "asset-7" {
		t.Fatalf("round trip = (%v, %q), want (%v, asset-7)", gotTime, gotID, when)
	}
	if _, _, err := decodeListCursor(raw, "accounts"); err == nil {
		t.Fatal("cursor from another endpoint must be rejected")
	}
}

func TestListCursorContextAndNullableTime(t *testing.T) {
	when := time.Date(2026, 8, 6, 12, 34, 56, 0, time.UTC)
	context := listCursorFilterContext(url.Values{"account_id": []string{"7"}, "status": []string{"pending"}}, "account_id", "status", "from", "to")
	raw := encodeListCursorForContext("uploads", context, when, "42")
	got, gotID, gotNull, err := decodeListCursorDetails(raw, "uploads", context)
	if err != nil || gotNull || !got.Equal(when) || gotID != "42" {
		t.Fatalf("context cursor = (%v, %q, %v, %v)", got, gotID, gotNull, err)
	}
	if _, _, _, err := decodeListCursorDetails(raw, "uploads", "status=pending"); err == nil {
		t.Fatal("cursor with a different filter context must be rejected")
	}

	nullRaw := encodeListCursorForContext("uploads", context, time.Time{}, "43")
	got, gotID, gotNull, err = decodeListCursorDetails(nullRaw, "uploads", context)
	if err != nil || !got.IsZero() || !gotNull || gotID != "43" {
		t.Fatalf("nullable cursor = (%v, %q, %v, %v)", got, gotID, gotNull, err)
	}
}

func TestParseListPageBounds(t *testing.T) {
	limit, cursor, err := parseListPageWithBounds(url.Values{"limit": []string{"500"}, "cursor": []string{"next"}}, 100, 500)
	if err != nil || limit != 500 || cursor != "next" {
		t.Fatalf("parse page = (%d, %q, %v)", limit, cursor, err)
	}
	if _, _, err := parseListPageWithBounds(url.Values{"limit": []string{"501"}}, 100, 500); err == nil {
		t.Fatal("limit above endpoint maximum must be rejected")
	}
}
