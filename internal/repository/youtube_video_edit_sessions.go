package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// MarkPublishing (Blocco #5 P0 #2) is the atomic CAS claim for the
// thumbnail-publish transition. The handler's previous read-then-update
// pattern (`edit.Status = "publishing"; Update(ctx, edit)`) had a TOCTOU
// race: two concurrent publish requests could both pass the row-state
// read and fire two PublishThumbnail calls (a real production bug —
// video would be thumbnail-published twice).
//
// CAS structure:
//
//	STRICT BRANCH (inFlightTimeout <= 0):
//	  UPDATE ... SET status, updated_at, desired_privacy, publish_at
//	   WHERE id=$1 AND status IN ('editing','failed')
//	   RETURNING ...
//	EXTENDED BRANCH (inFlightTimeout > 0):
//	  UPDATE ... SET ...  WHERE id=$1 AND (
//	     status IN ('editing','failed')
//	     OR (status='publishing' AND updated_at < NOW() - make_interval(secs => $4))
//	  ) RETURNING ...
//
// The extended branch is the orphan-recovery path: a previous publish
// was claimed but the worker died mid-call (status stuck at
// 'publishing' with no published_at stamp). The handler's in-flight
// read at the top of the function would have already rejected a
// recent (within timeout) sticky 'publishing' row with 409; this
// branch picks up only rows older than the timeout.
//
// include-desired_privacy-and-publish_at-in-CAS so the publish call sees
// the values that were atomically stamped (no chance of a concurrent
// reader flipping the resolved privacy between the MarkPublishing
// return and the PublishThumbnail call). Avoiding the
// read-then-modify-write reverts every race that the original
// handler was subject to (privacy/publish_at swap while the row was
// being updated).
//
// Returns ErrYouTubeVideoEditNotFound (wrapped) when 0 rows match —
// distinct from a real *sql.DB error so the handler can branch on
// errors.Is(..., ErrYouTubeVideoEditNotFound) to map to HTTP 409.
func (r *YouTubeVideoEditRepository) MarkPublishing(ctx context.Context, id string, desiredPrivacy string, publishAt *time.Time, inFlightTimeout time.Duration) (*models.YouTubeVideoEdit, error) {
	edit := &models.YouTubeVideoEdit{}
	var err error
	if inFlightTimeout <= 0 {
		// Strict branch: only editing/failed are claimable.
		err = r.db.QueryRowContext(ctx,
			`UPDATE youtube_video_edits
			 SET status = 'publishing', updated_at = NOW(),
			     desired_privacy = $2, publish_at = $3
			 WHERE id = $1 AND status IN ('editing','failed')
			 RETURNING `+youtubeVideoEditSelectColumns,
			id, desiredPrivacy, publishAt,
		).Scan(
			&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
			&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
			&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
			&edit.CreatedAt, &edit.UpdatedAt,
		)
	} else {
		// Extended branch: also re-claim a stale 'publishing' row.
		err = r.db.QueryRowContext(ctx,
			`UPDATE youtube_video_edits
			 SET status = 'publishing', updated_at = NOW(),
			     desired_privacy = $2, publish_at = $3
			 WHERE id = $1
			   AND (
			       status IN ('editing','failed')
			       OR (
			            status = 'publishing'
			            AND updated_at < NOW() - make_interval(secs => $4)
			        )
			   )
			 RETURNING `+youtubeVideoEditSelectColumns,
			id, desiredPrivacy, publishAt, int(inFlightTimeout.Seconds()),
		).Scan(
			&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
			&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
			&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
			&edit.CreatedAt, &edit.UpdatedAt,
		)
	}
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: id=%s", ErrYouTubeVideoEditNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("youtube video edit MarkPublishing: %w", err)
	}
	return edit, nil
}

