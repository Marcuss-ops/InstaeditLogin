package bootstrap

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// Minimal stubs for the dependencies that NewRouter considers
// mandatory. The test only needs the router to be constructed so
// we can verify that api.WithSentryHub(hub) actually reaches the
// router's sentryHub field. The stubs are intentionally stateless;
// their methods are never invoked in this wiring test.

type sentryWiringVault struct{}

func (sentryWiringVault) Save(context.Context, int64, *models.TokenData) error { return nil }
func (sentryWiringVault) Get(context.Context, int64, string) (*models.OAuthToken, error) {
	return nil, errors.New("not implemented")
}
func (sentryWiringVault) Rotate(context.Context, int64, *models.TokenData) error { return nil }
func (sentryWiringVault) Renew(context.Context, int64, string, credentials.TokenRefresher) (*models.OAuthToken, error) {
	return nil, errors.New("not implemented")
}
func (sentryWiringVault) Revoke(context.Context, int64) error { return nil }

type sentryWiringAuthorizer struct{}

func (sentryWiringAuthorizer) AuthorizeChannel(context.Context, int64, string, []string, ...*models.TokenData) (int64, error) {
	return 0, errors.New("not implemented")
}

type sentryWiringOneTimeCodeStore struct{}

func (sentryWiringOneTimeCodeStore) Generate(api.ExchangePayload) (string, error) { return "", nil }
func (sentryWiringOneTimeCodeStore) Consume(string) (api.ExchangePayload, error) {
	return api.ExchangePayload{}, errors.New("not implemented")
}
func (sentryWiringOneTimeCodeStore) Stop() {}

type sentryWiringIdempotencyStore struct{}

func (sentryWiringIdempotencyStore) FindActiveByKey(int64, string, time.Time) (*models.IdempotencyRecord, error) {
	return nil, nil
}
func (sentryWiringIdempotencyStore) Insert(*models.IdempotencyRecord) error { return nil }
func (sentryWiringIdempotencyStore) FindBatchReplay(int64) (*models.BatchReplay, error) {
	return nil, nil
}
func (sentryWiringIdempotencyStore) InsertBatchReplay(*models.BatchReplay) error { return nil }

type sentryWiringConnectLinkNonceStore struct{}

func (sentryWiringConnectLinkNonceStore) Create(string, string, time.Time) error { return nil }
func (sentryWiringConnectLinkNonceStore) Consume(string) error                   { return nil }

// TestConfigureSentry_WiresRouter proves that the Sentry hub
// produced by configureSentry is forwarded into api.Router via
// api.WithSentryHub and that events emitted from that hub are
// delivered to the configured transport. This is the bootstrap
// wiring test: it exercises the same path production uses, without
// needing a real database or S3 backend.
//
// Note: configureSentry mutates Sentry's global client. Tests in
// the same Go package run sequentially by default, so the existing
// TestConfigureSentry and this test do not interfere.
func TestConfigureSentry_WiresRouter(t *testing.T) {
	transport := newFakeSentryTransport()
	hub, err := configureSentry(sentry.ClientOptions{
		Dsn:       "https://public@example.com/1",
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("configureSentry: want nil error, got %v", err)
	}
	if hub == nil {
		t.Fatal("configureSentry: want non-nil hub, got nil")
	}

	manager := auth.NewManager("test-secret-that-is-long-enough-for-jwt-signing", time.Minute, time.Hour)
	capRouter := services.NewCapabilityRouter()

	router, err := api.NewRouter(
		capRouter,
		nil, // userRepo is not used in this wiring test
		manager,
		"http://localhost:5173",
		nil,
		api.WithCredentialVault(sentryWiringVault{}),
		api.WithChannelAuthorizer(sentryWiringAuthorizer{}),
		api.WithOneTimeCodeStore(sentryWiringOneTimeCodeStore{}),
		api.WithIdempotencyStore(sentryWiringIdempotencyStore{}),
		api.WithConnectLinkNonceStore(sentryWiringConnectLinkNonceStore{}),
		api.WithSentryHub(hub),
	)
	if err != nil {
		t.Fatalf("api.NewRouter: %v", err)
	}
	if router == nil {
		t.Fatal("api.NewRouter returned nil router")
	}

	// Verify via reflection that the Sentry hub made it onto the
	// Router. sentryHub is unexported to keep pkg/api decoupled
	// from internal/bootstrap; reflection lets us assert the wiring
	// without changing the public API.
	v := reflect.ValueOf(router).Elem().FieldByName("sentryHub")
	if !v.IsValid() {
		t.Fatal("api.Router does not have a sentryHub field")
	}
	if v.IsNil() {
		t.Fatal("api.WithSentryHub did not wire the hub into the router")
	}

	// Simulate a controlled panic through the wired hub and assert
	// the fake transport receives the event.
	hub.Recover(errors.New("controlled bootstrap wiring panic"))

	event, ok := transport.waitEvent(2 * time.Second)
	if !ok {
		t.Fatal("expected a Sentry event to be sent through the wired transport, got none")
	}
	if event.Exception == nil || len(event.Exception) == 0 {
		t.Fatal("expected exception in Sentry event")
	}
	exc := event.Exception[0]
	if !strings.Contains(exc.Value, "controlled bootstrap wiring panic") {
		t.Errorf("exception value should contain panic message; got %q", exc.Value)
	}
}
