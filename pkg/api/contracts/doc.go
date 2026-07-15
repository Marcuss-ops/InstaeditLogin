// Package contracts declares the domain-spanning interfaces used by the
// HTTP layer (pkg/api). Each interface mirrors a subset of an
// internal/repository or internal/services capability that the Router
// needs in order to satisfy a request.
//
// Contracts are extracted from pkg/api/handlers.go (which historically
// accumulated every per-domain Store interface in a single 1500+ LOC
// file) into small, domain-named files inside this leaf package so:
//
//   - tests and providers can implement them without dragging in the
//     *sql.DB-bound repository types;
//   - pkg/api/handlers.go stays focused on routing/middleware and
//     command-line boot wiring; and
//   - future swaps (mocking, in-memory fakes, alternate
//     implementations for a Taglio) become one-file diffs instead of
//     requiring edits across the API surface.
//
// Only the interface declaration lives here. Concrete implementations
// (e.g. repoUserWorkspaceHelper) and production constructors
// (e.g. RepoUserWorkspaceHelper) stay in pkg/api to avoid creating an
// import cycle (contracts is a leaf — nothing inside this package
// may import pkg/api or internal/*).
//
// pkg/api/handlers.go keeps a type alias for each moved interface so
// existing references (`WorkspaceStore`, `UserStore`, …) continue to
// resolve without touching workspaces.go, posts.go, etc. Those aliases
// will be removed in a final cleanup commit once every reference has
// migrated to `contracts.X`.
package contracts
