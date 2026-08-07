package worker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// fakeTranslator is a ChannelTranslator test double recording every
// Translate call and returning a fixed translation (or error).
type fakeTranslator struct {
	mu    sync.Mutex
	tr    *models.YouTubeTranslation
	err   error
	calls []services.TranslateRequest
}

func (f *fakeTranslator) Translate(_ context.Context, req services.TranslateRequest) (*models.YouTubeTranslation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.err != nil {
		return nil, f.err
	}
	if f.tr == nil {
		return &models.YouTubeTranslation{Title: "Tradotto", Description: "Tradotto"}, nil
	}
	return f.tr, nil
}

func (f *fakeTranslator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeTranslator) lastCall() services.TranslateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return services.TranslateRequest{}
	}
	return f.calls[len(f.calls)-1]
}

// newTranslateTestRig wires a PublishWorker for a YouTube channel whose
// language is metadata["language"]. The provider's publishFn records
// the payload it received.
func newTranslateTestRig(channelLang string, post *models.Post) (*mockPostStore, *mockProvider, *PublishWorker) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return post, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			acct := &models.PlatformAccount{
				ID: 10, UserID: 1, Platform: "youtube",
				PlatformUserID: "UC_chan", Status: "active",
			}
			if channelLang != "" {
				acct.Metadata = models.Metadata{"language": channelLang}
			}
			return acct, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "vid1"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorkerWithoutThrottle(posts, users, "youtube", svc, vault)
	return posts, svc, w
}

func translateTestPost(title, caption, sourceLang string) *models.Post {
	post := &models.Post{ID: 100, Title: title, Caption: caption}
	if sourceLang != "" {
		post.Metadata = []byte(`{"source_language":"` + sourceLang + `"}`)
	}
	return post
}

// TestPublishTarget_TranslatesForChannelLanguage: a channel declaring
// language "es" + a post with source_language "it" → the provider
// receives the TRANSLATED title/text, and the translator was called
// exactly once with (it → es).
func TestPublishTarget_TranslatesForChannelLanguage(t *testing.T) {
	post := translateTestPost("Come iniziare a fare boxe", "Guida semplice per iniziare.", "it")
	posts, svc, w := newTranslateTestRig("es", post)

	tr := &fakeTranslator{tr: &models.YouTubeTranslation{
		Title:       "Cómo empezar a practicar boxeo",
		Description: "Una guía sencilla para empezar.",
	}}
	w.nvidiaTranslator = tr

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}

	if n := tr.callCount(); n != 1 {
		t.Fatalf("Translate calls: want 1, got %d", n)
	}
	req := tr.lastCall()
	if req.TargetLanguage != "es" {
		t.Errorf("TargetLanguage: want es, got %q", req.TargetLanguage)
	}
	if req.SourceLanguage != "it" {
		t.Errorf("SourceLanguage: want it, got %q", req.SourceLanguage)
	}
	if req.Title != post.Title || req.Description != post.Caption {
		t.Errorf("Translate input mismatch: title=%q desc=%q", req.Title, req.Description)
	}

	if svc.capturedPayload == nil {
		t.Fatal("publish payload not captured")
	}
	if svc.capturedPayload.Title != "Cómo empezar a practicar boxeo" {
		t.Errorf("published title: want translated, got %q", svc.capturedPayload.Title)
	}
	if svc.capturedPayload.Text != "Una guía sencilla para empezar." {
		t.Errorf("published text: want translated, got %q", svc.capturedPayload.Text)
	}
	// The publish must have completed (status published, not failed).
	if len(posts.updateTargets) == 0 {
		t.Fatal("no UpdateStatus captured")
	}
	if final := posts.updateTargets[len(posts.updateTargets)-1]; final.Status != models.PostStatusPublished {
		t.Errorf("final target status: want published, got %q (err=%s)", final.Status, final.ErrorMessage)
	}
}

// TestPublishTarget_SameLanguage_SkipsTranslation: channel language ==
// post source language → NO translator call and the original text is
// published unchanged.
func TestPublishTarget_SameLanguage_SkipsTranslation(t *testing.T) {
	post := translateTestPost("Come iniziare a fare boxe", "Guida semplice.", "es")
	_, svc, w := newTranslateTestRig("es", post)

	tr := &fakeTranslator{}
	w.nvidiaTranslator = tr

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if n := tr.callCount(); n != 0 {
		t.Errorf("Translate calls: want 0 (same language), got %d", n)
	}
	if svc.capturedPayload == nil || svc.capturedPayload.Title != post.Title {
		t.Errorf("published title: want original %q, got %q", post.Title, svc.capturedPayload.Title)
	}
}

