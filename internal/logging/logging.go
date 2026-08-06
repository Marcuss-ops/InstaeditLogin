// Package logging provides the process-wide structured logging policy.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
)

const (
	redactedValue = "[REDACTED]"
	redactedURL   = "[REDACTED_URL]"
	maxSampleKeys = 1024
)

// Options controls the shared logging policy.
type Options struct {
	Level       slog.Level
	SampleEvery uint64
	ReplaceAttr func(groups []string, attr slog.Attr) slog.Attr
}

// Handler wraps a standard slog handler with secret redaction and bounded
// sampling. Redaction is applied before the underlying handler sees records.
type Handler struct {
	next   slog.Handler
	sample *sampler
}

type sampler struct {
	mu     sync.Mutex
	counts map[string]uint64
	every  uint64
}

// NewHandler returns a structured handler with the repository privacy policy.
// SampleEvery applies only to Debug and Info records; warnings and errors are
// never sampled. The first low-severity record for each message is retained.
func NewHandler(w io.Writer, opts Options) slog.Handler {
	every := opts.SampleEvery
	if every == 0 {
		every = 10
	}
	return &Handler{
		next: slog.NewTextHandler(w, &slog.HandlerOptions{
			Level:       opts.Level,
			ReplaceAttr: opts.ReplaceAttr,
		}),
		sample: &sampler{counts: make(map[string]uint64), every: every},
	}
}

// NewLogger constructs a logger using the shared handler policy.
func NewLogger(w io.Writer, opts Options) *slog.Logger {
	return slog.New(NewHandler(w, opts))
}

// RedactingWriter protects legacy loggers (log.Printf/fmt.Fprintln) that
// cannot use slog.Handler directly. It removes URLs and known credential
// forms from the complete line before writing it.
type RedactingWriter struct{ w io.Writer }

func NewRedactingWriter(w io.Writer) io.Writer { return RedactingWriter{w: w} }

func (w RedactingWriter) Write(p []byte) (int, error) {
	clean := redactText(string(p))
	_, err := io.WriteString(w.w, clean)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	if !h.shouldKeep(record.Level, record.Message) {
		return nil
	}

	clean := slog.NewRecord(record.Time, record.Level, redactText(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		clean.AddAttrs(redactAttr(attr))
		return true
	})
	return h.next.Handle(ctx, clean)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		clean = append(clean, redactAttr(attr))
	}
	return &Handler{next: h.next.WithAttrs(clean), sample: h.sample}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name), sample: h.sample}
}

func (s *sampler) shouldKeep(level slog.Level, message string) bool {
	if level >= slog.LevelWarn {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.counts) >= maxSampleKeys {
		// Resetting the bounded window prevents attacker-controlled or
		// high-cardinality messages from growing process memory forever.
		s.counts = make(map[string]uint64, maxSampleKeys/2)
	}
	s.counts[message]++
	count := s.counts[message]
	return count == 1 || count%s.every == 0
}

func (h *Handler) shouldKeep(level slog.Level, message string) bool {
	return h.sample.shouldKeep(level, message)
}

func redactAttr(attr slog.Attr) slog.Attr {
	if attr.Equal(slog.Attr{}) {
		return attr
	}
	key := attr.Key
	if isSensitiveKey(key) {
		return slog.String(key, redactForKey(key))
	}
	return slog.Attr{Key: key, Value: redactValue(attr.Value)}
}

func redactForKey(key string) string {
	normalized := strings.ToLower(key)
	if strings.Contains(normalized, "url") || strings.Contains(normalized, "uri") {
		return redactedURL
	}
	return redactedValue
}

func redactValue(value slog.Value) slog.Value {
	value = value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		return slog.StringValue(redactText(value.String()))
	case slog.KindGroup:
		attrs := value.Group()
		clean := make([]slog.Attr, 0, len(attrs))
		for _, attr := range attrs {
			clean = append(clean, redactAttr(attr))
		}
		return slog.GroupValue(clean...)
	case slog.KindAny:
		return redactAny(value.Any())
	default:
		return value
	}
}

func redactAny(value any) slog.Value {
	if value == nil {
		return slog.AnyValue(nil)
	}
	if err, ok := value.(error); ok {
		return slog.StringValue("error_type:" + reflect.TypeOf(err).String())
	}
	if _, ok := value.(http.Header); ok {
		return slog.StringValue(redactedValue)
	}
	if _, ok := value.(http.Request); ok {
		return slog.StringValue(redactedValue)
	}
	if _, ok := value.(*http.Request); ok {
		return slog.StringValue(redactedValue)
	}
	if u, ok := value.(url.URL); ok {
		return slog.StringValue(redactText(u.String()))
	}
	if u, ok := value.(*url.URL); ok && u != nil {
		return slog.StringValue(redactText(u.String()))
	}
	// Opaque values are stringified only after the same URL/credential
	// redaction pass, preventing a map or struct from bypassing policy.
	return slog.StringValue(redactText(fmt.Sprint(value)))
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(key))
	for _, fragment := range []string{
		"access_token", "refresh_token", "authorization", "cookie", "set_cookie",
		"stream_key", "streamkey", "signed_url", "signedurl", "presigned",
		"signature", "password", "secret", "api_key", "apikey", "private_key",
		"headers", "header", "body", "response", "error", "err",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return normalized == "token" || strings.HasSuffix(normalized, "_token") ||
		strings.Contains(normalized, "url") || strings.Contains(normalized, "uri")
}

var (
	bearerPattern           = regexp.MustCompile(`(?i)\bBearer[[:space:]]+[^[:space:]]+`)
	secretAssignmentPattern = regexp.MustCompile(`(?i)(access[_-]?token|refresh[_-]?token|stream[_-]?key|signature|signed[_-]?url|password|secret|cookie|authorization|session|csrf[_-]?token|x-amz-security-token)[[:space:]]*[:=][[:space:]]*[^[:space:]]+`)
	urlPattern              = regexp.MustCompile(`https?://[^[:space:]"']+`)
)

func redactText(text string) string {
	if text == "" {
		return text
	}
	text = urlPattern.ReplaceAllString(text, redactedURL)
	text = bearerPattern.ReplaceAllString(text, "Bearer "+redactedValue)
	text = secretAssignmentPattern.ReplaceAllString(text, "$1="+redactedValue)
	lower := strings.ToLower(text)
	for _, prefix := range []string{"ya29.", "1//", "akia"} {
		if strings.Contains(lower, prefix) {
			return redactedValue
		}
	}
	return text
}

// RedactTextForTest exposes the exact redaction contract to focused tests in
// this package without making the production handler depend on test helpers.
func RedactTextForTest(text string) string { return redactText(text) }

var _ slog.Handler = (*Handler)(nil)