// AttachThumbnail atomically links a verified media asset (the
// rendered thumbnail) to an editor session. Single UPDATE statement
// with CAS predicate `status IN ('editing','failed')` so concurrent
// publishes cannot race the link (a session already in 'publishing' or
// 'published' state will not match — handler maps 0-rows to 409).
// Returns ErrYouTubeVideoEditNotFound (wrapped) on 0-rows match.
func (r *YouTubeVideoEditRepository) AttachThumbnail(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
	edit := &models.YouTubeVideoEdit{}
	err := r.db.QueryRowContext(ctx,
		`UPDATE youtube_video_edits
		 SET thumbnail_media_id = $2, updated_at = NOW()
		 WHERE id = $1 AND status IN ('editing','failed')
		 RETURNING `+youtubeVideoEditSelectColumns,
		sessionID, thumbnailMediaID,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
		&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
		&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
		&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
		&edit.CreatedAt, &edit.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: id=%s", ErrYouTubeVideoEditNotFound, sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("youtube video edit AttachThumbnail: %w", err)
	}
	return edit, nil
}

// MarkPublishedWithActualPrivacy (P0#7 actual_privacy read-back) is
// the atomic CAS that stamps the YouTube-side projection in the same
// SQL statement as the status='published' flip, so a concurrent
// dashboard reader cannot observe a 'published' row with stale NULL
// actual_privacy / pending youtube_sync_status.
//
// Why CAS, not a follow-up Update:
//
//	The orchestrator's previous final-step pattern (`edit.Status =
//	"published"; Update(...)`) had a subtle race: a concurrent GET on
//	the same velox_project_id could observe Status='published' but
//	ActualPrivacy=NULL (the row had not yet been written with the new
//	projection). Operators then saw "Pubblicato" badges with no
//	actual privacy colour — confusing + missed drift. The CAS below
//	guarantees the four columns (status, actual_privacy,
//	youtube_sync_status, updated_at) flip together in the same SQL,
//	so a reader either sees the row pre-CAS or post-CAS, never in
//	between.
//
// CAS predicate: status='publishing' (only). MarkPublishing was the
// gate prior to this — a row in 'editing'/'failed' must never reach
// this branch because PublishThumbnail has not successfully run yet,
// and a row in 'published' (e.g. on a replay) was caught at the
// orchestrator's idempotency guard.
//
// Returns ErrYouTubeVideoEditNotFound (wrapped) on 0-rows — the
// orchestrator maps to 409 (CAS-loss). This is distinct from a real
// Postgres error so the handlers can branch on errors.Is.
func (r *YouTubeVideoEditRepository) MarkPublishedWithActualPrivacy(
	ctx context.Context,
	id string,
	actualPrivacy string,
	syncStatus string,
) (*models.YouTubeVideoEdit, error) {
	// syncStatus is constrained at the DB layer (CHECK constraint,
	// migration 072). We do NOT re-validate here: the application
	// layer is the single source of truth for the four lifecycle
	// values; overlaying an allow-list at this layer would just
	// drift the truth away from the DB constraint.
	//
	// actualPrivacy uses NULLIF($2, '') so the orchestrator's
	// pending fallback (empty string when videos.list read-back
	// errored) collapses to a true SQL NULL on the column. This
	// preserves the schema invariant the verdict pivots on:
	//   NULL  = no read-back yet (transient state)
	//   ''    = read-back completed AND YouTube reports empty
	//            privacy (a different failure mode — emitted only
	//            if YouTube returns a malformed status payload).
	// Both keep the DTO's `omitempty` behaviour intact.
	edit := &models.YouTubeVideoEdit{}
	err := r.db.QueryRowContext(ctx,
		`UPDATE youtube_video_edits
		 SET status = 'published', updated_at = NOW(),
		     last_error = '',
		     actual_privacy = NULLIF($2, ''), youtube_sync_status = $3
		 WHERE id = $1 AND status = 'publishing'
		 RETURNING `+youtubeVideoEditSelectColumns,
		id, actualPrivacy, syncStatus,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
		&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
		&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
		&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
		&edit.CreatedAt, &edit.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: id=%s", ErrYouTubeVideoEditNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("youtube video edit MarkPublishedWithActualPrivacy: %w", err)
	}
	return edit, nil
}

