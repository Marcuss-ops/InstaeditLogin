package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestRepairOutcome(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "repaired", want: "repaired"},
		{name: "youtube publication missing", err: errYouTubePublicationNotFound, want: "not_found"},
		{name: "post target association missing", err: errPostTargetAssociationNotFound, want: "not_found"},
		{name: "wrapped missing publication", err: fmt.Errorf("lookup: %w", errYouTubePublicationNotFound), want: "not_found"},
		{name: "operational failure", err: errors.New("database unavailable"), want: "operation_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repairOutcome(tt.err); got != tt.want {
				t.Fatalf("repairOutcome(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
