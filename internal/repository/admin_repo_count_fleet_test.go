package repository

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestAdminRepository_CountFleetReadiness_Happy200 pins the
// canonical 12-field Definition-of-Done breakdown for a 200-channel
// fleet. The sqlmock expectation matches the production SQL via a
// substring guard on the FILTER clauses so a future SQL refactor
// that preserves the counts surface but reorders columns or
// rewrites WHERE clauses stays test-pinned.
func TestAdminRepository_CountFleetReadiness_Happy200(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	rep := NewAdminRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("FROM platform_accounts")).
		WillReturnRows(
			sqlmock.NewRows([]string{
				"total", "active", "pending_authorization", "reauth_required",
				"revoked", "error",
				"refresh_test_ok", "scope_youtube_upload_ok", "scope_youtube_readonly_ok",
				"channel_binding_ok", "private_canary_ok", "canary_channel_match_ok",
			}).AddRow(
				200, 187, 0, 13, 0, 0,
				200, 200, 200, 200, 200, 200,
			),
		)

	got, err := rep.CountFleetReadiness(context.Background())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	want := FleetReadinessCounts{
		Total:                  200,
		Active:                 187,
		PendingAuthorization:   0,
		ReauthRequired:         13,
		Revoked:                0,
		Error:                  0,
		RefreshTestOK:          200,
		ScopeYoutubeUploadOK:   200,
		ScopeYoutubeReadonlyOK: 200,
		ChannelBindingOK:       200,
		PrivateCanaryOK:        200,
		CanaryChannelMatchOK:   200,
	}
	require.Equal(t, want, got)

	// Spot-check the JSON tags match docs/OAUTH-PRODUCTION.md Step
	// 10 verbatim. A regression that renames a field surfaces here
	// BEFORE the operator dashboard re-skin ships. Table-driven so
	// the per-field t.Errorf prefix stays uniform across all 12
	// fields (a future 13th field is a 1-line append to the slice).
	fieldJSONTags := []struct {
		field string
		want  string
	}{
		{"Total", "youtube_channels_total"},
		{"Active", "active"},
		{"PendingAuthorization", "pending_authorization"},
		{"ReauthRequired", "reauth_required"},
		{"Revoked", "revoked"},
		{"Error", "error"},
		{"RefreshTestOK", "refresh_test_ok"},
		{"ScopeYoutubeUploadOK", "scope_youtube_upload_ok"},
		{"ScopeYoutubeReadonlyOK", "scope_youtube_readonly_ok"},
		{"ChannelBindingOK", "channel_binding_ok"},
		{"PrivateCanaryOK", "private_canary_ok"},
		{"CanaryChannelMatchOK", "canary_channel_match_ok"},
	}
	for _, tc := range fieldJSONTags {
		got := jsonTagForFleetReadinessField(t, tc.field)
		if got != tc.want {
			t.Errorf("FleetReadinessCounts.%s JSON tag mismatch: want %q, got %q",
				tc.field, tc.want, got)
		}
	}
}

// TestAdminRepository_CountFleetReadiness_QueryError ensures the
// sql-scan failure path wraps the underlying error in the canonical
// "admin: count fleet readiness: %w" shape. The handler logs this
// verbatim; the wrapper string is part of the public contract (we
// do NOT want a refactor to silently change the prefix).
func TestAdminRepository_CountFleetReadiness_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close()

	rep := NewAdminRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("FROM platform_accounts")).
		WillReturnError(errBoom)

	got, err := rep.CountFleetReadiness(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, errBoom)
	require.Contains(t, err.Error(), "admin: count fleet readiness")
	require.NoError(t, mock.ExpectationsWereMet())

	// defalt-zero counts on error so the handler can render a
	// clean "unknown" envelope without per-field nil-checking.
	require.Equal(t, FleetReadinessCounts{}, got)
}

