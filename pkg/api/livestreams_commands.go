package api

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// livestreamFieldInput is the transport-neutral command accepted by both
// create and patch. A nil field means "leave unchanged" for PATCH; CREATE
// supplies pointers for every configurable field so the same policy applies
// defaults and validation in one place.
type livestreamFieldInput struct {
	Title             *string
	Description       *string
	PrivacyStatus     *string
	PlaybackMode      *string
	ScheduleType      *string
	ScheduledStartAt  *string
	Resolution        *string
	FrameRate         *int
	AutoRestart       *bool
	Category          *string
	MadeForKids       *bool
	Language          *string
	ThumbnailMediaID  *string
	DVREnabled        *bool
	AutoStart         *bool
	AutoStop          *bool
	LatencyPreference *string
}

func livestreamCreateFields(payload createLivestreamRequest) livestreamFieldInput {
	return livestreamFieldInput{
		Title:             &payload.Title,
		Description:       &payload.Description,
		PrivacyStatus:     &payload.PrivacyStatus,
		PlaybackMode:      &payload.PlaybackMode,
		ScheduleType:      &payload.ScheduleType,
		ScheduledStartAt:  payload.ScheduledStartAt,
		Resolution:        &payload.Resolution,
		FrameRate:         &payload.FrameRate,
		AutoRestart:       payload.AutoRestart,
		Category:          &payload.Category,
		MadeForKids:       payload.MadeForKids,
		Language:          &payload.Language,
		ThumbnailMediaID:  payload.ThumbnailMediaID,
		DVREnabled:        payload.DVREnabled,
		AutoStart:         payload.AutoStart,
		AutoStop:          payload.AutoStop,
		LatencyPreference: &payload.LatencyPreference,
	}
}

func livestreamPatchFields(payload patchLivestreamRequest) livestreamFieldInput {
	return livestreamFieldInput{
		Title:             payload.Title,
		Description:       payload.Description,
		PrivacyStatus:     payload.PrivacyStatus,
		PlaybackMode:      payload.PlaybackMode,
		ScheduleType:      payload.ScheduleType,
		ScheduledStartAt:  payload.ScheduledStartAt,
		Resolution:        payload.Resolution,
		FrameRate:         payload.FrameRate,
		AutoRestart:       payload.AutoRestart,
		Category:          payload.Category,
		MadeForKids:       payload.MadeForKids,
		Language:          payload.Language,
		ThumbnailMediaID:  payload.ThumbnailMediaID,
		DVREnabled:        payload.DVREnabled,
		AutoStart:         payload.AutoStart,
		AutoStop:          payload.AutoStop,
		LatencyPreference: payload.LatencyPreference,
	}
}

// applyLivestreamFields is the command policy for configurable livestream
// metadata. It deliberately mutates only fields present in input, then
// validates the resulting schedule invariant. Keeping this policy outside
// HTTP handlers prevents CREATE and PATCH from drifting apart.
func applyLivestreamFields(ls *models.Livestream, input livestreamFieldInput) error {
	if ls == nil {
		return errors.New("livestream is required")
	}
	if input.Title != nil {
		value, err := normalizeLivestreamTitle(*input.Title)
		if err != nil {
			return err
		}
		ls.Title = value
	}
	if input.Description != nil {
		value, err := normalizeLivestreamDescription(*input.Description)
		if err != nil {
			return err
		}
		ls.Description = value
	}
	if input.PrivacyStatus != nil {
		value, err := validateLivestreamPrivacy(*input.PrivacyStatus)
		if err != nil {
			return err
		}
		ls.PrivacyStatus = value
	}
	if input.PlaybackMode != nil {
		value, err := validateLivestreamPlaybackMode(*input.PlaybackMode)
		if err != nil {
			return err
		}
		ls.PlaybackMode = value
	}
	if input.ScheduleType != nil {
		value, err := validateLivestreamScheduleType(*input.ScheduleType)
		if err != nil {
			return err
		}
		ls.ScheduleType = value
	}
	if input.ScheduledStartAt != nil {
		value, err := parseOptionalRFC3339(input.ScheduledStartAt)
		if err != nil {
			return err
		}
		ls.ScheduledStartAt = value
	}
	if input.Resolution != nil {
		value, err := validateLivestreamResolution(*input.Resolution)
		if err != nil {
			return err
		}
		ls.Resolution = value
	}
	if input.FrameRate != nil {
		value, err := validateLivestreamFrameRate(*input.FrameRate)
		if err != nil {
			return err
		}
		ls.FrameRate = value
	}
	if input.AutoRestart != nil {
		ls.AutoRestart = *input.AutoRestart
	}
	if input.Category != nil {
		value, err := validateLivestreamCategory(*input.Category)
		if err != nil {
			return err
		}
		ls.Category = value
	}
	if input.MadeForKids != nil {
		ls.MadeForKids = *input.MadeForKids
	}
	if input.Language != nil {
		value, err := validateLivestreamLanguage(*input.Language)
		if err != nil {
			return err
		}
		ls.Language = value
	}
	if input.ThumbnailMediaID != nil {
		value, err := normalizeLivestreamThumbnail(input.ThumbnailMediaID)
		if err != nil {
			return err
		}
		ls.ThumbnailMediaID = value
	}
	if input.DVREnabled != nil {
		ls.DVREnabled = *input.DVREnabled
	}
	if input.AutoStart != nil {
		ls.AutoStart = *input.AutoStart
	}
	if input.AutoStop != nil {
		ls.AutoStop = *input.AutoStop
	}
	if input.LatencyPreference != nil {
		value, err := validateLivestreamLatency(*input.LatencyPreference)
		if err != nil {
			return err
		}
		ls.LatencyPreference = value
	}
	if ls.ScheduleType == models.LivestreamScheduleScheduled && ls.ScheduledStartAt == nil {
		return errors.New("scheduled_start_at is required when schedule_type is scheduled")
	}
	return nil
}

