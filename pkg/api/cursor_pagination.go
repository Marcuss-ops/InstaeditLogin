package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListPageSize = 50
	maxListPageSize     = 100
)

var errInvalidListCursor = errors.New("invalid list cursor")

type listCursor struct {
	Version  int    `json:"v"`
	Scope    string `json:"s"`
	Context  string `json:"c,omitempty"`
	Time     string `json:"t,omitempty"`
	NullTime bool   `json:"n,omitempty"`
	ID       string `json:"i"`
}

func encodeListCursor(scope string, timestamp time.Time, id string) string {
	return encodeListCursorForContext(scope, "", timestamp, id)
}

func encodeListCursorForContext(scope, context string, timestamp time.Time, id string) string {
	cursor := listCursor{Version: 1, Scope: scope, Context: context, ID: id}
	if timestamp.IsZero() {
		cursor.NullTime = true
	} else {
		cursor.Time = timestamp.UTC().Format(time.RFC3339Nano)
	}
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeListCursor(raw, scope string) (time.Time, string, error) {
	return decodeListCursorForContext(raw, scope, "")
}

func decodeListCursorForContext(raw, scope, context string) (time.Time, string, error) {
	timestamp, id, _, err := decodeListCursorDetails(raw, scope, context)
	return timestamp, id, err
}

func decodeListCursorDetails(raw, scope, context string) (time.Time, string, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, "", false, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, "", false, fmt.Errorf("%w: encoding", errInvalidListCursor)
	}
	var cursor listCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Version != 1 || cursor.Scope != scope || cursor.ID == "" || cursor.Context != context {
		return time.Time{}, "", false, fmt.Errorf("%w: malformed token or filter scope", errInvalidListCursor)
	}
	if cursor.NullTime || cursor.Time == "" {
		return time.Time{}, cursor.ID, true, nil
	}
	timestamp, err := time.Parse(time.RFC3339Nano, cursor.Time)
	if err != nil {
		return time.Time{}, "", false, fmt.Errorf("%w: timestamp", errInvalidListCursor)
	}
	return timestamp, cursor.ID, false, nil
}

func listCursorFilterContext(q url.Values, keys ...string) string {
	selected := make(url.Values, len(keys))
	for _, key := range keys {
		if values, ok := q[key]; ok {
			selected[key] = append([]string(nil), values...)
		}
	}
	return selected.Encode()
}

func parseListPage(q url.Values) (int, string, error) {
	return parseListPageWithBounds(q, defaultListPageSize, maxListPageSize)
}

func parseCursorID(raw string) int64 {
	id, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return id
}

func parseListPageWithBounds(q url.Values, defaultLimit, maxLimit int) (limit int, cursor string, err error) {
	limit = defaultLimit
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxLimit {
			return 0, "", fmt.Errorf("limit must be between 1 and %d", maxLimit)
		}
	}
	return limit, strings.TrimSpace(q.Get("cursor")), nil
}
