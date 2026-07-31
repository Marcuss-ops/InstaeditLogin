package models

import "testing"

func TestPostAggregateStatusResolver(t *testing.T) {
	resolver := NewPostAggregateStatusResolver()

	tests := []struct {
		name    string
		targets []PostTarget
		want    PostStatus
	}{
		{"empty is draft", nil, PostStatusDraft},
		{"all draft", targets(PostStatusDraft, PostStatusDraft), PostStatusDraft},
		{"queued", targets(PostStatusQueued), PostStatusQueued},
		{"queued beats draft", targets(PostStatusDraft, PostStatusQueued), PostStatusQueued},
		{"waiting provider", targets(PostStatusWaitingProvider), PostStatusWaitingProvider},
		{"retrying beats waiting", targets(PostStatusWaitingProvider, PostStatusRetrying), PostStatusRetrying},
		{"publishing beats retrying", targets(PostStatusRetrying, PostStatusPublishing), PostStatusPublishing},
		{"published only", targets(PostStatusPublished, PostStatusPublished), PostStatusPublished},
		{"publishing beats published", targets(PostStatusPublished, PostStatusPublishing), PostStatusPublishing},
		{"retrying beats published", targets(PostStatusPublished, PostStatusRetrying), PostStatusRetrying},
		{"waiting beats published", targets(PostStatusPublished, PostStatusWaitingProvider), PostStatusWaitingProvider},
		{"queued beats published", targets(PostStatusPublished, PostStatusQueued), PostStatusQueued},
		{"partial after terminal failure", targets(PostStatusPublished, PostStatusFailed), PostStatusPartiallyPublished},
		{"all failed", targets(PostStatusFailed, PostStatusDLQ, PostStatusBlockedAuth), PostStatusFailed},
		{"legacy dead letter is failed", targets(PostStatus("dead_letter")), PostStatusFailed},
		{"draft plus failed", targets(PostStatusDraft, PostStatusFailed), PostStatusFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolver.Resolve(tt.targets)
			if err != nil {
				t.Fatalf("Resolve returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Resolve = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPostAggregateStatusResolverRejectsUnknownStatus(t *testing.T) {
	_, err := NewPostAggregateStatusResolver().Resolve([]PostTarget{{Status: PostStatus("unknown")}})
	if err == nil {
		t.Fatal("Resolve returned nil error for unknown status")
	}
}

func TestPostAggregateStatusResolverIsDeterministicAndIdempotent(t *testing.T) {
	resolver := NewPostAggregateStatusResolver()
	input := targets(PostStatusPublished, PostStatusFailed, PostStatusFailed)

	first, err := resolver.Resolve(input)
	if err != nil {
		t.Fatalf("first Resolve returned error: %v", err)
	}
	if first != PostStatusPartiallyPublished {
		t.Fatalf("first Resolve = %q, want partially_published", first)
	}

	// Re-resolving the same immutable target set must produce the same
	// result and must not mutate the caller's slice.
	second, err := resolver.Resolve(input)
	if err != nil {
		t.Fatalf("second Resolve returned error: %v", err)
	}
	if second != first {
		t.Fatalf("Resolve is not deterministic: first=%q second=%q", first, second)
	}
	if input[0].Status != PostStatusPublished || input[1].Status != PostStatusFailed || input[2].Status != PostStatusFailed {
		t.Fatalf("Resolve mutated input target statuses: %+v", input)
	}
}

func targets(statuses ...PostStatus) []PostTarget {
	out := make([]PostTarget, len(statuses))
	for i, status := range statuses {
		out[i].Status = status
	}
	return out
}
