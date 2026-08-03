package veloxjobs

import (
	"encoding/json"
	"testing"
)

func TestTypedValidatorsAcceptSupportedSpecs(t *testing.T) {
	tests := []struct {
		jobType string
		spec    string
	}{
		{"scene.composite.v1", `{"scenes":[{"id":"scene-1","text":"hello","assets":{"primary_clip":{"asset_id":"asset-1"}},"audio":{"voiceover":{"uri":"velox-asset://voice"}},"timeline":{"duration_ms":1000},"visual_slots":[{"id":"intro","asset":{"asset_id":"asset-2"}}]}]}`},
		{"clip.stock.v1", `{"scenes":[{"id":"scene-1","primary_clip":{"asset_id":"clip-1"},"stock":{"asset_id":"stock-1"},"bindings":{"clip":{"asset_id":"clip-1"},"stock":{"asset_id":"stock-1"}}}]}`},
		{"scene.image.v1", `{"scenes":[{"id":"scene-1","image":{"asset_id":"image-1"},"subtitles":[{"text":"hello","start_ms":0,"end_ms":500}]}]}`},
		{"slideshow.v1", `{"images":[{"id":"image-1","asset_id":"asset-1","duration_ms":1000,"transition":{"type":"fade","duration_ms":250}}]}`},
	}
	registry := NewDefaultRegistry()
	for _, tt := range tests {
		t.Run(tt.jobType, func(t *testing.T) {
			definition, err := registry.Resolve(tt.jobType)
			if err != nil {
				t.Fatal(err)
			}
			if err := definition.Validator.Validate(json.RawMessage(tt.spec)); err != nil {
				t.Fatalf("valid spec rejected: %v", err)
			}
		})
	}
}

func TestTypedValidatorsRejectNullItems(t *testing.T) {
	registry := NewDefaultRegistry()
	for _, test := range []struct {
		jobType string
		spec    string
	}{
		{"scene.composite.v1", `{"scenes":[null]}`},
		{"clip.stock.v1", `{"scenes":[null]}`},
		{"scene.image.v1", `{"scenes":[null]}`},
		{"slideshow.v1", `{"images":[null]}`},
	} {
		t.Run(test.jobType, func(t *testing.T) {
			definition, err := registry.Resolve(test.jobType)
			if err != nil {
				t.Fatal(err)
			}
			if err := definition.Validator.Validate(json.RawMessage(test.spec)); err == nil {
				t.Fatal("null item was accepted")
			}
		})
	}
}

func TestTypedValidatorsRejectUnknownFieldsAtEverySchemaLevel(t *testing.T) {
	tests := []struct {
		name    string
		jobType string
		spec    string
	}{
		{"scene top level", "scene.composite.v1", `{"scenes":[],"unexpected":true}`},
		{"scene item", "scene.image.v1", `{"scenes":[{"id":"scene-1","unexpected":true}]}`},
		{"nested assets", "clip.stock.v1", `{"scenes":[{"id":"scene-1","assets":{"unexpected":true}}]}`},
		{"nested transition", "slideshow.v1", `{"images":[{"id":"image-1","transition":{"type":"fade","unexpected":true}}]}`},
	}
	registry := NewDefaultRegistry()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			definition, err := registry.Resolve(tt.jobType)
			if err != nil {
				t.Fatal(err)
			}
			if err := definition.Validator.Validate(json.RawMessage(tt.spec)); err == nil {
				t.Fatal("unknown field was accepted")
			}
		})
	}
}