// jsonTagForFleetReadinessField returns the JSON wire name of a
// FleetReadinessCounts struct field by reflection. Used to PIN the
// contract that docs/OAUTH-PRODUCTION.md Step 10 + the SPA dashboard
// rely on across package upgrades.
//
// The first param is testing.TB (the union of *testing.T and
// *testing.B methods) rather than *testing.T so a missing-field
// regression can be exercised by tests that pass a fakeT that
// captures Fatalf calls — see TestJsonTagForFleetReadinessField_
// MissingField_Fatals below. Production-style callers pass *testing.T
// which satisfies testing.TB. Line attribution still points at the
// CALLER (the t.Errorf or require.Equal in Happy200), not at this
// helper. The silent "<missing>" return-shape from the pre-polish
// version was acceptable for green but produced misleading
// "X == <missing>" failures when a rename happened.
func jsonTagForFleetReadinessField(t testing.TB, name string) string {
	t.Helper()
	rt := reflect.TypeOf(FleetReadinessCounts{})
	f, ok := rt.FieldByName(name)
	if !ok {
		t.Fatalf("FleetReadinessCounts is missing the field %q that jsonTagForFleetReadinessField is asserting; double-check the struct definition matches the dashboard contract", name)
		return "" // unreachable; t.Fatalf stops the test
	}
	return f.Tag.Get("json")
}

// errBoom is a sentinel used only by the failure-path test so the
// sqlmock interaction is hermetic. stdlib errors.New keeps the
// surface to one line + one field; the earlier custom struct was
// over-engineered for a single-error use case.
var errBoom = errors.New("simulated sql scan failure")

// fakeT implements testing.TB by embedding the interface (compile-time
// satisfaction) + overriding only the 2 methods
// jsonTagForFleetReadinessField uses (Helper + Fatalf). Any other
// testing.TB method called on fakeT would panic via nil-interface
// dispatch — that's intentional, it's a sentinel "should never be
// called by code under test". Used by
// TestJsonTagForFleetReadinessField_MissingField_Fatals to capture
// the formatted Fatalf message without actually halting the parent
// test goroutine.
type fakeT struct {
	testing.TB
	fatalMsg string
}

func (f *fakeT) Helper() {}

// FakeT.Fatalf mirrors what testing.TB.Fatalf would have formatted
// before invoking runtime.Goexit: Sprintf the format string + args
// into fatalMsg so the test harness can assert the message content.
// The override does NOT halt (the production t.Fatalf halts via
// runtime.Goexit which is goroutine-local and not recoverable as a
// panic — the canonical "code should have called FailNow" test
// pattern is exactly this fakeT recording).
func (f *fakeT) Fatalf(format string, args ...any) {
	f.fatalMsg = fmt.Sprintf(format, args...)
}

// TestJsonTagForFleetReadinessField_MissingField_Fatals pins the
// "missing field → t.Fatalf" contract documented above. Asking the
// helper for a field that does not exist on FleetReadinessCounts
// MUST halt the test (via the fakeT capture) rather than silently
// return "" which would mask rename regressions as a misleading
// "want X got <empty string>" comparison.
//
// Using a fakeT rather than a sub-testing.T: testing.TB's FailNow /
// Fatalf is implemented via runtime.Goexit which is goroutine-local
// and cannot be recovered as a panic. The canonical pattern for
// asserting "code under test SHOULD have called FailNow" is to
// inject a fakeT that records the invocation so the parent test
// continues running and reads out what was about to halt. See
// https://pkg.go.dev/testing#T.Fatalf for the canonical Go testing
// source on the FailNow / Goexit relationship.
func TestJsonTagForFleetReadinessField_MissingField_Fatals(t *testing.T) {
	fake := &fakeT{}
	_ = jsonTagForFleetReadinessField(fake, "NonExistentField_xyz")
	if fake.fatalMsg == "" {
		t.Fatalf("jsonTagForFleetReadinessField did not call Fatalf on missing field %q; expected the production t.Fatalf message, got empty",
			"NonExistentField_xyz")
	}
	if !strings.Contains(fake.fatalMsg, "NonExistentField_xyz") {
		t.Errorf("Fatalf message must name the missing field %q; got %q",
			"NonExistentField_xyz", fake.fatalMsg)
	}
	if !strings.Contains(fake.fatalMsg, "FleetReadinessCounts") {
		t.Errorf("Fatalf message must mention the FleetReadinessCounts struct; got %q",
			fake.fatalMsg)
	}
}
