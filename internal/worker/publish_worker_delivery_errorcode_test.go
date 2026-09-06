package worker

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestDeliveryErrorCode_TypedClassification pins the typed classifier
// contract that replaced substring matching over err.Error(): codes are
// derived from the ERR_DRIVE_* sentinels and the services.DeliveryError
// carrier only. Byte-compatibility with the historical codes is asserted
// so operator dashboards keep their series.
func TestDeliveryErrorCode_TypedClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil",
			err:  nil,
			want: "",
		},
		{
			name: "drive sentinels classify through arbitrary wrapping",
			err:  fmt.Errorf("deliver: %w", services.ErrDriveSessionExpired),
			want: "ERR_DRIVE_SESSION_EXPIRED",
		},
		{
			name: "drive config sentinel",
			err:  fmt.Errorf("deliver: %w", services.ErrDriveConfig),
			want: "ERR_DRIVE_CONFIG",
		},
		{
			name: "upstream HTTP status carried by the carrier",
			err:  fmt.Errorf("GoogleDriveDestination.Deliver: %w", &services.DeliveryError{Status: 429, Err: errors.New("rate limited")}),
			want: "HTTP_429",
		},
		{
			name: "pipeline stage carried by the carrier",
			err:  fmt.Errorf("GoogleDriveDestination.Deliver: %w", &services.DeliveryError{Stage: "sessionStore.Create", Err: errors.New("db down")}),
			want: "SESSIONSTORE.CREATE", // legacy codes preserved dots (only spaces → underscores)
		},
		{
			name: "stage+status carrier: HTTP wins (historical precedence)",
			err:  &services.DeliveryError{Stage: "source Range GET", Status: 500, Err: errors.New("boom")},
			want: "HTTP_500",
		},
		{
			name: "outermost stage wins over inner stage (historical specificity order)",
			err:  &services.DeliveryError{Stage: "sessionStore", Err: &services.DeliveryError{Stage: "sessionStore.MarkCompleted", Err: errors.New("cas")}},
			want: "SESSIONSTORE",
		},
		{
			name: "HTTP status found deep in the chain",
			err:  fmt.Errorf("outer: %w", fmt.Errorf("mid: %w", &services.DeliveryError{Status: 403, Err: errors.New("forbidden")})),
			want: "HTTP_403",
		},
		{
			name: "untyped error falls back to the stable bucket",
			err:  errors.New("something unexpected happened"),
			want: "DELIVERY_ERROR",
		},
		{
			name: "provider text mentioning 'returned 500' must NOT classify by string",
			err:  errors.New("upstream said: request returned 500 in body"),
			want: "DELIVERY_ERROR",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deliveryErrorCode(tc.err); got != tc.want {
				t.Fatalf("deliveryErrorCode(%v): want %q, got %q", tc.err, tc.want, got)
			}
		})
	}
}
