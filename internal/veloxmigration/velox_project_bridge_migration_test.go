package veloxmigration

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestNormalizeMappingsRejectsDuplicateOwnership(t *testing.T) {
	_, err := NormalizeMappings([]Mapping{
		{VeloxProjectID: "ve_1", ProjectID: "thumb_1"},
		{VeloxProjectID: "ve_1", ProjectID: "thumb_2"},
	})
	if !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("expected invalid mapping, got %v", err)
	}
}

func TestNormalizeMappingsRejectsDuplicateInstaEditProject(t *testing.T) {
	_, err := NormalizeMappings([]Mapping{
		{VeloxProjectID: "ve_1", ProjectID: "thumb_1"},
		{VeloxProjectID: "ve_2", ProjectID: "thumb_1"},
	})
	if !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("expected invalid mapping, got %v", err)
	}
}

func TestNormalizeMappingsTrimsAssertions(t *testing.T) {
	got, err := NormalizeMappings([]Mapping{{VeloxProjectID: " ve_1 ", ProjectID: " thumb_1 ", ChannelID: " UC1 ", VideoID: " vid1 ", Language: " en "}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].VeloxProjectID != "ve_1" || got[0].ProjectID != "thumb_1" || got[0].ChannelID != "UC1" || got[0].VideoID != "vid1" || got[0].Language != "en" {
		t.Fatalf("mapping was not normalized: %+v", got[0])
	}
}

func TestSafeToApplyRequiresCompleteReport(t *testing.T) {
	r := &Report{Entries: []Entry{{Status: StatusMatched}, {Status: StatusAlreadyLinked}}}
	updateSummary(r)
	if !safeToApply(r) {
		t.Fatal("complete report should be safe")
	}
	r.Entries = append(r.Entries, Entry{Status: StatusConflict})
	updateSummary(r)
	if safeToApply(r) {
		t.Fatal("conflicting report must not be safe")
	}
}

func TestReportVersionIsStable(t *testing.T) {
	if ReportVersion != "instaedit.velox.bridge-migration.v1" {
		t.Fatal("unexpected report contract version")
	}
}

func TestRollbackRequiresAppliedReportRunID(t *testing.T) {
	_, err := Rollback(context.Background(), nil, Report{Applied: true})
	if !errors.Is(err, ErrRollbackUnsafe) {
		t.Fatalf("expected rollback safety error, got %v", err)
	}
}

func TestVerifyMigrationReadyRejectsMissingMarker(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM information_schema.columns`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	if err := VerifyMigrationReady(context.Background(), db); !errors.Is(err, ErrMigrationNotReady) {
		t.Fatalf("expected schema readiness error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyMigrationReadyRequiresRecordedMigration(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM information_schema.columns`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	if err := VerifyMigrationReady(context.Background(), db); !errors.Is(err, ErrMigrationNotReady) {
		t.Fatalf("expected migration ledger error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyMigrationReadyAcceptsAppliedMigration(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM information_schema.columns`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	if err := VerifyMigrationReady(context.Background(), db); err != nil {
		t.Fatalf("VerifyMigrationReady: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
