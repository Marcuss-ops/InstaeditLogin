// Command repair-post-aggregate repairs one post's aggregate status from its
// complete post_target set. It is intended for a controlled operator run when
// a known YouTube publication has left its parent status stale.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

var errYouTubePublicationNotFound = errors.New("youtube publication not found")
var errPostTargetAssociationNotFound = errors.New("post target association not found")

func repairOutcome(err error) string {
	if errors.Is(err, errYouTubePublicationNotFound) || errors.Is(err, errPostTargetAssociationNotFound) {
		return "not_found"
	}
	if err != nil {
		return "operation_failed"
	}
	return "repaired"
}

func main() {
	videoID := flag.String("youtube-video-id", "", "YouTube video ID whose parent aggregate should be repaired (required)")
	flag.Parse()
	if *videoID == "" {
		fmt.Fprintln(os.Stderr, "usage: repair-post-aggregate --youtube-video-id <id>")
		os.Exit(2)
	}

	if err := run(*videoID); err != nil {
		if repairOutcome(err) == "not_found" {
			// A missing association is a safe, successful no-op: there is
			// no post target to repair. Keep the outcome explicit without
			// logging wrapped DB/configuration details.
			slog.Info("post aggregate repair skipped", "outcome", "not_found")
			return
		}
		// Never log the wrapped error: config/database errors can contain
		// deployment-specific connection details. The operator gets a
		// stable, secret-free outcome while the process still exits non-zero.
		slog.Error("post aggregate repair failed", "error_class", repairOutcome(err))
		os.Exit(1)
	}
}

func run(videoID string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := database.Connect(&cfg.Database)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer db.Close()
	if err := database.VerifyInstallationIdentity(context.Background(), db, cfg.Database.ExpectedInstallationUUID); err != nil {
		return fmt.Errorf("database identity verification failed: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	youtubePubs := repository.NewYouTubeTargetPublicationRepository(db)
	pub, err := youtubePubs.FindByYouTubeVideoID(ctx, videoID)
	if err != nil {
		return fmt.Errorf("lookup YouTube publication: %w", err)
	}
	if pub == nil {
		return errYouTubePublicationNotFound
	}

	postTargets := repository.NewPostRepository(db)
	target, err := postTargets.FindTargetByID(pub.PostTargetID)
	if err != nil {
		return fmt.Errorf("lookup post target: %w", err)
	}
	if target == nil {
		return errPostTargetAssociationNotFound
	}

	oldStatus, newStatus, changed, err := postTargets.RepairAggregateStatusForPost(target.PostID)
	if err != nil {
		return fmt.Errorf("repair post aggregate: %w", err)
	}

	// Safe operational breadcrumb: IDs and lifecycle states only.
	slog.Info("post aggregate repair complete",
		"youtube_video_id", videoID,
		"post_target_id", pub.PostTargetID,
		"post_id", target.PostID,
		"target_status", target.Status,
		"previous_post_status", oldStatus,
		"resolved_post_status", newStatus,
		"changed", changed,
	)
	return nil
}