// FindOrCreateEditableSession atomically returns the open (non-terminal)
// editor session for the (workspaceID, platformAccountID, youtubeVideoID)
// triple, or creates a fresh one if no such row exists.
//
// Three-step race-safe sequence:
//  1. SELECT — lookup an existing open session for the triple.
//     If a row in ('editing','failed','publishing') state is found,
//     return it WITH its existing (id, velox_project_id) untouched so
//     the SPA reuses the same Dark Editor URL across clicks.
//  2. INSERT — if no row exists, mint a fresh (session_id, velox_project_id)
//     from the hint args (or auto-generate UUIDs when the hints are
//     empty) and pin status='editing'. Single-row INSERT; the partial
//     UNIQUE INDEX `uniq_youtube_video_edits_open_session` (migration
//  071. protects against two concurrent inserts for the same triple.
//  3. ON 23505 CONFLICT — if step 2 loses the race, the winning row was
//     inserted by a peer goroutine between our SELECT and INSERT.
//     Re-SELECT the triple; the partial UNIQUE INDEX guarantees
//     exactly one row is visible; return it. The rare case where the
//     re-SELECT returns (nil, nil) — meaning the row flipped to a
//     terminal state in the tiny window between INSERT-fail and
//     SELECT — surfaces the original 23505 sentinel so the operator
//     can re-trigger the click.
//
// Args:
//   - sessionIDHint: optional pre-generated UUID for the new row's ID.
//     Empty → repo auto-generates (`uuid.NewString`). The handler always
//     supplies a hint to keep id/velox_project_id generated in the same
//     logging boundary as the request, but the repo tolerates empty.
//   - projectIDHint: optional pre-generated `ve_<uuid>` for the new row's
//     Velox project id. Empty → repo auto-generates.
//
// Edge cases:
//   - workspaceID <= 0 / platformAccountID <= 0 / empty videoID → error
//     (no SQL executed). The handler-level CreateEditorSession already
//     runs the same gate; this is defence-in-depth.
//   - found pointer's ID/velox_project_id are returned as-is. The hint
//     args are silently discarded on the SELECT path — they only take
//     effect on the INSERT path.
func (r *YouTubeVideoEditRepository) FindOrCreateEditableSession(
	ctx context.Context,
	workspaceID int64,
	platformAccountID int64,
	youtubeVideoID string,
	sessionIDHint string,
	projectIDHint string,
) (*models.YouTubeVideoEdit, error) {
	if workspaceID <= 0 || platformAccountID <= 0 || youtubeVideoID == "" {
		return nil, fmt.Errorf("youtube video edit FindOrCreateEditableSession: invalid triple (workspaceID=%d platformAccountID=%d youtubeVideoID=%q)", workspaceID, platformAccountID, youtubeVideoID)
	}

	// Step 1 — SELECT fast path: an editor session in
	// ('editing','failed','publishing') state already exists for this
	// triple. Return it so the SPA keeps the same velox_project_id.
	existing, err := r.findOpenEditableSessionByTriple(ctx, workspaceID, platformAccountID, youtubeVideoID)
	if err != nil {
		return nil, fmt.Errorf("youtube video edit FindOrCreateEditableSession lookup: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	// Step 2 — INSERT: mint a fresh (session_id, velox_project_id)
	// from the hint args or auto-generate. The partial UNIQUE INDEX
	// (migration 071) protects this INSERT from racing siblings.
	now := time.Now().UTC()
	sessionID := sessionIDHint
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	projectID := projectIDHint
	if projectID == "" {
		projectID = "ve_" + uuid.NewString()
	}
	newRow := &models.YouTubeVideoEdit{
		ID:                sessionID,
		WorkspaceID:       workspaceID,
		PlatformAccountID: platformAccountID,
		YouTubeVideoID:    youtubeVideoID,
		VeloxProjectID:    projectID,
		DesiredPrivacy:    "private",
		Status:            "editing",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := r.Create(ctx, newRow); err == nil {
		return newRow, nil
	} else if !isPQUniqueViolation(err) {
		return nil, fmt.Errorf("youtube video edit FindOrCreateEditableSession insert: %w", err)
	} else {
		// Step 3 — RACE-LOSER: 23505 means a concurrent goroutine won
		// the partial-UNIQUE contest between step 1's SELECT and our
		// step 2 INSERT. Re-SELECT to pick up the winner's row.
		winner, err2 := r.findOpenEditableSessionByTriple(ctx, workspaceID, platformAccountID, youtubeVideoID)
		if err2 != nil {
			return nil, fmt.Errorf("youtube video edit FindOrCreateEditableSession re-lookup: %w", err2)
		}
		if winner == nil {
			// Defence-in-depth: the partial UNIQUE guarantees at
			// least one open row exists for this triple the moment
			// 23505 fires. If the row flipped through 'published'
			// before our re-SELECT, surface the original error so
			// the caller can re-trigger and let the new session be
			// minted (partial UNIQUE allows re-creating after
			// 'published').
			return nil, fmt.Errorf("youtube video edit FindOrCreateEditableSession: 23505 fired but re-SELECT found no open row (original=%v)", err)
		}
		return winner, nil
	}
}

// findOpenEditableSessionByTriple is the inner SELECT used by both
// the fast path and the race-loser re-lookup of
// FindOrCreateEditableSession. Returns (nil, nil) when no open row
// exists yet for the triple — the contract every caller branches on.
//
// Index hint: the partial UNIQUE INDEX `uniq_youtube_video_edits_open_session`
// (migration 071) covers exactly the predicate below, so this
// query is always an index-only scan regardless of the workspace's
// total row count.
func (r *YouTubeVideoEditRepository) findOpenEditableSessionByTriple(
	ctx context.Context,
	workspaceID int64,
	platformAccountID int64,
	youtubeVideoID string,
) (*models.YouTubeVideoEdit, error) {
	edit := &models.YouTubeVideoEdit{}
	err := r.db.QueryRowContext(ctx,
		`SELECT `+youtubeVideoEditSelectColumns+`
		 FROM youtube_video_edits
		 WHERE workspace_id = $1
		   AND platform_account_id = $2
		   AND youtube_video_id = $3
		   AND status IN ('editing', 'failed', 'publishing')
		 LIMIT 1`,
		workspaceID, platformAccountID, youtubeVideoID,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
		&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
		&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
		&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
		&edit.CreatedAt, &edit.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("findOpenEditableSessionByTriple: %w", err)
	}
	return edit, nil
}

// isPQUniqueViolation reports whether err is a Postgres SQLSTATE 23505
// unique_violation raised by lib/pq. Centralised so the
// FindOrCreateEditableSession race-handler keeps a single source of
// truth for the violation check (and so other repository methods can
// borrow the helper when they grow the same pattern).
func isPQUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

// SaveDraft (P2 — Dark Editor auto-save) atomically writes the operator's
// mid-edit form values to youtube_video_edits.draft_* AND stamps
// dirty_flag=false AND draft_updated_at=$11 in a single SQL statement.
//
// CAS predicate: id=$1 AND status IN ('editing','failed'). The publish
// orchestrator owns the row during the 'publishing' window — racing a
// draft save against a publish would let an operator's keystrokes
// silently overwrite the privacy/title the orchestrator just pushed
// to YouTube. Once a row lands in 'published' state, the partial
// UNIQUE INDEX `uniq_youtube_video_edits_open_session` (migration
// 071) keeps 'published' rows invisible to the predicate; a re-edit
// click after a successful publish will mint a FRESH row through
// FindOrCreateEditableSession (CAS predicate matches open rows only).
//
// Returns ErrYouTubeVideoEditNotFound (wrapped) on 0-rows match — the
// handler maps to 409 (CAS-loss). A real *sql.DB error propagates
// wrapped. Idempotency: same payload, same final UPSERT, no timestamp
// drift per call (draftUpdatedAt is supplied by the handler so the
// response echo and the row stamp agree to the microsecond).
//
// draft_translations encoding (P2 architecture verdict Option A):
//   - non-nil map → json.Marshal then bind as the JSONB column;
//   - nil map → SQL NULL (lighter row than '{}', and the SPA can
//     distinguish "draft cleared translations" via the typed echo).
//
// We do NOT run YouTubePublishOptions.Validate() here — strict bounds
// live at the publish endpoint. Drafts by definition can be
// incomplete (mid-typing) or temporarily out-of-spec (the operator
// is shortening an over-long title).
func (r *YouTubeVideoEditRepository) SaveDraft(
	ctx context.Context,
	id string,
	title string,
	description string,
	tags []string,
	defaultLanguage string,
	defaultAudioLanguage string,
	translations map[string]models.YouTubeTranslation,
	desiredPrivacy string,
	publishAt *time.Time,
	draftUpdatedAt time.Time,
) error {
	// Nil → SQL NULL. Empty map → {} (so the SPA can distinguish
	// "operator just opened the editor" / "operator actively cleared").
	var translationsJSON interface{}
	if translations != nil {
		b, err := json.Marshal(translations)
		if err != nil {
			return fmt.Errorf("youtube video edit SaveDraft marshal translations: %w", err)
		}
		translationsJSON = b
	}
	// Nil tags → NULL (lighter than '{}}::text[]'). Empty
	// []string{} → '{}'::text[] so the SPA can distinguish
	// "all-tags-removed" from "no draft yet".
	var tagsVal interface{}
	if tags != nil {
		tagsVal = pq.Array(tags)
	}

	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_video_edits
		 SET draft_title = $2,
		     draft_description = $3,
		     draft_tags = $4,
		     draft_default_language = $5,
		     draft_default_audio_language = $6,
		     draft_translations = $7,
		     draft_desired_privacy = $8,
		     draft_publish_at = $9,
		     draft_updated_at = $10,
		     dirty_flag = FALSE,
		     updated_at = NOW()
		 WHERE id = $1
		   AND status IN ('editing','failed')`,
		id,
		nullableDraftString(title),
		nullableDraftString(description),
		tagsVal,
		nullableDraftString(defaultLanguage),
		nullableDraftString(defaultAudioLanguage),
		translationsJSON,
		nullableDraftString(desiredPrivacy),
		publishAt,
		draftUpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("youtube video edit SaveDraft: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("youtube video edit SaveDraft rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrYouTubeVideoEditNotFound, id)
	}
	return nil
}

// nullableDraftString binds a string column from an optional form
// value WITHOUT coercing empty-string to NULL. The P2 architect
// verdict explicitly preserves two distinct states the operator can
// produce:
//
//   - NULL    = "no draft written yet" (column never touched)
//   - ""      = "operator actively cleared this field" (column was
//     written with an empty value, distinct from no-row)
//
// Coercing empty -> nil here would collapse both into the same SQL
// NULL, removing the operator's "I cleared the title" intent from
// the read-back. We bind "" as "" so the JSON DTO renders
// `draft_title: ""` (the SPA's "Bozza salvata" indicator shows the
// field was cleared) vs `draft_title` omitted when the column is
// genuinely NULL.
//
// The function is a thin type-coercion helper (string -> any); the
// name is preserved for grep-tracking against the architect verdict.
func nullableDraftString(s string) interface{} {
	return s
}