// TestPublishTarget_NoChannelLanguage_SkipsTranslation: a channel
// without language metadata publishes the original text untouched.
func TestPublishTarget_NoChannelLanguage_SkipsTranslation(t *testing.T) {
	post := translateTestPost("Come iniziare a fare boxe", "Guida semplice.", "it")
	_, svc, w := newTranslateTestRig("", post)

	tr := &fakeTranslator{}
	w.nvidiaTranslator = tr

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if n := tr.callCount(); n != 0 {
		t.Errorf("Translate calls: want 0 (no channel language), got %d", n)
	}
	if svc.capturedPayload == nil || svc.capturedPayload.Title != post.Title {
		t.Errorf("published title: want original, got %q", svc.capturedPayload.Title)
	}
}

// TestPublishTarget_TranslationError_MarksTargetFailed: a translation
// failure must NEVER publish the wrong language — the target is marked
// failed so the next tick retries the whole step.
func TestPublishTarget_TranslationError_MarksTargetFailed(t *testing.T) {
	post := translateTestPost("Come iniziare a fare boxe", "Guida semplice.", "it")
	posts, svc, w := newTranslateTestRig("es", post)

	w.nvidiaTranslator = &fakeTranslator{err: errors.New("nvidia timeout")}

	// markFailed returns the reason so the tick error counter
	// increments (existing convention: the STATE is terminal-failed,
	// the ERROR is the tick signal).
	if err := w.publishTarget(context.Background(), scheduledTarget()); err == nil {
		t.Fatal("publishTarget must surface the translation failure (markFailed reason)")
	}
	if svc.publishCalls != 0 {
		t.Errorf("Publish calls: want 0 (never publish the wrong language), got %d", svc.publishCalls)
	}
	if len(posts.updateTargets) == 0 {
		t.Fatal("no UpdateStatus captured")
	}
	final := posts.updateTargets[len(posts.updateTargets)-1]
	if final.Status != models.PostStatusFailed {
		t.Errorf("final target status: want failed, got %q", final.Status)
	}
	if !strings.Contains(final.ErrorMessage, "channel language translation") {
		t.Errorf("ErrorMessage should explain the translation failure, got %q", final.ErrorMessage)
	}
}

// TestPublishTarget_TranslationCache_DedupesSiblingTargets: two targets
// of the SAME post sharing the channel language trigger ONE NVIDIA call
// (the cache serves the second target).
func TestPublishTarget_TranslationCache_DedupesSiblingTargets(t *testing.T) {
	post := translateTestPost("Come iniziare a fare boxe", "Guida semplice.", "it")
	posts, svc, w := newTranslateTestRig("es", post)

	tr := &fakeTranslator{}
	w.nvidiaTranslator = tr

	first := scheduledTarget() // post 100, account 10
	second := &models.PostTarget{ID: 201, PostID: 100, PlatformAccountID: 11, Status: models.PostStatusScheduled}

	if err := w.publishTarget(context.Background(), first); err != nil {
		t.Fatalf("first publishTarget: %v", err)
	}
	if err := w.publishTarget(context.Background(), second); err != nil {
		t.Fatalf("second publishTarget: %v", err)
	}

	if n := tr.callCount(); n != 1 {
		t.Fatalf("Translate calls for 2 sibling targets: want 1 (cache hit), got %d", n)
	}
	if svc.publishCalls != 2 {
		t.Errorf("Publish calls: want 2 (both targets published), got %d", svc.publishCalls)
	}
	if svc.capturedPayload == nil || svc.capturedPayload.Title != "Tradotto" {
		t.Errorf("second target must publish the cached translation, got %q", svc.capturedPayload.Title)
	}
	_ = posts
}

// TestLocalizeForChannel_NoTranslator_ReturnsOriginal: a worker without
// a wired translator is a no-op (the pre-feature behaviour).
func TestLocalizeForChannel_NoTranslator_ReturnsOriginal(t *testing.T) {
	w := &PublishWorker{}
	post := translateTestPost("Titolo", "Descrizione", "")
	account := &models.PlatformAccount{Metadata: models.Metadata{"language": "es"}}
	out, translated, err := w.localizeForChannel(context.Background(), scheduledTarget(), account, post)
	if err != nil {
		t.Fatalf("localizeForChannel: %v", err)
	}
	if translated {
		t.Error("translated=true with no translator wired")
	}
	if out != post {
		t.Error("must return the original post pointer when the feature is off")
	}
}
