package models

// YouTubeMetadataPatch is a PARTIAL snippet update for an existing
// YouTube video. Pointer semantics: nil = leave the field untouched;
// non-nil = apply the value (including an empty string to deliberately
// clear a field). This mirrors how the JSON PATCH body distinguishes
// "omitted" from "explicitly cleared".
type YouTubeMetadataPatch struct {
	Title       *string
	Description *string
	CategoryID  *string
}

// YouTubeMetadataResult is the MERGED snippet projection returned
// after a successful videos.update(part=snippet). It carries the
// effective values (current + patch), so the caller can echo them
// without a follow-up read.
type YouTubeMetadataResult struct {
	VideoID     string
	Title       string
	Description string
	CategoryID  string
}
