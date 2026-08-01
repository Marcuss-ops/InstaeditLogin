package worker

// VideoMimePrefixes is the conservative allowlist of Google Drive
// mime prefixes the crawler treats as video-shaped. Non-video items
// (docs, sheets, images) are skipped so a folder containing a
// mixed-content tree doesn't enqueue broken upload_jobs.
//
// Conservative on purpose: a misclassified file at this stage
// silently fails at Drive download time anyway, but we'd rather
// skip in advance to keep the upload_job queue clean.
var VideoMimePrefixes = []string{
	"video/",
}

// IsVideoMime returns true if mimeType is in the VideoMimePrefixes
// allowlist. Empty strings and unknown prefixes are rejected.
func IsVideoMime(mimeType string) bool {
	for _, prefix := range VideoMimePrefixes {
		if len(mimeType) >= len(prefix) && mimeType[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
