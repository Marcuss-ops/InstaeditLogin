// Package agentworkflow defines durable control-plane workflow contracts.
// It plans tool calls; execution remains owned by PipelineGen workers.
package agentworkflow

import (
	"fmt"
	"strings"
)

type VideoCreateRequest struct {
	Project        string
	Topic          string
	Language       string
	Duration       string
	UseStock       bool
	UseYouTube     bool
	IdempotencyKey string
}

type ToolCall struct {
	ToolName       string `json:"tool_name"`
	IdempotencyKey string `json:"idempotency_key"`
}

type VideoCreatePlan struct {
	Calls []ToolCall `json:"calls"`
}

func PlanVideoCreate(in VideoCreateRequest) (VideoCreatePlan, error) {
	in.Project = strings.TrimSpace(in.Project)
	in.Topic = strings.TrimSpace(in.Topic)
	in.Language = strings.TrimSpace(in.Language)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if in.Project == "" || in.Topic == "" || in.Language == "" || in.IdempotencyKey == "" {
		return VideoCreatePlan{}, fmt.Errorf("project, topic, language, and idempotency_key are required")
	}
	key := func(s string) string { return in.IdempotencyKey + "-" + s }
	calls := []ToolCall{{ToolName: "content.generate_script", IdempotencyKey: key("script")}}
	if in.UseYouTube {
		calls = append(calls, ToolCall{ToolName: "content.extract_youtube_clip", IdempotencyKey: key("youtube")})
	}
	if in.UseStock {
		calls = append(calls, ToolCall{ToolName: "content.acquire_stock", IdempotencyKey: key("stock")})
	}
	calls = append(calls,
		ToolCall{ToolName: "content.generate_voiceover", IdempotencyKey: key("voiceover")},
		ToolCall{ToolName: "content.render_clip", IdempotencyKey: key("render")},
		ToolCall{ToolName: "content.assemble_video", IdempotencyKey: key("assemble")},
	)
	return VideoCreatePlan{Calls: calls}, nil
}
