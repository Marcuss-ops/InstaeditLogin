package config

import "testing"

func TestValidateYouTubeRedirectURI_ProductionContract(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want bool
	}{
		{
			name: "canonical production callback",
			uri:  "https://api.instaedit.org/api/v1/auth/youtube/callback",
			want: true,
		},
		{
			name: "http rejected",
			uri:  "http://api.instaedit.org/api/v1/auth/youtube/callback",
		},
		{
			name: "localhost rejected",
			uri:  "https://localhost/api/v1/auth/youtube/callback",
		},
		{
			name: "loopback rejected",
			uri:  "https://127.0.0.1/api/v1/auth/youtube/callback",
		},
		{
			name: "development host rejected",
			uri:  "https://dev.instaedit.org/api/v1/auth/youtube/callback",
		},
		{
			name: "untrusted InstaEdit subdomain rejected",
			uri:  "https://oauth.instaedit.org/api/v1/auth/youtube/callback",
		},
		{
			name: "wrong path rejected",
			uri:  "https://api.instaedit.org/api/v1/auth/youtube/callback/",
		},
		{
			name: "query rejected",
			uri:  "https://api.instaedit.org/api/v1/auth/youtube/callback?env=production",
		},
		{
			name: "credentials rejected",
			uri:  "https://client:secret@api.instaedit.org/api/v1/auth/youtube/callback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateYouTubeRedirectURI(tt.uri, "production")
			if tt.want && err != nil {
				t.Fatalf("valid URI rejected: %v", err)
			}
			if !tt.want && err == nil {
				t.Fatal("invalid production URI accepted")
			}
		})
	}
}

func TestValidateYouTubeRedirectURI_LocalAndStagingCompatibility(t *testing.T) {
	for _, env := range []string{"dev", "staging"} {
		t.Run(env, func(t *testing.T) {
			if err := validateYouTubeRedirectURI("http://localhost:8080/api/v1/auth/youtube/callback", env); err != nil {
				t.Fatalf("local-compatible URI rejected in %s: %v", env, err)
			}
		})
	}
}

func TestValidateYouTubeRedirectURI_ProductionRejectsMissingURI(t *testing.T) {
	if err := validateYouTubeRedirectURI("", "production"); err == nil {
		t.Fatal("empty production redirect URI accepted")
	}
}
