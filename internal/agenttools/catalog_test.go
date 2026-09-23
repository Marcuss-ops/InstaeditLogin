package agenttools

import (
	"encoding/json"
	"testing"
)

func TestCatalogFiltersInternalRemoteTypes(t *testing.T) {
	c := NewCatalog()
	got, err := c.Available(json.RawMessage(`{"types":[{"type":"script.generate"},{"type":"system.cleanup"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		if d.Name == "system.cleanup" {
			t.Fatal("internal job leaked into catalog")
		}
		if d.Name == "content.generate_script" && !d.Available {
			t.Fatal("script should be available")
		}
	}
}

func TestCatalogSupportsPlainTypeArray(t *testing.T) {
	got, err := NewCatalog().Available(json.RawMessage(`[
        "script.generate", {"name":"clip.render"}
    ]`))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		if d.Name == "content.create_video" && !d.Available {
			t.Fatal("scene-composite video lane should be available")
		}
	}
}
