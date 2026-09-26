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
		if d.Name == "content.create_video" && d.Available {
			t.Fatal("script and clip jobs alone must not imply a final-video assembler")
		}
	}
}

func TestCatalogExposesFullVideoOnlyWhenMasterAdvertisesVideoCreate(t *testing.T) {
	got, err := NewCatalog().Available(json.RawMessage(`{"types":[{"type":"script.generate"},{"type":"clip.render"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		if d.Name == "content.create_video" && d.Available {
			t.Fatal("content.create_video must stay unavailable without a supported full-video endpoint")
		}
	}
	got, err = NewCatalog().Available(json.RawMessage(`{"types":[{"type":"video.create","input_schema":"video.create.v1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range got {
		if definition.Name == "content.create_video" {
			if !definition.Available || definition.RemoteType != "video.create" || definition.InputSchema != "video.create.v1" {
				t.Fatalf("video.create contract not projected: %+v", definition)
			}
			return
		}
	}
	t.Fatal("content.create_video missing from catalog")
}

func TestCatalogPreservesRemoteSchemaReferencesAndArtifacts(t *testing.T) {
	raw := json.RawMessage(`{"types":[{"type":"script.generate","input_schema":"script.generate.v1","result_schema":"script.generate.result.v1","artifact_kinds":["script"],"estimated_resource_class":"llm"}]}`)
	got, err := NewCatalog().Available(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range got {
		if definition.Name != "content.generate_script" {
			continue
		}
		if !definition.Available || definition.InputSchema != "script.generate.v1" || definition.ResultSchema != "script.generate.result.v1" || definition.ResourceClass != "llm" {
			t.Fatalf("remote schema metadata not projected: %+v", definition)
		}
		if len(definition.ArtifactKinds) != 1 || definition.ArtifactKinds[0] != "script" {
			t.Fatalf("artifact kinds not projected: %+v", definition.ArtifactKinds)
		}
		return
	}
	t.Fatal("content.generate_script missing from catalog")
}
