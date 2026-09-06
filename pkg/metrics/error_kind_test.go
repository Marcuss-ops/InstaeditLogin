package metrics

import (
	"errors"
	"fmt"
	"testing"
)

// carrierStub is a test double implementing ErrorKindCarrier.
type carrierStub struct{ name string }

func (c carrierStub) Error() string         { return "stub carrier: " + c.name }
func (c carrierStub) ErrorKindName() string { return c.name }

// TestErrorKind_TypedCarrierFirst pins the resolution order: the outermost
// ErrorKindCarrier in the wrap chain wins over the legacy substring
// heuristic; unknown carrier names are defensively bucketed to internal so
// no carrier can inject arbitrary error_kind label values; plain untyped
// errors still fall through to the heuristic.
func TestErrorKind_TypedCarrierFirst(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"outermost carrier wins over inner text", fmt.Errorf("wrapped: %w", carrierStub{"api"}), "api"},
		{"auth carrier", carrierStub{"auth"}, "auth"},
		{"network carrier", carrierStub{"network"}, "network"},
		{"internal carrier", carrierStub{"internal"}, "internal"},
		{"unknown carrier name is defensively re-bucketed", carrierStub{"weird"}, "internal"},
		{
			name: "typed 401-shaped text without carrier still heuristic-auth",
			err:  errors.New("request failed with status 401"),
			want: ErrKindAuth,
		},
		{
			name: "carrier named api beats text mentioning 401",
			err:  fmt.Errorf("outer says 401: %w", carrierStub{"api"}),
			want: "api",
		},
		{
			name: "untyped non-matching text falls to internal",
			err:  errors.New("something entirely different"),
			want: ErrKindInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ErrorKind(tc.err); got != tc.want {
				t.Fatalf("ErrorKind(%v): want %q, got %q", tc.err, tc.want, got)
			}
		})
	}
}
