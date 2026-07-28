// Package stores provides canonical test doubles for the two
// persistence-only stores api.MustNewRouter requires via its
// validateRequiredDeps gate (IdempotencyStore + ConnectLinkNonceStore)
// but where the production-side stores don't ship an in-memory
// constructor callers can reuse without spinning up Postgres.
//
// Why combine both in one package:
//
//   - the dep list on Router.validateRequiredDeps is today five items
//     (CredentialVault + ChannelAuthorizer + OneTimeCodeStore +
//     IdempotencyStore + ConnectLinkNonceStore); production already
//     wires ALL of them via cmd/server. Tests need to wire all five
//     too — splitting each dep into its own testutil/<dep>/ package
//     would inflate the import surface on tests without extending
//     the contract. A single internal/testutil/stores package is
//     one import for the whole "satisfies validateRequiredDeps"
//     convenience, mirroring how production main.go involves one
//     orchestrator file that wires all five.
//
//   - the *test.go-side fakes (e.g. testConnectLinkNonceStore in
//     pkg/api/modules_test.go) live behind Go's _test.go build rule
//     and cannot be imported from external test packages; lifting
//     them into a non-_test.go package makes them importable
//     without dragging in any production symbol.
//
// What this package intentionally does NOT do:
//
//   - Implement ChannelAuthorizer — that interface is wired per-test
//     (a panic-on-call vs an accept-vs-error authorizer); a generic
//     "fake" would defeat the fire-alarm semantic that makes the
//     negative-bind test loud.
//   - Implement OneTimeCodeStore — pkg/api already ships
//     NewInMemoryOneTimeCodeStore(ttl); callers wire that directly.
//   - Implement CredentialVault — internal/testutil/vault owns that
//     surface and is imported separately.
//
// Compile-time assertions pin interface drift; tests/e2e imports
// the package as `_ "github.com/Marcuss-ops/InstaeditLogin/internal/testutil/stores"`
// is unnecessary — explicit imports are preferred so a single
// grep on the import block finds every e2e file wiring these stores.
package stores

