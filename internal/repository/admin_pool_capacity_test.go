package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// The three queries must match the repository byte-for-byte (the
// harness uses QueryMatcherEqual). Kept in sync with
// internal/repository/admin_pool_capacity.go.
const adminPoolUsageSQL = `SELECT oc.oauth_client_key,
       COUNT(DISTINCT oc.id) AS active_refresh_grants
  FROM oauth_connections oc
  JOIN tokens t ON t.oauth_connection_id = oc.id
 WHERE oc.provider = 'youtube'
   AND oc.status = 'active'
   AND t.token_type = 'bearer'
   AND t.encrypted_refresh_token IS NOT NULL
   AND octet_length(t.encrypted_refresh_token) > 0
 GROUP BY oc.oauth_client_key`

const adminPoolManagersSQL = `SELECT oc.provider_subject_id,
       oc.oauth_client_key,
       oc.status,
       COUNT(pa.id) AS channels_total,
       COUNT(pa.id) FILTER (WHERE pa.status = 'reauth_required') AS channels_reauth
  FROM oauth_connections oc
  LEFT JOIN platform_accounts pa ON pa.oauth_connection_id = oc.id
 WHERE oc.provider = 'youtube'
   AND oc.provider_subject_id <> ''
 GROUP BY oc.provider_subject_id, oc.oauth_client_key, oc.status
 ORDER BY oc.provider_subject_id, oc.oauth_client_key, oc.status`

const adminPoolChannelsSQL = `SELECT oc.provider_subject_id,
       pa.id,
       pa.platform_user_id,
       COALESCE(pa.username, ''),
       pa.status,
       oc.oauth_client_key,
       oc.status
  FROM platform_accounts pa
  JOIN oauth_connections oc ON oc.id = pa.oauth_connection_id
 WHERE oc.provider = 'youtube'
   AND oc.provider_subject_id <> ''
 ORDER BY oc.provider_subject_id, pa.platform_user_id`

