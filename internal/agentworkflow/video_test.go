package agentworkflow

import "testing"

func TestPlanVideoCreateIsDeterministic(t *testing.T) {
	p, err := PlanVideoCreate(VideoCreateRequest{Project: "p", Topic: "Elon Musk", Language: "en", UseStock: true, UseYouTube: true, IdempotencyKey: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	wantTools := []string{
		"content.generate_script",
		"content.extract_youtube_clip",
		"content.acquire_stock",
		"content.generate_voiceover",
		"content.render_clip",
		"content.assemble_video",
	}
	if len(p.Calls) != len(wantTools) {
		t.Fatalf("got %d calls, want %d: %+v", len(p.Calls), len(wantTools), p.Calls)
	}
	for i, want := range wantTools {
		if p.Calls[i].ToolName != want {
			t.Errorf("call %d tool = %q, want %q", i, p.Calls[i].ToolName, want)
		}
		if p.Calls[i].IdempotencyKey != "run-1-"+[]string{"script", "youtube", "stock", "voiceover", "render", "assemble"}[i] {
			t.Errorf("call %d idempotency key = %q", i, p.Calls[i].IdempotencyKey)
		}
	}
}

func TestPlanVideoCreateWithoutSourceAcquisition(t *testing.T) {
	p, err := PlanVideoCreate(VideoCreateRequest{
		Project:        "elon-musk-short",
		Topic:          "Elon Musk",
		Language:       "it",
		IdempotencyKey: "elon-run-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"content.generate_script",
		"content.generate_voiceover",
		"content.render_clip",
		"content.assemble_video",
	}
	if len(p.Calls) != len(want) {
		t.Fatalf("got %d calls, want %d: %+v", len(p.Calls), len(want), p.Calls)
	}
	for i, tool := range want {
		if p.Calls[i].ToolName != tool {
			t.Errorf("call %d tool = %q, want %q", i, p.Calls[i].ToolName, tool)
		}
	}
}

func TestPlanVideoCreateRejectsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		in   VideoCreateRequest
	}{
		{name: "project", in: VideoCreateRequest{Topic: "Elon Musk", Language: "en", IdempotencyKey: "run"}},
		{name: "topic", in: VideoCreateRequest{Project: "p", Language: "en", IdempotencyKey: "run"}},
		{name: "language", in: VideoCreateRequest{Project: "p", Topic: "Elon Musk", IdempotencyKey: "run"}},
		{name: "idempotency key", in: VideoCreateRequest{Project: "p", Topic: "Elon Musk", Language: "en"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := PlanVideoCreate(tt.in); err == nil {
				t.Fatal("expected missing required field to be rejected")
			}
		})
	}
}
