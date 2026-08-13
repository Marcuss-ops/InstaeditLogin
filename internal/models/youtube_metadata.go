package models

// YouTubeMetadataPatch is a PARTIAL snippet update for an existing
// YouTube video. Pointer semantics: nil = leave the field untouched;
// non-nil = apply the value (including an empty string to deliberately
// clear a field). This mirrors how the JSON PATCH body distinguishes
// "omitted" from "explicitly cleared".
type YouTubeMetadataPatch struct {
	Title         *string
	Description   *string
	CategoryID    *string
	// PrivacyStatus is the optional visibility change ("public" |
	// "private" | "unlisted"). nil = leave the current status
	// untouched; non-nil = fold a status.privacyStatus write into the
	// same videos.update call. It is NOT pointer-cleared like the
	// snippet fields: visibility is always one of the three legal
	// values, never "empty".
	PrivacyStatus *string
}

// YouTubeMetadataResult is the MERGED projection returned after a
// successful videos.update. It carries the effective snippet values
// (current + patch) plus the privacy status actually applied (empty
// string when visibility was not changed), so the caller can echo
// them without a follow-up read.
type YouTubeMetadataResult struct {
	VideoID       string
	Title         string
	Description   string
	CategoryID    string
	PrivacyStatus string
}
