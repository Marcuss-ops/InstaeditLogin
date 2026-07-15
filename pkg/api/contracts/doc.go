// Package contracts declares the domain-spanning interfaces used by the
// HTTP layer (pkg/api). Each interface mirrors a subset of the
// capability the Router needs in order to satisfy a request.
//
// Contracts are extracted from pkg/api/handlers.go (which historically
// accumulated every per-domain Store interface in a single 1500+ LOC
// file) into small, domain-named files inside this leaf package so:
//
//   - tests and providers can implement the contract without dragging
//     in the *sql.DB-bound repository types;
//   - pkg/api/handlers.go stays focused on routing/middleware and
//     command-line boot wiring; and
//   - future swaps (mocking, in-memory fakes, alternate
//     implementations for a Taglio) become one-file diffs instead of
//     requiring edits across the API surface.
//
// Each interface declaration lives on its own; concrete implementations
// and production constructors stay in pkg/api to avoid creating an import
// cycle (contracts is a leaf — nothing here may import pkg/api).
//
// # Import policy (architecture contract — enforced by code review)
//
// Principle: Interface Segregation. The HTTP contract layer must be
// implementable by any in-memory fake without dragging SQL drivers,
// HTTP clients, or worker runtimes into the test process. This is the
// reason pkg/api/handlers.go is being dismantled — preserving the
// principle across the move is what makes the refactor a win rather
// than a re-shuffle. Cycle prevention is a happy side-effect, not the
// primary goal.
//
// ALLOWED dependencies:
//
//   - the Go standard library (context, time, errors, …);
//   - internal/models — plain DTO/entity structs that cross layer
//     boundaries by design (Workspace, Post, ApiKey, PlatformAccount,
//     IdempotencyRecord, and once Domain-Shifted: UploadGrant,
//     StartSessionRequest, ConnectionState, Session, …).
//
// FORBIDDEN dependencies (explicit denylist so drift is verifiable
// line-by-line in code review across the next ~9 commits):
//
//   - internal/repository       — SQL-bound persistence (pgx driver,
//                                 connection pool, table-coupled
//                                 structs). Importing here is the
//                                 primary regression risk.
//   - internal/services         — domain services transitively pull
//                                 internal/repository, internal/auth,
//                                 etc. There is no "pure services"
//                                 path: services are categorically
//                                 forbidden. Service-layer
//                                 request/response DTOs that a contract
//                                 needs MUST Domain-Shift to
//                                 internal/models BEFORE the contract
//                                 is extracted.
//   - internal/auth             — JWT/session primitives; depend on
//                                 crypto and PostgreSQL lookups.
//   - internal/bootstrap        — wiring/composition root.
//   - internal/credentials      — encrypted-token vault (Postgres-
//                                 bound).
//   - internal/database         — pgx pool, query helpers.
//   - internal/outbox           — transactional outbox.
//   - internal/providers        — provider registry.
//   - internal/worker           — background job runners.
//   - internal/crypto           — AES-GCM key map.
//   - pkg/api                   — the HTTP layer consuming this
//                                 package (would invert the dependency
//                                 direction).
//   - pkg/metrics               — observation instrumentation; pulls
//                                 Prometheus clients.
//   - cmd/*                     — entrypoints.
//
// (pkg/api/contracts itself is implicitly not in the denylist by
// virtue of being the file we are inside; future subfiles added under
// pkg/api/contracts/ are likewise OK.)
//
// # Domain Shift pre-condition
//
// For every type a contract references that today lives outside
// `internal/models` (e.g. `*repository.ConnectionState`,
// `services.UploadGrant`, `services.StartSessionRequest`), the type
// MUST be Domain-Shifted to `internal/models` BEFORE the contract
// that references it can be extracted. The original location keeps
// a Go type alias for the moved type so every external call site
// continues to resolve:
//
//   // internal/repository/connection_state_repo.go
//   type ConnectionState = models.ConnectionState
//
//   // internal/services/sessions_service.go
//   type StartSessionRequest = models.StartSessionRequest
//
// ## Method-set audit before a struct Domain Shift
//
// Before committing any struct Domain Shift, run (against every
// declaring file involved in the move):
//
//   grep -EH 'func \([a-zA-Z]+ \*?[A-Z][a-zA-Z]+\)' \
//       internal/repository/connection_state_repo.go \
//       internal/repository/sessions_repo.go \
//       internal/services/sessions_service.go \
//       internal/services/storage.go
//
// Any methods attached to the type in its original package must move
// to `internal/models` alongside the struct — a Go type alias
// re-exports the name but does NOT migrate the method set bound to
// the defining package. If the audit returns nothing (expected for
// pure DTOs) the alias-only Domain Shift is safe.
//
// ## Service-DTO audit before a service DTO Domain Shift
//
// Same audit applies to service-layer DTOs (`UploadGrant`,
// `StartSessionRequest`, etc.). These structs are blanket pure DTOs
// today; if a future release adds methods, the same migration rule
// applies.
//
// ## Atomicity rule
//
// A Domain Shift (struct or service-DTO relocation + paired Go type
// alias in the original location) and the corresponding contract
// extraction MUST land in the SAME atomic commit. Splitting them
// across two commits leaves the build in a transient state that
// spans three locations for a single logical change, and any revert
// now has to coordinate two commits. The "type alias + collapse in
// cleanup commit" idiom is the same for structs and interfaces; one
// mental model across the entire refactor.
//
// # Scope clarification
//
// The original followup listed 12 interfaces under
// pkg/api/contracts/{users,workspaces,posts,…}.go, but handlers.go
// only holds 10 of them (UserWorkspaceHelper, ConnectionStateStore,
// SessionsStore, UserStore, WorkspaceStore, PostStore, ApiKeyStore,
// IdempotencyStore, StorageProvider, AuditLogStore). The remaining 2
// names — MediaStore, BillingServiceAPI, AuthEmailStore, TeamStore,
// WebhookStore — actually live in OTHER files (pkg/api/media.go,
// pkg/api/billing.go, pkg/api/auth_email.go, pkg/api/team.go,
// pkg/api/webhooks.go) and are NOT in scope for the "handlers.go
// extraction pipeline" tracked by these commits. They will be
// migrated from their respective files in a separate followup
// pipeline once the handlers.go chain is stable and the policy is
// validated end-to-end.
//
// # Lock-step rule for future commits
//
// Any commit that introduces or modifies a contracts file MUST
// either:
//
//   (a) re-justify any newly-accepted dependency in a matching
//       comment and update the ALLOWED list above; or
//   (b) extend or replace the Domain Shift pre-condition to cover
//       the new dependency.
//
// Drift that re-imports an item from the FORBIDDEN list without an
// accompanying Domain Shift is the primary regression mode for this
// refactor across the remaining ~8 commits.
//
// # Mechanical enforcement
//
// A non-doc enforcement of these rules lives in
// `pkg/api/contracts/contracts_test.go`. The test walks every
// non-test Go file in this package using stdlib `go/parser` and
// fails the build (`t.Errorf` marks the test FAIL) on any import
// that matches the FORBIDDEN list (or lands under `cmd/*`). Drift
// that re-imports a previously-forbidden package surfaces as a red
// CI signal at PR time instead of in a future refactor retrospective.
//
// The forbiddenImportPrefixes slice in contracts_test.go IS the
// source of truth for the FORBIDDEN list and must be kept in
// lock-step with the bullets above. To add or change an entry, edit
// BOTH places in the SAME atomic commit (per the Lock-step rule).
// Sub-packages of pkg/api/contracts are exempt from the "pkg/api/"
// prefix via the trailing-slash carve-out in the test's inner loop.
// When a new contract must accept a previously-forbidden dependency,
// the Domain Shift + the test's forbiddenImportPrefixes update land
// in the SAME atomic commit (see Domain Shift pre-condition).
package contracts
