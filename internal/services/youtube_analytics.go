package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// youtubeAnalyticsReportResponse mirrors the JSON shape returned by
// YouTube Analytics API v2 reports.query. The API returns columns in
// the order requested (dimensions first, then metrics), so we rely on
// the header names rather than positional indexing when possible.
type youtubeAnalyticsReportResponse struct {
	Kind          string `json:"kind"`
	ColumnHeaders []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"columnHeaders"`
	Rows [][]any `json:"rows"`
}

// ErrYouTubeEarningsNotAvailable is returned when the channel is not
// monetized, the token lacks the monetary scope, or the analytics
// data is otherwise unavailable. Callers should treat this as a soft
// failure and skip the earnings enrichment rather than fail the sync.
type ErrYouTubeEarningsNotAvailable struct {
	Reason string
}

func (e *ErrYouTubeEarningsNotAvailable) Error() string {
	return fmt.Sprintf("youtube earnings not available: %s", e.Reason)
}

// FetchEarnings queries the YouTube Analytics API earnings report for
// the given channel and date range. It returns one AccountMetricPoint
// per day with RevenueCents, RPMCents and CPMCents populated.
//
// The returned date is normalised to UTC midnight. If the channel is
// not monetized, the API call is skipped gracefully and an empty slice
// is returned.
func (s *YouTubeOAuthService) FetchEarnings(
	ctx context.Context,
	accessToken string,
	channelID string,
	days int,
) ([]repository.AccountMetricPoint, error) {
	if channelID == "" {
		return nil, &ErrYouTubeEarningsNotAvailable{Reason: "empty channel id"}
	}
	if days <= 0 {
		days = 30
	}

	endDate := time.Now().UTC()
	startDate := endDate.AddDate(0, 0, -days+1)

	q := url.Values{}
	q.Set("ids", "channel=="+channelID)
	q.Set("startDate", startDate.Format("2006-01-02"))
	q.Set("endDate", endDate.Format("2006-01-02"))
	q.Set("dimensions", "day")
	q.Set("metrics", "estimatedRevenue,cpm,views")
	q.Set("currency", "USD")

	reqURL := "https://youtubeanalytics.googleapis.com/v2/reports?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("youtube analytics earnings: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube analytics earnings: request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode != http.StatusOK {
		// Soft-fail common non-monetized / permission cases so a single
		// channel without monetization does not break the whole sync flow.
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusBadRequest {
			return nil, &ErrYouTubeEarningsNotAvailable{
				Reason: fmt.Sprintf("analytics returned %d: %s", resp.StatusCode, string(body)),
			}
		}
		return nil, fmt.Errorf("youtube analytics earnings returned %d: %s", resp.StatusCode, string(body))
	}

	var report youtubeAnalyticsReportResponse
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, fmt.Errorf("youtube analytics earnings: decode: %w", err)
	}

	// Map column name -> index so the helper is independent of the
	// requested metric order.
	colIdx := make(map[string]int, len(report.ColumnHeaders))
	for i, h := range report.ColumnHeaders {
		colIdx[h.Name] = i
	}
	dayIdx, hasDay := colIdx["day"]
	revenueIdx, hasRevenue := colIdx["estimatedRevenue"]
	cpmIdx, hasCPM := colIdx["cpm"]
	viewsIdx, hasViews := colIdx["views"]

	if !hasDay || !hasRevenue || !hasCPM || !hasViews {
		return nil, &ErrYouTubeEarningsNotAvailable{Reason: "unexpected column layout"}
	}

	points := make([]repository.AccountMetricPoint, 0, len(report.Rows))
	for _, row := range report.Rows {
		if len(row) <= dayIdx || len(row) <= revenueIdx || len(row) <= cpmIdx || len(row) <= viewsIdx {
			continue
		}

		dayStr, ok := row[dayIdx].(string)
		if !ok {
			continue
		}
		date, err := time.Parse("2006-01-02", dayStr)
		if err != nil {
			continue
		}

		revenue := asFloat64(row[revenueIdx])
		cpm := asFloat64(row[cpmIdx])
		views := asFloat64(row[viewsIdx])

		revenueCents := int64(math.Round(revenue * 100))
		cpmCents := int64(math.Round(cpm * 100))

		var rpmCents *int64
		if views > 0 {
			rpm := revenue / views * 1000
			rpmCentsVal := int64(math.Round(rpm * 100))
			rpmCents = &rpmCentsVal
		}

		point := repository.AccountMetricPoint{
			Date:         date.UTC(),
			RevenueCents: &revenueCents,
			CPMCents:     &cpmCents,
		}
		if rpmCents != nil {
			point.RPMCents = rpmCents
		}

		points = append(points, point)
	}

	return points, nil
}

// asFloat64 coerces a JSON-unmarshaled number (float64) or a string
// containing a number into a float64. Non-numeric values return 0.
func asFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		var f float64
		// Ignore parse errors; caller treats 0 as no data.
		_, _ = fmt.Sscanf(n, "%f", &f)
		return f
	}
	return 0
}
