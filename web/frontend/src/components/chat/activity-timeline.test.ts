import { describe, expect, it } from "vitest"

import { buildTimelineSteps } from "@/components/chat/activity-timeline"
import type { ActivityEventEnvelope } from "@/store/chat"

function event(overrides: Partial<ActivityEventEnvelope>): ActivityEventEnvelope {
  return {
    v: "1.0",
    event_id: "evt-1",
    event_type: "step.started",
    timestamp: "2026-03-30T10:12:41.203Z",
    sequence: 1,
    session_id: "sess-1",
    run_id: "run-1",
    data: {},
    ...overrides,
  }
}

describe("buildTimelineSteps", () => {
  it("renders step updates and tool progress on the matching step", () => {
    const steps = buildTimelineSteps([
      event({
        event_id: "evt-step",
        event_type: "step.started",
        data: { step_id: "step-1", title: "Planning", kind: "planning" },
      }),
      event({
        event_id: "evt-update",
        event_type: "step.updated",
        sequence: 2,
        data: { step_id: "step-1", headline: "Checking context budget" },
      }),
      event({
        event_id: "evt-tool",
        event_type: "tool.called",
        sequence: 3,
        data: {
          step_id: "step-tool",
          tool_call_id: "tc-1",
          tool_name: "web.search",
          display_name: "Searching",
        },
      }),
      event({
        event_id: "evt-tool-progress",
        event_type: "tool.progress",
        sequence: 4,
        data: {
          step_id: "step-tool",
          tool_call_id: "tc-1",
          message: "Opened 4 result pages",
        },
      }),
    ])

    expect(steps[0]?.details).toContain("Checking context budget")
    expect(steps[1]?.details).toContain("Opened 4 result pages")
  })

  it("dedupes tool.failed and tool-scoped error.raised onto one row", () => {
    const steps = buildTimelineSteps([
      event({
        event_id: "evt-tool",
        event_type: "tool.called",
        data: {
          step_id: "step-tool",
          tool_call_id: "tc-1",
          tool_name: "web.search",
          display_name: "Searching",
        },
      }),
      event({
        event_id: "evt-tool-failed",
        event_type: "tool.failed",
        sequence: 2,
        data: {
          step_id: "step-tool",
          tool_call_id: "tc-1",
          message: "search failed",
        },
      }),
      event({
        event_id: "evt-error",
        event_type: "error.raised",
        sequence: 3,
        data: {
          scope: "tool",
          step_id: "step-tool",
          tool_call_id: "tc-1",
          message: "search failed",
        },
      }),
    ])

    expect(steps).toHaveLength(1)
    expect(steps[0]?.status).toBe("failed")
    expect(steps[0]?.details.filter((detail) => detail.includes("search failed"))).toHaveLength(2)
  })
})
