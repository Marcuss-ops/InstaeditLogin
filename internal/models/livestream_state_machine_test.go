package models

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestLivestreamDesiredTransitions(t *testing.T) {
	valid := [][2]LivestreamDesiredState{
		{LivestreamDesiredDraft, LivestreamDesiredPrepared},
		{LivestreamDesiredPrepared, LivestreamDesiredRunning},
		{LivestreamDesiredRunning, LivestreamDesiredStopped},
		{LivestreamDesiredStopped, LivestreamDesiredRunning},
		{LivestreamDesiredRunning, LivestreamDesiredCancelled},
	}
	for _, edge := range valid {
		if !CanTransitionDesiredState(edge[0], edge[1]) {
			t.Errorf("expected desired transition %q -> %q", edge[0], edge[1])
		}
	}
	invalid := [][2]LivestreamDesiredState{
		{LivestreamDesiredCancelled, LivestreamDesiredRunning},
		{LivestreamDesiredRunning, LivestreamDesiredPrepared},
		{"invalid", LivestreamDesiredRunning},
	}
	for _, edge := range invalid {
		if CanTransitionDesiredState(edge[0], edge[1]) {
			t.Errorf("did not expect desired transition %q -> %q", edge[0], edge[1])
		}
	}
}

func TestLivestreamActualTransitions(t *testing.T) {
	valid := [][2]LivestreamActualState{
		{LivestreamActualDraft, LivestreamActualPreflighting},
		{LivestreamActualPreflighting, LivestreamActualPreparing},
		{LivestreamActualPreparing, LivestreamActualReady},
		{LivestreamActualStarting, LivestreamActualWaitingForIngest},
		{LivestreamActualTesting, LivestreamActualLive},
		{LivestreamActualLive, LivestreamActualDegraded},
		{LivestreamActualDegraded, LivestreamActualLive},
		{LivestreamActualReconnecting, LivestreamActualLive},
		{LivestreamActualStopping, LivestreamActualCompleted},
	}
	for _, edge := range valid {
		if !CanTransitionActualState(edge[0], edge[1]) {
			t.Errorf("expected actual transition %q -> %q", edge[0], edge[1])
		}
	}
	if CanTransitionActualState(LivestreamActualCompleted, LivestreamActualLive) {
		t.Fatal("completed run must not return to live")
	}
	if CanTransitionActualState(LivestreamActualLive, LivestreamActualPreparing) {
		t.Fatal("live run must not jump back to preparing")
	}
}

func TestValidateLivestreamEvent(t *testing.T) {
	event := &LivestreamEvent{
		LivestreamID: "live-1",
		EventType:    LivestreamEventBroadcastLive,
		Severity:     "info",
		Payload:      json.RawMessage(`{"state":"live"}`),
	}
	if err := ValidateLivestreamEvent(event); err != nil {
		t.Fatalf("ValidateLivestreamEvent: %v", err)
	}
	for _, bad := range []*LivestreamEvent{
		{LivestreamID: "live-1", EventType: LivestreamEventRunCreated, Severity: "secret"},
		{LivestreamID: "live-1", EventType: LivestreamEventRunCreated, Severity: "info", Payload: json.RawMessage(`[]`)},
	} {
		if err := ValidateLivestreamEvent(bad); err == nil {
			t.Errorf("expected invalid event error for %+v", bad)
		}
	}
	if errors.Is(nil, ErrInvalidLivestreamActualTransition) {
		t.Fatal("sentinel unexpectedly matched nil")
	}
}
