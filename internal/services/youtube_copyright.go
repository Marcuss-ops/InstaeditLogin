package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// YouTubeCopyrightChecker is deliberately separate from AsyncPublisher:
// copyright checks are a read-only, post-upload YouTube capability.
type YouTubeCopyrightChecker interface {
	CheckCopyright(ctx context.Context, accessToken, videoID string) (*YouTubeCopyrightCheck, error)
}

type YouTubeCopyrightCheck struct {
	Status           string
	Message          string
	ProcessingStatus string
	RejectionReason  string
	FailureReason    string
	LicensedContent  bool
	BlockedRegions   []string
	AllowedRegions   []string
}

func (s *YouTubeOAuthService) CheckCopyright(ctx context.Context, accessToken, videoID string) (*YouTubeCopyrightCheck, error) {
	video, err := s.fetchCopyrightStatus(ctx, accessToken, videoID)
	if err != nil {
		return nil, err
	}

	check := &YouTubeCopyrightCheck{
		Status:          "clear",
		Message:         "Nessun problema copyright rilevato dai controlli YouTube.",
		RejectionReason: video.Status.RejectionReason,
		FailureReason:   video.Status.FailureReason,
		LicensedContent: video.ContentDetails.LicensedContent,
	}
	if video.ProcessingDetails != nil {
		check.ProcessingStatus = video.ProcessingDetails.ProcessingStatus
	}
	if video.ContentDetails.RegionRestriction != nil {
		check.BlockedRegions = append([]string(nil), video.ContentDetails.RegionRestriction.Blocked...)
		check.AllowedRegions = append([]string(nil), video.ContentDetails.RegionRestriction.Allowed...)
	}

	if check.RejectionReason == "claim" || check.RejectionReason == "copyright" {
		check.Status = "claim"
		check.Message = "YouTube ha rilevato una rivendicazione o un rifiuto copyright."
	} else if len(check.BlockedRegions) > 0 || len(check.AllowedRegions) > 0 {
		check.Status = "blocked"
		check.Message = "Il video ha restrizioni geografiche su YouTube."
	} else if check.LicensedContent {
		check.Status = "claim"
		check.Message = "YouTube segnala contenuto concesso o rivendicato da un partner."
	} else if check.ProcessingStatus != "" && check.ProcessingStatus != "succeeded" {
		check.Status = "processing"
		check.Message = "YouTube sta ancora elaborando il video."
	} else if check.FailureReason != "" {
		check.Status = "error"
		check.Message = "YouTube ha segnalato un errore durante l'elaborazione del video."
	}
	return check, nil
}

func (s *YouTubeOAuthService) fetchCopyrightStatus(ctx context.Context, accessToken, videoID string) (*youtubeVideo, error) {
	reqURL := "https://www.googleapis.com/youtube/v3/videos?part=status,processingDetails,contentDetails&id=" + url.QueryEscape(videoID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create youtube copyright request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube copyright request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("youtube copyright request returned %d", resp.StatusCode)
	}
	var result youtubeVideosResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode youtube copyright response: %w", err)
	}
	if len(result.Items) == 0 {
		return nil, fmt.Errorf("youtube video %s not found", videoID)
	}
	return &result.Items[0], nil
}