func normalizeLivestreamTitle(s string) (string, error) {
	title := strings.TrimSpace(s)
	if title == "" {
		return "", errors.New("title is required")
	}
	if utf8.RuneCountInString(title) > models.LivestreamTitleMaxRunes {
		return "", fmt.Errorf("title must be at most %d characters", models.LivestreamTitleMaxRunes)
	}
	return title, nil
}

func normalizeLivestreamDescription(s string) (string, error) {
	if utf8.RuneCountInString(s) > models.LivestreamDescriptionMaxRunes {
		return "", fmt.Errorf("description must be at most %d characters", models.LivestreamDescriptionMaxRunes)
	}
	return s, nil
}

func validateLivestreamPrivacy(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch s {
	case models.LivestreamPrivacyPrivate, models.LivestreamPrivacyUnlisted, models.LivestreamPrivacyPublic:
		return s, nil
	default:
		return "", fmt.Errorf("privacy_status must be one of private, unlisted, public")
	}
}

func validateLivestreamPlaybackMode(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch s {
	case models.LivestreamPlaybackLoopContinuous, models.LivestreamPlaybackPlayOnce:
		return s, nil
	default:
		return "", fmt.Errorf("playback_mode must be one of loop_continuous, play_once")
	}
}

func validateLivestreamScheduleType(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch s {
	case models.LivestreamScheduleManual, models.LivestreamScheduleNow,
		models.LivestreamScheduleScheduled, models.LivestreamScheduleRecurring:
		return s, nil
	default:
		return "", fmt.Errorf("schedule_type must be one of manual, now, scheduled, recurring")
	}
}

func validateLivestreamResolution(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch s {
	case "":
		return models.LivestreamResolution1080p, nil
	case models.LivestreamResolution720p, models.LivestreamResolution1080p:
		return s, nil
	default:
		return "", fmt.Errorf("resolution must be one of 720p30, 1080p30")
	}
}

func validateLivestreamFrameRate(n int) (int, error) {
	if n == 0 {
		return models.LivestreamFrameRate, nil
	}
	if n != models.LivestreamFrameRate {
		return 0, fmt.Errorf("frame_rate must be %d", models.LivestreamFrameRate)
	}
	return n, nil
}

func parseOptionalRFC3339(s *string) (*time.Time, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*s))
	if err != nil {
		return nil, fmt.Errorf("scheduled_start_at must be an RFC3339 timestamp: %w", err)
	}
	return &t, nil
}

func validateLivestreamCategory(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if !models.ValidLivestreamCategory(s) {
		return "", fmt.Errorf("category must be a known YouTube category id (or empty)")
	}
	return s, nil
}

func validateLivestreamLanguage(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if utf8.RuneCountInString(s) > models.LivestreamLanguageMaxRunes {
		return "", fmt.Errorf("language must be at most %d characters", models.LivestreamLanguageMaxRunes)
	}
	if err := models.CheckBCP47Like("language", s); err != nil {
		return "", err
	}
	return s, nil
}

func validateLivestreamLatency(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return models.LivestreamLatencyNormal, nil
	}
	if !models.ValidLivestreamLatency(s) {
		return "", fmt.Errorf("latency_preference must be one of normal, low, ultraLow")
	}
	return s, nil
}

func normalizeLivestreamThumbnail(s *string) (*string, error) {
	if s == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(trimmed) > 64 {
		return nil, errors.New("thumbnail_media_id is too long")
	}
	return &trimmed, nil
}
