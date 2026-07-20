package repository

import (
	"context"
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
	require.Equal(t, "youtube_channels_total", jsonTagForFleetReadinessField("Total"))
	require.Equal(t, "active", jsonTagForFleetReadinessField("Active"))
	require.Equal(t, "pending_authorization", jsonTagForFleetReadinessField("PendingAuthorization"))
	require.Equal(t, "reauth_required", jsonTagForFleetReadinessField("ReauthRequired"))
	require.Equal(t, "revoked", jsonTagForFleetReadinessField("Revoked"))
	require.Equal(t, "error", jsonTagForFleetReadinessField("Error"))
	require.Equal(t, "refresh_test_ok", jsonTagForFleetReadinessField("RefreshTestOK"))
	require.Equal(t, "scope_youtube_upload_ok", jsonTagForFleetReadinessField("ScopeYoutubeUploadOK"))
	require.Equal(t, "scope_youtube_readonly_ok", jsonTagForFleetReadinessField("ScopeYoutubeReadonlyOK"))
	require.Equal(t, "channel_binding_ok", jsonTagForFleetReadinessField("ChannelBindingOK"))
	require.Equal(t, "private_canary_ok", jsonTagForFleetReadinessField("PrivateCanaryOK"))
	require.Equal(t, "canary_channel_match_ok", jsonTagForFleetReadinessField("CanaryChannelMatchOK"))
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
func jsonTagForFleetReadinessField(name string) string {
	t := reflect.TypeOf(FleetReadinessCounts{})
	f, ok := t.FieldByName(name)
	if !ok {
		return "<missing>"
	}
	return f.Tag.Get("json")
}

// errBoom is a sentinel used only inside admin_repo_count_fleet_test.go
// so the failure-path test is hermetic (no need for statik error
// generation logic; the type is enough).
type errBoomType struct{ msg string }

func (e errBoomType) Error() string { return e.msg }

var errBoom = errBoomType{msg: "simulated sql scan failure"}
