package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func (r *Router) fetchAccountEditableVideos(ctx context.Context, acc *models.PlatformAccount, maxItems int) ([]models.YouTubeVideoDetails, error) {
	if r.vault == nil {
		return nil, fmt.Errorf("vault not configured")
	}
	if r.youTubeSvc == nil {
		return nil, fmt.Errorf("youtube service not configured")
	}
	if maxItems <= 0 {
		maxItems = groupYouTubeVideosMaxTotalVideos
	}
	token, err := r.vault.Renew(ctx, acc.ID, models.TokenTypeBearer, r.youTubeSvc.RefreshOAuthToken)
	if err != nil {
		return nil, fmt.Errorf("no valid token: %w", err)
	}
	items := make([]models.YouTubeVideoDetails, 0, maxItems)
	pageToken := ""
	for len(items) < maxItems {
		page, listErr := r.youTubeSvc.ListEditableVideos(ctx, token.AccessToken, acc.PlatformUserID, pageToken)
		if listErr != nil {
			return nil, fmt.Errorf("youtube list: %w", listErr)
		}
		if page == nil {
			return nil, errors.New("youtube list: empty page")
		}
		remaining := maxItems - len(items)
		if len(page.Items) > remaining {
			items = append(items, page.Items[:remaining]...)
		} else {
			items = append(items, page.Items...)
		}
		next := strings.TrimSpace(page.NextPageToken)
		if next == "" || next == pageToken || len(items) >= maxItems {
			break
		}
		pageToken = next
	}
	return items, nil
}
