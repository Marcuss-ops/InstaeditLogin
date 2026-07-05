package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
)

// BackfillYouTubeRefreshTokens migrates YouTube refresh tokens that the old
// version of the service stored inside the scopes[] column as entries
// starting with "refresh_token:". After this runs once, the new
// encrypted_refresh_token column is populated and the legacy scope entries
// are stripped out.
//
// The migration is idempotent: it only touches rows where
// encrypted_refresh_token IS NULL AND a refresh_token scope exists.
// Failed encryptions abort the batch via tx rollback so partial state is
// avoided.
func BackfillYouTubeRefreshTokens(db *sql.DB, encryptor *crypto.Encryptor) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("backfill begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	rows, err := tx.Query(
		`SELECT t.id, t.scopes
		 FROM tokens t
		 JOIN platform_accounts pa ON pa.id = t.platform_account_id
		 WHERE pa.platform = 'youtube'
		   AND t.encrypted_refresh_token IS NULL
		   AND t.scopes IS NOT NULL
		 FOR UPDATE OF t`,
	)
	if err != nil {
		return fmt.Errorf("backfill query failed: %w", err)
	}
	defer rows.Close()

	const refreshPrefix = "refresh_token:"

	type pending struct {
		id          int64
		cipher      []byte
		legacyScope string
	}

	var batch []pending
	scanned := 0
	for rows.Next() {
		var id int64
		var scopes []string
		if err = rows.Scan(&id, &scopes); err != nil {
			rows.Close()
			return fmt.Errorf("backfill scan failed: %w", err)
		}
		scanned++

		var refresh string
		for _, s := range scopes {
			if strings.HasPrefix(s, refreshPrefix) {
				refresh = strings.TrimPrefix(s, refreshPrefix)
				break
			}
		}
		if refresh == "" {
			continue
		}

		cipher, encErr := encryptor.Encrypt(refresh)
		if encErr != nil {
			rows.Close()
			return fmt.Errorf("backfill encrypt (id=%d): %w", id, encErr)
		}
		batch = append(batch, pending{
			id:          id,
			cipher:      cipher,
			legacyScope: refreshPrefix + refresh,
		})
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("backfill rows iteration: %w", err)
	}
	rows.Close()

	if scanned == 0 {
		// No candidates; rollback the empty tx and exit cleanly (no rows changed).
		if err = tx.Rollback(); err != nil && err != sql.ErrTxDone {
			return fmt.Errorf("backfill rollback: %w", err)
		}
		slog.Info("YouTube refresh-token backfill: nothing to do")
		return nil
	}

	for _, p := range batch {
		if _, err = tx.Exec(
			`UPDATE tokens
			   SET encrypted_refresh_token = $1,
			       scopes = array_remove(scopes, $2)
			 WHERE id = $3`,
			p.cipher, p.legacyScope, p.id,
		); err != nil {
			return fmt.Errorf("backfill update (id=%d): %w", p.id, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("backfill commit: %w", err)
	}
	slog.Info("YouTube refresh-token backfill complete", "scanned", scanned, "migrated", len(batch))
	return nil
}
