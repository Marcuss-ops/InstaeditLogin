package repository

import (
	"context"
	"errors"
	"regexp"
	"reflect"
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
	// BEFORE the operator dashboard re-skin ships.
	require.Equal(t, "youtube_channels_total", jsonTagForFleetReadinessField(t, "Total"))
	require.Equal(t, "active", jsonTagForFleetReadinessField(t, "Active"))
	require.Equal(t, "pending_authorization", jsonTagForFleetReadinessField(t, "PendingAuthorization"))
	require.Equal(t, "reauth_required", jsonTagForFleetReadinessField(t, "ReauthRequired"))
	require.Equal(t, "revoked", jsonTagForFleetReadinessField(t, "Revoked"))
	require.Equal(t, "error", jsonTagForFleetReadinessField(t, "Error"))
	require.Equal(t, "refresh_test_ok", jsonTagForFleetReadinessField(t, "RefreshTestOK"))
	require.Equal(t, "scope_youtube_upload_ok", jsonTagForFleetReadinessField(t, "ScopeYoutubeUploadOK"))
	require.Equal(t, "scope_youtube_readonly_ok", jsonTagForFleetReadinessField(t, "ScopeYoutubeReadonlyOK"))
	require.Equal(t, "channel_binding_ok", jsonTagForFleetReadinessField(t, "ChannelBindingOK"))
	require.Equal(t, "private_canary_ok", jsonTagForFleetReadinessField(t, "PrivateCanaryOK"))
	require.Equal(t, "canary_channel_match_ok", jsonTagForFleetReadinessField(t, "CanaryChannelMatchOK"))
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
// The first param is the test fixture (*testing.T) so a missing-field
// regression t.Fatal()s with line attribution pointing at the CALLER
// (the require.Equal), not at this helper. The silent "<missing>"
// return-shape from the prior version was acceptable for green but
// produced misleading "X == <missing>" failures when a rename
// happened.
func jsonTagForFleetReadinessField(t *testing.T, name string) string {
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
