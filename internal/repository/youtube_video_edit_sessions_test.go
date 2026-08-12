package repository_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestYouTubeVideoEditRepository_FindDraftByVeloxProjectIDDecodesPostgresValues(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := repository.NewYouTubeVideoEditRepository(db)
	updatedAt := time.Date(2026, 8, 12, 19, 0, 0, 0, time.UTC)
	query := regexp.QuoteMeta(`SELECT draft_title, draft_description, draft_tags,
		        draft_default_language, draft_default_audio_language,
		        draft_translations, draft_desired_privacy, draft_publish_at
		   FROM youtube_video_edits
		  WHERE velox_project_id = $1`)
	mock.ExpectQuery(query).
		WithArgs("ve_comedy_clips").
		WillReturnRows(sqlmock.NewRows([]string{
			"draft_title", "draft_description", "draft_tags",
			"draft_default_language", "draft_default_audio_language",
			"draft_translations", "draft_desired_privacy", "draft_publish_at",
		}).AddRow(
			"Actors Comedy Clips", "Riferimenti clip verificati", "{comedy,actors}",
			"it", "it",
			[]byte(`{"it":{"title":"Clip comiche","description":"Momenti spontanei"}}`),
			"private", updatedAt,
		))

	draft, err := repo.FindDraftByVeloxProjectID(context.Background(), "ve_comedy_clips")
	if err != nil {
		t.Fatalf("FindDraftByVeloxProjectID: %v", err)
	}
	if draft == nil {
		t.Fatal("FindDraftByVeloxProjectID returned nil draft")
	}
	if len(draft.DraftTags) != 2 || draft.DraftTags[0] != "comedy" || draft.DraftTags[1] != "actors" {
		t.Fatalf("decoded tags: %#v", draft.DraftTags)
	}
	if got := draft.DraftTranslations["it"].Title; got != "Clip comiche" {
		t.Fatalf("decoded translations title: %q", got)
	}
	if draft.DraftPublishAt == nil || !draft.DraftPublishAt.Equal(updatedAt) {
		t.Fatalf("decoded publish time: %v", draft.DraftPublishAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
