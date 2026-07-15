package contracts

// Mechanical enforcement of the import policy declared in doc.go.
//
// This test FAILS the build if any non-test Go file under
// pkg/api/contracts/ imports a package whose module path begins with
// any of the forbiddenImportPrefixes or with cmd/.
//
// The doc.go policy alone is enforced by code-review discipline and
// is subject to drift across the next ~8 extraction commits. This
// test turns that drift into a red CI signal at PR time. Adding a
// forbidden prefix here is intentional; if a new contract must take
// on a previously-forbidden dependency, the corresponding Domain
// Shift + this test update land in the SAME atomic commit (see
// doc.go "Lock-step rule").

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImportPrefixes mirrors the FORBIDDEN denylist in doc.go.
// Each entry matches via strings.HasPrefix so adding a new prefix
// here automatically protects the entire sub-tree (e.g. an entry for
// "internal/repository/" also blocks internal/repository/foo).
//
// pkg/api and pkg/metrics are explicit (not a wild "pkg/*") because
// the FORBIDDEN list must allow pkg/api/contracts itself for the
// self-package test/import path. Any future pkg/api/X sub-package
// that contracts must not depend on needs to be added here as its
// own entry.
var forbiddenImportPrefixes = []string{
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository",
	"github.com/Marcuss-ops/InstaeditLogin/internal/services",
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth",
	"github.com/Marcuss-ops/InstaeditLogin/internal/bootstrap",
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials",
	"github.com/Marcuss-ops/InstaeditLogin/internal/database",
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox",
	"github.com/Marcuss-ops/InstaeditLogin/internal/providers",
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker",
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto",
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics",
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api/", // explicit; contracts/sub is NOT matched via the trailing "/" combined with the prefix check below
}

// TestNoForbiddenImports walks every non-test Go file under
// pkg/api/contracts/ and asserts each import statement's package
// path is not in the forbiddenImportPrefixes list AND is not a
// cmd/* entrypoint. The "pkg/api/" prefix is intentionally listed
// in the denylist above; the test self-package is parsed by Go
// without explicit self-import so the check does not trip on
// pkg/api/contracts itself.
//
// Run with: go test ./pkg/api/contracts/...
func TestNoForbiddenImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		// Skip test files: tests legitimately import dev-only
		// helpers and parser machinery; the policy applies to
		// production code only.
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(".", name)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			// cmd/ entrypoints are forbidden by category; block the
			// whole tree rather than enumerating every cmd/* subdir.
			if strings.HasPrefix(importPath, "github.com/Marcuss-ops/InstaeditLogin/cmd/") {
				t.Errorf("%s: %q — cmd/* entrypoints are FORBIDDEN in contracts (see doc.go)", path, importPath)
				continue
			}
			for _, forbidden := range forbiddenImportPrefixes {
				// Self-package carve-out: pkg/api/contracts references
				// itself for test fixtures / shared helpers (e.g. a
				// synthetic Store implementation in a sibling *.go).
				// Use exact equality OR the trailing-slash prefix form
				// so a hypothetical pkg/api/contracts/<sub>/ nested
				// package stays exempt from the broader "pkg/api/"
				// denylist prefix check above. Without the trailing
				// slash, the bare pkg/api/contracts prefix would
				// also match pkg/api/contracts_extra and friends.
				if importPath == "github.com/Marcuss-ops/InstaeditLogin/pkg/api/contracts" ||
					strings.HasPrefix(importPath, "github.com/Marcuss-ops/InstaeditLogin/pkg/api/contracts/") {
					continue
				}
				if strings.HasPrefix(importPath, forbidden) {
					t.Errorf("%s: import %q is FORBIDDEN — see doc.go FORBIDDEN list (matched %q) and Domain Shift pre-condition before requesting an exemption", path, importPath, forbidden)
				}
			}
		}
	}
}
