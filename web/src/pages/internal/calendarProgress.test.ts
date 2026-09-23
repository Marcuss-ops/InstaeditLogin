import { describe, expect, it } from "vitest";
import { getCalendarProgressRows } from "./calendarProgress";

describe("getCalendarProgressRows", () => {
  it("projects stage progress entries for the calendar detail panel", () => {
    expect(getCalendarProgressRows({
      stage_progress: {
        script_generation: { status: "completed", progress: 100 },
        voiceover_render: { status: "running", completed_count: 2, total_count: 5 },
      },
    })).toEqual([
      { key: "stage-script_generation", label: "SCRIPTING", status: "completed", progress: 100 },
      { key: "stage-voiceover_render", label: "VOICEOVER", status: "running", progress: undefined },
    ]);
  });

  it("falls back to timeline and then event data", () => {
    expect(getCalendarProgressRows({ timeline: [{ stage: "final_render", status: "running" }] })).toEqual([
      { key: "timeline-0", label: "Final Render", status: "running", progress: undefined },
    ]);
    expect(getCalendarProgressRows({ events: [{ event_type: "phase.completed", message: "Script ready" }] })).toEqual([
      { key: "event-0", label: "Phase Completed", status: "Script ready", progress: undefined },
    ]);
  });

  it("ignores malformed snapshots and bounds the displayed history", () => {
    expect(getCalendarProgressRows(undefined)).toEqual([]);
    expect(getCalendarProgressRows({ events: ["not an event", { type: "render.started", status: "running" }] })).toHaveLength(1);
    expect(getCalendarProgressRows({ stage_progress: { render: { progress: 140 } } })[0].progress).toBe(100);
  });
});
