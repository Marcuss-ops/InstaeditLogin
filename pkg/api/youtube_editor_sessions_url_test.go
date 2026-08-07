package api

import "testing"

func TestSafeEditorAssetURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: ""},
		{name: "http", raw: "http://cdn.example.test/thumb.jpg", want: "http://cdn.example.test/thumb.jpg"},
		{name: "https", raw: " https://cdn.example.test/thumb.jpg ", want: "https://cdn.example.test/thumb.jpg"},
		{name: "file", raw: "file:///tmp/thumb.jpg", want: ""},
		{name: "blob", raw: "blob:https://app.example.test/id", want: ""},
		{name: "data", raw: "data:image/png;base64,abc", want: ""},
		{name: "relative", raw: "/uploads/thumb.jpg", want: ""},
		{name: "missing host", raw: "https:///thumb.jpg", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeEditorAssetURL(tt.raw); got != tt.want {
				t.Fatalf("safeEditorAssetURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
