package services

import (
	"crypto/rand"
	"fmt"
	"path"
	"strings"
	"time"
)

// newUUID4 returns an RFC 4122 v4 UUID generated from crypto/rand. On
// the (very unlikely) OS failure of crypto/rand this returns a valid-
// shape UUID with version 4 + variant 10 bits set; we'd prefer not to
// panic since this is on a request hot-path.
func newUUID4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Don't panic — fill with time-based seed so the UUID still has
		// a valid shape. We're trading predictability for availability.
		n := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(n >> (uint(i) * 8))
		}
	}
	// RFC 4122 v4 layout: set version (4) and variant (10) bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// BuildUploadKey assembles the storage key for a user upload.
// Pattern: uploads/{user_id}/{uuid4}_{sanitized-filename}.
//
// The user_id prefix provides tenant isolation. The UUID4 component
// makes the key unguessable so the same filename from the same user
// never collides across uploads.
func BuildUploadKey(userID int64, filename string) string {
	return fmt.Sprintf("uploads/%d/%s_%s",
		userID, newUUID4(), sanitizeFilename(filename))
}

// sanitizeFilename reduces a client-provided filename to a safe token
// suitable for an S3 object key. Steps:
//  1. Strip any path components (path.Base) so ".." never escapes.
//  2. Replace unsafe chars (anything outside [A-Za-z0-9-_.]) with '_'.
//  3. Trim to 200 chars to keep the final key compact.
//  4. Reject empty / "." / ".." by returning "file" — defensive.
func sanitizeFilename(filename string) string {
	base := path.Base(filename)
	var b strings.Builder
	b.Grow(len(base))
	for _, r := range base {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := b.String()
	if s == "" || s == "." || s == ".." {
		return "file"
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
