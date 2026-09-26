// Package agenttools contains the control-plane catalog exposed to agents.
// Remote worker job names are implementation details; agents use stable,
// typed tool names and the control plane maps them through this catalog.
package agenttools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const PermissionAutomation = "automation"

type Definition struct {
	Name               string   `json:"name"`
	RemoteType         string   `json:"remote_type"`
	Description        string   `json:"description"`
	RequiredPermission string   `json:"required_permission"`
	Submit             bool     `json:"submit"`
	Available          bool     `json:"available"`
	InputSchema        string   `json:"input_schema,omitempty"`
	ResultSchema       string   `json:"result_schema,omitempty"`
	ArtifactKinds      []string `json:"artifact_kinds,omitempty"`
	ResourceClass      string   `json:"estimated_resource_class,omitempty"`
}

type remoteTypeMetadata struct {
	Type                   string   `json:"type"`
	Name                   string   `json:"name"`
	InputSchema            string   `json:"input_schema"`
	ResultSchema           string   `json:"result_schema"`
	ArtifactKinds          []string `json:"artifact_kinds"`
	EstimatedResourceClass string   `json:"estimated_resource_class"`
}

type Catalog struct {
	definitions []Definition
}

func NewCatalog() Catalog {
	return Catalog{definitions: []Definition{
		{Name: "content.generate_script", RemoteType: "script.generate", Description: "Generate a script and content plan.", RequiredPermission: PermissionAutomation, Submit: true},
		{Name: "content.generate_voiceover", RemoteType: "voiceover.generate", Description: "Generate voiceover audio.", RequiredPermission: PermissionAutomation, Submit: true},
		{Name: "content.acquire_stock", RemoteType: "media.stock", Description: "Acquire stock media.", RequiredPermission: PermissionAutomation, Submit: true},
		{Name: "content.extract_youtube_clip", RemoteType: "youtube_clip.extract", Description: "Extract a selected YouTube clip.", RequiredPermission: PermissionAutomation, Submit: true},
		{Name: "content.generate_image", RemoteType: "image.generate.google", Description: "Generate an image asset.", RequiredPermission: PermissionAutomation, Submit: true},
		{Name: "content.render_clip", RemoteType: "clip.render", Description: "Render a clip through the execution plane.", RequiredPermission: PermissionAutomation, Submit: true},
		{Name: "content.assemble_video", RemoteType: "video.assemble", Description: "Assemble compatible rendered clips and final audio.", RequiredPermission: PermissionAutomation, Submit: true},
		{Name: "content.create_video", RemoteType: "video.create", Description: "Generate a complete video and schedule it for publication.", RequiredPermission: PermissionAutomation, Submit: true},
		{Name: "publishing.create_thumbnail", Description: "Create a thumbnail on the control plane.", RequiredPermission: PermissionAutomation},
		{Name: "publishing.attach_thumbnail", Description: "Attach a thumbnail to a publishing session.", RequiredPermission: PermissionAutomation},
		{Name: "publishing.publish_video", Description: "Publish a prepared video through InstaEdit.", RequiredPermission: PermissionAutomation},
		{Name: "jobs.get", Description: "Read a persisted workflow and its execution references.", RequiredPermission: PermissionAutomation},
	}}
}

func (c Catalog) Definitions() []Definition {
	out := append([]Definition(nil), c.definitions...)
	return out
}

func (c Catalog) Resolve(name string) (Definition, bool) {
	name = strings.TrimSpace(name)
	for _, d := range c.definitions {
		if d.Name == name {
			return d, true
		}
	}
	return Definition{}, false
}

func (c Catalog) Available(remoteTypes json.RawMessage) ([]Definition, error) {
	set, err := DecodeRemoteTypes(remoteTypes)
	if err != nil {
		return nil, err
	}
	metadata := decodeRemoteTypeMetadata(remoteTypes)
	out := make([]Definition, 0, len(c.definitions))
	for _, d := range c.definitions {
		d.Available = set[d.RemoteType]
		if remote, ok := metadata[d.RemoteType]; ok {
			d.InputSchema = remote.InputSchema
			d.ResultSchema = remote.ResultSchema
			d.ArtifactKinds = append([]string(nil), remote.ArtifactKinds...)
			d.ResourceClass = remote.EstimatedResourceClass
		}
		out = append(out, d)
	}
	return out, nil
}

func decodeRemoteTypeMetadata(raw json.RawMessage) map[string]remoteTypeMetadata {
	var values []json.RawMessage
	var envelope struct {
		Types []json.RawMessage `json:"types"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.Types != nil {
		values = envelope.Types
	} else if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	result := make(map[string]remoteTypeMetadata, len(values))
	for _, value := range values {
		var item remoteTypeMetadata
		if json.Unmarshal(value, &item) != nil {
			continue
		}
		name := item.Type
		if name == "" {
			name = item.Name
		}
		if strings.TrimSpace(name) != "" {
			result[name] = item
		}
	}
	return result
}

// DecodeRemoteTypes accepts both the current {"types":[...]} envelope and
// the historical plain array. It never leaks unregistered internal types.
func DecodeRemoteTypes(raw json.RawMessage) (map[string]bool, error) {
	var values []json.RawMessage
	var envelope struct {
		Types []json.RawMessage `json:"types"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Types != nil {
		values = envelope.Types
	} else if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("decode remote job types: %w", err)
	}
	set := make(map[string]bool, len(values))
	for _, value := range values {
		var name string
		if json.Unmarshal(value, &name) == nil && strings.TrimSpace(name) != "" {
			set[name] = true
			continue
		}
		var item struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if json.Unmarshal(value, &item) != nil {
			continue
		}
		name = item.Type
		if name == "" {
			name = item.Name
		}
		if strings.TrimSpace(name) != "" {
			set[name] = true
		}
	}
	return set, nil
}

func (c Catalog) Names() []string {
	result := make([]string, 0, len(c.definitions))
	for _, d := range c.definitions {
		result = append(result, d.Name)
	}
	sort.Strings(result)
	return result
}
