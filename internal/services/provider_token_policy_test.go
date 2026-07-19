package services

import (
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type staticTokenPolicyProvider struct {
	name  string
	types []string
}

func (s staticTokenPolicyProvider) Name() string { return s.name }

func (s staticTokenPolicyProvider) PreferredTokenTypes() []string {
	return s.types
}

type noTokenPolicyProvider struct{}

func (noTokenPolicyProvider) Name() string { return "no-policy" }

func TestCapabilityRouter_TokenPolicy(t *testing.T) {
	router := NewCapabilityRouter()

	provider := staticTokenPolicyProvider{
		name:  "youtube",
		types: []string{models.TokenTypeBearer},
	}
	router.Register("youtube", provider)

	tp, ok := router.TokenPolicy("youtube")
	if !ok {
		t.Fatal("expected TokenPolicy to be registered for youtube")
	}

	got := tp.PreferredTokenTypes()
	if len(got) != 1 || got[0] != models.TokenTypeBearer {
		t.Fatalf("expected [bearer], got %v", got)
	}
}

func TestCapabilityRouter_TokenPolicy_NotImplemented(t *testing.T) {
	router := NewCapabilityRouter()
	router.Register("no-policy", noTokenPolicyProvider{})

	_, ok := router.TokenPolicy("no-policy")
	if ok {
		t.Fatal("expected TokenPolicy to not be registered when provider does not implement interface")
	}
}

func TestDefaultTokenTypes(t *testing.T) {
	got := DefaultTokenTypes()
	want := []string{
		models.TokenTypeBearer,
		models.TokenTypeShortLived,
		models.TokenTypeLongLived,
		models.TokenTypePageAccess,
	}

	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}

	for i, v := range want {
		if got[i] != v {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}