func TestAdminRepository_YouTubePoolCapacity_AssemblesReport(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewAdminRepository(db)

	mock.ExpectQuery(adminPoolUsageSQL).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_client_key", "active_refresh_grants"}).
			AddRow("youtube_pool_a", int64(48)).
			AddRow("youtube_pool_b", int64(43)))

	mock.ExpectQuery(adminPoolManagersSQL).
		WillReturnRows(sqlmock.NewRows([]string{"provider_subject_id", "oauth_client_key", "status", "channels_total", "channels_reauth"}).
			AddRow("google-sub-111", "youtube_pool_a", "active", int64(2), int64(1)).
			AddRow("google-sub-222", "youtube_pool_b", "active", int64(1), int64(0)))

	mock.ExpectQuery(adminPoolChannelsSQL).
		WillReturnRows(sqlmock.NewRows([]string{"subject", "id", "platform_user_id", "username", "status", "client_key", "grant_status"}).
			AddRow("google-sub-111", int64(10), "UCchannelA", "Channel A", "active", "youtube_pool_a", "active").
			AddRow("google-sub-111", int64(11), "UCchannelB", "Channel B", "reauth_required", "youtube_pool_a", "active").
			AddRow("google-sub-222", int64(12), "UCchannelC", "Channel C", "active", "youtube_pool_b", "active"))

	report, err := repo.YouTubePoolCapacity(context.Background())
	if err != nil {
		t.Fatalf("YouTubePoolCapacity: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}

	if len(report.PoolUsage) != 2 {
		t.Fatalf("pool usage: want 2 rows, got %d", len(report.PoolUsage))
	}
	if report.PoolUsage[0].OAuthClientKey != "youtube_pool_a" || report.PoolUsage[0].ActiveRefreshTokens != 48 {
		t.Errorf("pool usage[0]: want (youtube_pool_a, 48), got %+v", report.PoolUsage[0])
	}
	if report.PoolUsage[1].OAuthClientKey != "youtube_pool_b" || report.PoolUsage[1].ActiveRefreshTokens != 43 {
		t.Errorf("pool usage[1]: want (youtube_pool_b, 43), got %+v", report.PoolUsage[1])
	}

	if len(report.Managers) != 2 {
		t.Fatalf("managers: want 2, got %d", len(report.Managers))
	}
	m1 := report.Managers[0]
	if m1.ProviderSubjectID != "google-sub-111" || m1.ChannelsTotal != 2 || m1.ChannelsReauthRequired != 1 {
		t.Errorf("manager[0]: got %+v", m1)
	}
	if len(m1.Channels) != 2 {
		t.Fatalf("manager[0] channels: want 2, got %d", len(m1.Channels))
	}
	if m1.Channels[1].PlatformUserID != "UCchannelB" || m1.Channels[1].Status != "reauth_required" ||
		m1.Channels[1].GrantStatus != "active" || m1.Channels[1].OAuthClientKey != "youtube_pool_a" {
		t.Errorf("manager[0] channel drill-down: got %+v", m1.Channels[1])
	}
	m2 := report.Managers[1]
	if m2.ProviderSubjectID != "google-sub-222" || len(m2.Channels) != 1 {
		t.Errorf("manager[1]: got %+v", m2)
	}
}

func TestAdminRepository_YouTubePoolCapacity_LegacyEmptyClientKeyDefaultsToPoolA(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewAdminRepository(db)

	mock.ExpectQuery(adminPoolUsageSQL).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_client_key", "active_refresh_grants"}).
			AddRow("", int64(5)))
	mock.ExpectQuery(adminPoolManagersSQL).
		WillReturnRows(sqlmock.NewRows([]string{"provider_subject_id", "oauth_client_key", "status", "channels_total", "channels_reauth"}))
	mock.ExpectQuery(adminPoolChannelsSQL).
		WillReturnRows(sqlmock.NewRows([]string{"subject", "id", "platform_user_id", "username", "status", "client_key", "grant_status"}))

	report, err := repo.YouTubePoolCapacity(context.Background())
	if err != nil {
		t.Fatalf("YouTubePoolCapacity: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if len(report.PoolUsage) != 1 || report.PoolUsage[0].OAuthClientKey != "youtube_pool_a" ||
		report.PoolUsage[0].ActiveRefreshTokens != 5 {
		t.Errorf("legacy empty client key must default to youtube_pool_a, got %+v", report.PoolUsage)
	}
}

// A Google manager with grants on BOTH pool clients (the 50/50 fleet
// shape) yields two manager rows for the same subject; the report must
// merge them into ONE manager entry summing the channel totals, with
// the drill-down carrying every channel under the subject.
func TestAdminRepository_YouTubePoolCapacity_MergesSubjectAcrossPools(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewAdminRepository(db)

	mock.ExpectQuery(adminPoolUsageSQL).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_client_key", "active_refresh_grants"}).
			AddRow("youtube_pool_a", int64(48)).
			AddRow("youtube_pool_b", int64(43)))

	// Same subject on both pools: pool A holds 2 channels (1 reauth),
	// pool B holds 3 channels.
	mock.ExpectQuery(adminPoolManagersSQL).
		WillReturnRows(sqlmock.NewRows([]string{"provider_subject_id", "oauth_client_key", "status", "channels_total", "channels_reauth"}).
			AddRow("google-sub-111", "youtube_pool_a", "active", int64(2), int64(1)).
			AddRow("google-sub-111", "youtube_pool_b", "active", int64(3), int64(0)))

	mock.ExpectQuery(adminPoolChannelsSQL).
		WillReturnRows(sqlmock.NewRows([]string{"subject", "id", "platform_user_id", "username", "status", "client_key", "grant_status"}).
			AddRow("google-sub-111", int64(10), "UCchannelA", "Channel A", "active", "youtube_pool_a", "active").
			AddRow("google-sub-111", int64(11), "UCchannelB", "Channel B", "reauth_required", "youtube_pool_a", "active").
			AddRow("google-sub-111", int64(12), "UCchannelC", "Channel C", "active", "youtube_pool_b", "active").
			AddRow("google-sub-111", int64(13), "UCchannelD", "Channel D", "active", "youtube_pool_b", "active").
			AddRow("google-sub-111", int64(14), "UCchannelE", "Channel E", "active", "youtube_pool_b", "active"))

	report, err := repo.YouTubePoolCapacity(context.Background())
	if err != nil {
		t.Fatalf("YouTubePoolCapacity: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}

	if len(report.Managers) != 1 {
		t.Fatalf("managers: want 1 merged entry, got %d", len(report.Managers))
	}
	mgr := report.Managers[0]
	if mgr.ChannelsTotal != 5 || mgr.ChannelsReauthRequired != 1 {
		t.Errorf("merged manager totals: want (5, 1), got (%d, %d)",
			mgr.ChannelsTotal, mgr.ChannelsReauthRequired)
	}
	// First row in deterministic order (pool a before pool b) wins the
	// displayed pool client + grant status.
	if mgr.OAuthClientKey != "youtube_pool_a" || mgr.GrantStatus != "active" {
		t.Errorf("merged manager display: got %+v", mgr)
	}
	if len(mgr.Channels) != 5 {
		t.Fatalf("merged manager channels: want 5, got %d", len(mgr.Channels))
	}
	// Every channel keeps its own pool, including the pool B ones.
	if mgr.Channels[2].OAuthClientKey != "youtube_pool_b" {
		t.Errorf("channel drill-down must keep per-channel pool, got %+v", mgr.Channels[2])
	}
}

func TestAdminRepository_YouTubePoolCapacity_QueryErrorWrapped(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.NewAdminRepository(db)

	mock.ExpectQuery(adminPoolUsageSQL).WillReturnError(errors.New("db unavailable"))

	if _, err := repo.YouTubePoolCapacity(context.Background()); err == nil {
		t.Fatal("YouTubePoolCapacity: want wrapped DB error, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
