package bootstrap

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

// fakeSentryTransport records every Sentry event it receives so tests can
// assert that a panic really travelled through the SDK without calling a
// real Sentry endpoint.
type fakeSentryTransport struct {
	events chan *sentry.Event
}

func newFakeSentryTransport() *fakeSentryTransport {
	return &fakeSentryTransport{events: make(chan *sentry.Event, 10)}
}

func (t *fakeSentryTransport) Configure(_ sentry.ClientOptions) {}
func (t *fakeSentryTransport) Flush(_ time.Duration) bool       { return true }
func (t *fakeSentryTransport) SendEvent(event *sentry.Event) {
	t.events <- event
}

func (t *fakeSentryTransport) waitEvent(timeout time.Duration) (*sentry.Event, bool) {
	select {
	case event := <-t.events:
		return event, true
	case <-time.After(timeout):
		return nil, false
	}
}

// TestConfigureSentry proves that the bootstrap Sentry wiring helper
// behaves correctly for the three configuration states the operator can
// give us: disabled (empty DSN), malformed (init error), and enabled
// with a fake transport. The valid-DSN sub-test is intentionally run
// last because sentry.Init can only succeed once per process; the
// empty/malformed cases do not initialise the SDK.
func TestConfigureSentry(t *testing.T) {
	t.Run("empty DSN disables Sentry", func(t *testing.T) {
		hub, err := configureSentry(sentry.ClientOptions{Dsn: ""})
		if err != nil {
			t.Fatalf("configureSentry with empty DSN: want nil error, got %v", err)
		}
		if hub != nil {
			t.Fatalf("configureSentry with empty DSN: want nil hub, got %v", hub)
		}
	})

	t.Run("malformed DSN returns error without hub", func(t *testing.T) {
		hub, err := configureSentry(sentry.ClientOptions{Dsn: "://malformed"})
		if err == nil {
			t.Fatal("configureSentry with malformed DSN: want error, got nil")
		}
		if hub != nil {
			t.Fatalf("configureSentry with malformed DSN: want nil hub, got %v", hub)
		}
	})

	t.Run("valid DSN wires hub and captures panic", func(t *testing.T) {
		transport := newFakeSentryTransport()
		hub, err := configureSentry(sentry.ClientOptions{
			Dsn:       "https://public@example.com/1",
			Transport: transport,
		})
		if err != nil {
			t.Fatalf("configureSentry with valid DSN: want nil error, got %v", err)
		}
		if hub == nil {
			t.Fatal("configureSentry with valid DSN: want non-nil hub, got nil")
		}

		hub.Recover(errors.New("controlled bootstrap boom"))

		event, ok := transport.waitEvent(2 * time.Second)
		if !ok {
			t.Fatal("expected a Sentry event to be sent, got none (Sentry wiring may be broken)")
		}
		if event.Exception == nil || len(event.Exception) == 0 {
			t.Fatal("expected exception in Sentry event")
		}
		exc := event.Exception[0]
		if !strings.Contains(exc.Value, "controlled bootstrap boom") {
			t.Errorf("exception value should contain panic message; got %q", exc.Value)
		}
	})
}
