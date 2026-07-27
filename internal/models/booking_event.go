package models

import "time"

// BookingEvent is one row in booking_events, mirroring migration 076.
//
// The intent / goal / budget / ready fields are all closed sets
// (see web/src/lib/booking.ts). The constants below are the
// server-side mirror: the handler validates that the JSON payload
// matches one of the canonical values and rejects unknown values
// with 400. Adding a new enum value requires updating BOTH the
// web BookingProvider options array AND the corresponding constant
// in this file so a typo in either side surfaces immediately.
type BookingEvent struct {
	ID         int64     `json:"id"`
	Intent     string    `json:"intent"`
	Goal       string    `json:"goal"`
	Budget     string    `json:"budget"`
	Ready      string    `json:"ready"`
	IPHash     string    `json:"ip_hash,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	Referer    string    `json:"referer,omitempty"`
	DedupeHash string    `json:"dedupe_hash,omitempty"`
	// Metadata is a free-form map persisted into the JSONB column
	// introduced by migration 076. The SPA submits marketing tags
	// (utm_source / utm_campaign / etc.) here as a fallback when the
	// upstream scheduler's redirect chain strips the query string
	// — verified empirically for Google Appointment Schedules
	// (`calendar.app.google/<id>` drops every `?…` on the 302 hop to
	// `calendar.google.com/calendar/appointments/schedules/<id>`).
	// The repository JSON-marshals the map; an empty map is
	// COALESCE'd to '{}'::jsonb on the SQL side, so omitting the key
	// is equivalent to passing nil.
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// Intent constants mirror web/src/lib/booking.ts → BookingIntent.
const (
	BookingIntentStarter = "starter"
	BookingIntentGrowth  = "growth"
	BookingIntentPremium = "premium"
	BookingIntentGeneral = "general"
)

// Goal constants mirror web/src/lib/booking.ts → BookingGoal.
const (
	BookingGoalLaunch     = "launch"
	BookingGoalScale      = "scale"
	BookingGoalAutomated  = "automated"
)

// Budget constants mirror web/src/lib/booking.ts → BookingBudget.
const (
	BookingBudgetStarter = "starter"
	BookingBudgetBase    = "base"
	BookingBudgetPremium = "premium"
)

// Ready constants mirror web/src/lib/booking.ts → BookingReady.
const (
	BookingReadyYes = "yes"
	BookingReadyNo  = "no"
)

// validBookingIntents / validBookingGoals / validBookingBudgets /
// validBookingReadys are closed-set membership tables used by the
// handler to reject malformed JSON payloads. Keeping these as
// package-level vars (not as exported constants) avoids exposing
// them to consumers outside the model package; the handler in
// pkg/api/booking_events.go holds its own copies too so a future
// refactor that splits validation out can move these freely.
var (
	validBookingIntents = map[string]struct{}{
		BookingIntentStarter: {},
		BookingIntentGrowth:  {},
		BookingIntentPremium: {},
		BookingIntentGeneral: {},
	}
	validBookingGoals = map[string]struct{}{
		BookingGoalLaunch:    {},
		BookingGoalScale:     {},
		BookingGoalAutomated: {},
	}
	validBookingBudgets = map[string]struct{}{
		BookingBudgetStarter: {},
		BookingBudgetBase:    {},
		BookingBudgetPremium: {},
	}
	validBookingReadys = map[string]struct{}{
		BookingReadyYes: {},
		BookingReadyNo:  {},
	}
)

// ValidBookingIntent reports whether the supplied intent string is
// one of the canonical (closed-set) values. Exported so handlers
// outside the model package can validate without re-listing.
func ValidBookingIntent(v string) bool {
	_, ok := validBookingIntents[v]
	return ok
}

// ValidBookingGoal reports whether the supplied goal string is one
// of the canonical values.
func ValidBookingGoal(v string) bool {
	_, ok := validBookingGoals[v]
	return ok
}

// ValidBookingBudget reports whether the supplied budget string is
// one of the canonical values.
func ValidBookingBudget(v string) bool {
	_, ok := validBookingBudgets[v]
	return ok
}

// ValidBookingReady reports whether the supplied ready string is
// one of the canonical values.
func ValidBookingReady(v string) bool {
	_, ok := validBookingReadys[v]
	return ok
}
