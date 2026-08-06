package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestHandlerRedactsSensitiveAttributesAndText(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, Options{Level: slog.LevelDebug, SampleEvery: 100})

	logger.Info("request completed",
		"access_token", "access-secret-value",
		"cookie", "session=secret-cookie",
		"stream_key", "stream-secret",
		"signed_url", "https://objects.example.test/a?X-Amz-Signature=secret-signature",
		"authorization", "Bearer secret-bearer",
		"safe", "Bearer secret-inline",
	)
	output := buf.String()
	for _, secret := range []string{
		"access-secret-value", "secret-cookie", "stream-secret", "secret-signature", "secret-bearer", "secret-inline",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("log contains secret %q: %s", secret, output)
		}
	}
	if !strings.Contains(output, "access_token=[REDACTED]") || !strings.Contains(output, "safe=\"Bearer [REDACTED]\"") {
		t.Fatalf("expected redaction markers in output: %s", output)
	}
}

func TestHandlerRedactsErrorsWithoutLoggingErrorText(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, Options{Level: slog.LevelDebug, SampleEvery: 100})
	logger.Error("provider failed", "error", errors.New("refresh_token=secret-refresh"))
	if strings.Contains(buf.String(), "secret-refresh") {
		t.Fatalf("error text leaked: %s", buf.String())
	}
}

func TestHandlerSamplesDebugAndInfoButNeverWarningsOrErrors(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, Options{Level: slog.LevelDebug, SampleEvery: 3})
	for i := 0; i < 10; i++ {
		logger.Info("frequent event")
	}
	if got := strings.Count(buf.String(), "frequent event"); got != 4 {
		t.Fatalf("expected first plus every third info record, got %d: %s", got, buf.String())
	}
	for i := 0; i < 10; i++ {
		logger.Warn("important event")
		logger.Error("important error")
	}
	if got := strings.Count(buf.String(), "important event"); got != 10 {
		t.Fatalf("warnings must not be sampled, got %d", got)
	}
	if got := strings.Count(buf.String(), "important error"); got != 10 {
		t.Fatalf("errors must not be sampled, got %d", got)
	}
}

func TestHandlerRedactsNestedGroupsAndHeaders(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, Options{Level: slog.LevelDebug, SampleEvery: 100})
	logger.Info("nested", slog.Group("request", slog.String("authorization", "Bearer nested-secret"), slog.String("path", "/safe")))
	logger.Info("headers", "headers", map[string]string{"Authorization": "Bearer map-secret", "X-Request-ID": "request-1"})
	output := buf.String()
	for _, secret := range []string{"nested-secret", "map-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("nested secret leaked: %s", output)
		}
	}
	if !strings.Contains(output, "path=/safe") {
		t.Fatalf("safe nested value missing: %s", output)
	}
}

func TestRedactingWriterRemovesURLsAndCookies(t *testing.T) {
	var buf bytes.Buffer
	writer := NewRedactingWriter(&buf)
	_, _ = writer.Write([]byte("signed https://objects.example.test/file?X-Amz-Signature=secret Cookie: session=session-secret; csrf_token=csrf-secret\n"))
	output := buf.String()
	for _, secret := range []string{"objects.example.test", "secret", "session-secret", "csrf-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("redacting writer leaked %q: %s", secret, output)
		}
	}
}

func TestRedactTextForTest(t *testing.T) {
	for _, input := range []string{
		"Authorization: Bearer abc123",
		"https://example.test/callback?token=secret-token",
		"signed_url=https://objects.test/a?X-Amz-Signature=secret",
		"refresh_token=secret-refresh",
	} {
		if got := RedactTextForTest(input); got == input || strings.Contains(got, "secret") || strings.Contains(got, "abc123") {
			t.Fatalf("text was not redacted: input=%q output=%q", input, got)
		}
	}
}
