package api

import "testing"

func TestGroupVideosCursorRoundTrip(t *testing.T) {
	const context = "group_id=7&include_subgroups=false&days=90"
	cursor := encodeGroupVideosCursor(context, 42, "video-25")
	accountID, videoID, err := decodeGroupVideosCursor(cursor, context)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if accountID != 42 || videoID != "video-25" {
		t.Fatalf("cursor = (%d, %q), want (42, video-25)", accountID, videoID)
	}
}

func TestGroupVideosCursorRejectsDifferentFilterContext(t *testing.T) {
	cursor := encodeGroupVideosCursor("group_id=7&include_subgroups=false&days=90", 42, "video-25")
	if _, _, err := decodeGroupVideosCursor(cursor, "group_id=7&include_subgroups=true&days=90"); err == nil {
		t.Fatal("cursor from a different filter context must be rejected")
	}
}

func TestGroupVideosCursorRejectsMalformedAndNegative(t *testing.T) {
	if _, _, err := decodeGroupVideosCursor("not-base64", "group_id=7"); err == nil {
		t.Fatal("malformed cursor must be rejected")
	}
	invalid := encodeGroupVideosCursor("group_id=7", 0, "video-1")
	if _, _, err := decodeGroupVideosCursor(invalid, "group_id=7"); err == nil {
		t.Fatal("invalid account cursor must be rejected")
	}
}