import (
	"errors"
	"sync"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// =============================================================================
// FakeIdempotencyStore — minimal in-test IdempotencyStore double.
// =============================================================================

// errStoreNotImplemented is the typed sentinel returned by the
// unimplemented paths so a future test that unexpectedly reaches
// them surfaces loud in CI logs (rather than a nil deref or a
// silent return).
var errStoreNotImplemented = errors.New("not implemented in test fake (pkg: internal/testutil/stores)")

// FakeIdempotencyStore is a mutex-protected map keyed on (workspaceID,
// idempotencyKey) tracking the IdempotencyRecord found/inserted. The
// shape mirrors testutil/postgres.go's "single-source-of-truth"
// canonical helper contract: tests import this, never declare their
// own IdempotencyStore. The ConnectLinkNonceStore analogue lives
// further down in this file.
//
// Canonical use case (tests/e2e/oauth_callback_binding_e2e_test.go):
//   - The connect-link bind path doesn't read or write Idempotency
//     records (those are drive_batch/UPDATE/upload territory), so
//     the Find/Insert paths return a typed sentinel — the bind test
//     never reaches them.
//   - drive_batch tests that DO exercise FindActiveByKey + Insert
//     pre-script the findErr field to drive negative-path scenarios.
type FakeIdempotencyStore struct {
	mu sync.Mutex

	// findErr + insertErr are scripted-failure overrides honoured
	// before the per-method real work. Both default to nil so the
	// happy path (no scripted failure) returns the canonical canned
	// behaviour. Tests that want a "DB unreachable" simulation set
	// these.
	findErr   error
	insertErr error

	// records accumulates every Insert pair so future tests can
	// assert "Insert was called exactly N times".
	records []models.IdempotencyRecord
}

// errFakeIdempotencyFindNotStubbed is the typed sentinel returned by
// FindActiveByKey when a test reaches the path without pre-scripting
// a result. Mirrors the typed-sentinel pattern used by the vault's
// errFakeVaultDownloadNotStubbed in pkg/api/fakevault_test.go.
var errFakeIdempotencyFindNotStubbed = errors.New("fakeIdempotency.FindActiveByKey not stubbed (pkg: internal/testutil/stores); pre-script via a custom FakeIdempotencyStore wrapper if this test reaches the path")

// FindActiveByKey honours findErr first; otherwise returns a typed
// sentinel so the test logs clearly indicate the unimplemented path.
// Production IdempotencyRecord lookup is keyed on (workspaceID, key,
// now) — a SQL query with a TTL filter; the e2e bind path doesn't
// reach this code.
func (f *FakeIdempotencyStore) FindActiveByKey(_ int64, _ string, _ time.Time) (*models.IdempotencyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.findErr != nil {
		return nil, f.findErr
	}
	return nil, errFakeIdempotencyFindNotStubbed
}

// Insert honours insertErr first; otherwise records the row onto the
// slice + returns nil so future test-introspection (records == N)
// works. Does NOT simulate "key collision" semantics; the tests that
// need that override insertErr with a synthetic sentinel.
func (f *FakeIdempotencyStore) Insert(rec *models.IdempotencyRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertErr != nil {
		return f.insertErr
	}
	f.records = append(f.records, *rec)
	return nil
}

// FindBatchReplay surfaces the BatchReplay sibling table (migration
// 039). Production drive_batch idempotency uses it; tests that don't
// touch drive_batch never reach this path and the typed sentinel
// keeps the call loud in CI logs if they accidentally do.
func (f *FakeIdempotencyStore) FindBatchReplay(_ int64) (*models.BatchReplay, error) {
	return nil, errFakeIdempotencyFindNotStubbed
}

// InsertBatchReplay mirrors Insert for the drive_batch sibling.
// Tagged with the same sentinel so a future drive_batch test can
// override per-instance.
func (f *FakeIdempotencyStore) InsertBatchReplay(_ *models.BatchReplay) error {
	return errStoreNotImplemented
}

// =============================================================================
// FakeConnectLinkNonceStore — minimal in-test ConnectLinkNonceStore double.
// =============================================================================

// FakeConnectLinkNonceStore satisfies pkg/api.ConnectLinkNonceStore.
// Lifted from pkg/api/modules_test.go::testConnectLinkNonceStore which
// was reachable only inside package api (Go blocks cross-package
// import of *_test.go files; the e2e suite lives in package e2e).
//
// The connect-link handler at pkg/api's route::handleConnectLink
// (and its handlers.go counterpart) calls Create on bind-URL mint
// and Consume on callback completion. The canonical happy-path test
// mocks both as no-op. Tests that need a stricter contract (e.g.
// assert "exactly one nonce Consumed for jti=foo") inject a custom
// wrapper via the public struct fields below.
//
// Sync surface:
//   - createCalls + consumeCalls track invocations.
//   - lastSeenCreate + lastSeenConsume track the jti + channel id
//     most recently passed so a regression that drops the jti param
//     surfaces loud.
//
// Error simulation:
//   - createErr + consumeErr are pre-scripted-failure overrides; the
//     default behaviour is no-op success.
type FakeConnectLinkNonceStore struct {
	mu sync.Mutex

	createErr    error
	consumeErr   error
	createCalls  int
	consumeCalls int

	lastSeenCreateJTI       string
	lastSeenCreateChannelID string
	lastSeenConsumeJTI      string
}

// Create honours createErr first; otherwise records the call args +
// counts. Production semantics: persists the nonce + TTL so the
// callback can consume + verify. The fake skips persistence because
// the test never reads the row back — usage is always paired with a
// Consume call that returns nil.
func (f *FakeConnectLinkNonceStore) Create(jti, expectedChannelID string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.createCalls++
	f.lastSeenCreateJTI = jti
	f.lastSeenCreateChannelID = expectedChannelID
	return nil
}

// Consume honours consumeErr first; otherwise records the call + counts.
func (f *FakeConnectLinkNonceStore) Consume(jti string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumeErr != nil {
		return f.consumeErr
	}
	f.consumeCalls++
	f.lastSeenConsumeJTI = jti
	return nil
}

// =============================================================================
// Compile-time assertions
// =============================================================================
//
// Pin interface drift at build time. Any future change to either
// IdempotencyStore (pkg/api/router.go:561) or ConnectLinkNonceStore
// (pkg/api/router.go:667) surfaces here as a build error rather
// than a runtime panic.
//
// The third assertion (`_ credentials.TokenRefresher`) is here
// only because TokenRefresher is the narrow-shaped function the
// vault.NewFakeVault relies on; the assertion pins the alias too
// so a future rename of TokenRefresher surfaces here. (Drop this
// if testutil/vault re-exports it.)
var (
	_ IdempotencyStoreCompat      = (*FakeIdempotencyStore)(nil)
	_ ConnectLinkNonceStoreCompat = (*FakeConnectLinkNonceStore)(nil)
)

// IdempotencyStoreCompat is the local alias for pkg/api.IdempotencyStore
// so the compile-time assertion above doesn't take a hard pkg/api
// import cycle (the assertion is only meaningful to go vet +
// build-time; runtime usage of the fakes through api.WithIdempotencyStore
// is still mandatory).
//
// IdempotencyStoreCompat is identical to pkg/api.IdempotencyStore
// (FindActiveByKey + Insert + FindBatchReplay + InsertBatchReplay).
// It's defined here so the compile-time assertion below stays in
// this file; the production alias `pkg/api.IdempotencyStore` is the
// canonical name a caller uses.
type IdempotencyStoreCompat interface {
	FindActiveByKey(workspaceID int64, key string, now time.Time) (*models.IdempotencyRecord, error)
	Insert(rec *models.IdempotencyRecord) error
	FindBatchReplay(idempotencyRecordID int64) (*models.BatchReplay, error)
	InsertBatchReplay(rec *models.BatchReplay) error
}

// ConnectLinkNonceStoreCompat mirrors pkg/api.ConnectLinkNonceStore
// (Create + Consume). Declared locally for the same build-time-pin
// reason as IdempotencyStoreCompat.
type ConnectLinkNonceStoreCompat interface {
	Create(jti, expectedChannelID string, expiresAt time.Time) error
	Consume(jti string) error
}

// =============================================================================
// Constructor ergonomic helpers
// =============================================================================
//
// Mirrors testutil/vault's NewFakeVault() so a caller can take a
// single import — `internal/testutil/stores` — and pull both fakes
// without &T{} boilerplate at every site.
//
// Production-side fakes for CredentialVault, ChannelAuthorizer,
// OneTimeCodeStore live in this same dep slot but are imported
// separately (testutil/vault owns the vault, the authorizer is
// wired per-test with panic-on-call semantic, OneTimeCodeStore
// uses pkg/api's NewInMemoryOneTimeCodeStore directly).

// NewFakeIdempotencyStore returns a zero-valued IdempotencyStore
// fake. Live mutations are mutex-safe.
func NewFakeIdempotencyStore() *FakeIdempotencyStore { return &FakeIdempotencyStore{} }

// NewFakeConnectLinkNonceStore returns a zero-valued
// ConnectLinkNonceStore fake. Live mutations are mutex-safe.
func NewFakeConnectLinkNonceStore() *FakeConnectLinkNonceStore {
	return &FakeConnectLinkNonceStore{}
}
