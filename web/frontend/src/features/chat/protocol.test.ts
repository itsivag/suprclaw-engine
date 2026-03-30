import { afterEach, describe, expect, it } from "vitest"

import { handlePicoMessage } from "@/features/chat/protocol"
import {
  getChatState,
  resetChatStoreForTests,
  updateChatStore,
  type ActivityEventEnvelope,
} from "@/store/chat"

function activityEvent(
  overrides: Partial<ActivityEventEnvelope>,
): ActivityEventEnvelope {
  return {
    v: "1.0",
    event_id: "evt-1",
    event_type: "run.started",
    timestamp: "2026-03-30T10:12:41.203Z",
    sequence: 1,
    session_id: "sess-1",
    run_id: "run-1",
    data: {},
    ...overrides,
  }
}

afterEach(() => {
  resetChatStoreForTests()
})

describe("handlePicoMessage", () => {
  it("dedupes activity events by event_id", () => {
    const event = activityEvent({})
    handlePicoMessage(event, "sess-1")
    handlePicoMessage(event, "sess-1")

    const state = getChatState()
    expect(state.activityRuns["run-1"]?.events).toHaveLength(1)
  })

  it("orders events per run by sequence", () => {
    handlePicoMessage(
      activityEvent({
        event_id: "evt-2",
        event_type: "step.completed",
        sequence: 2,
        data: { step_id: "step-1" },
      }),
      "sess-1",
    )
    handlePicoMessage(
      activityEvent({
        event_id: "evt-1",
        event_type: "step.started",
        sequence: 1,
        data: { step_id: "step-1", title: "Planning", kind: "planning" },
      }),
      "sess-1",
    )

    const state = getChatState()
    expect(
      state.activityRuns["run-1"]?.events.map((event) => event.sequence),
    ).toEqual([1, 2])
  })

  it("marks unknown run status as stale and clears typing", () => {
    updateChatStore({
      isTyping: true,
      activeRunId: "run-1",
      activityRuns: {
        "run-1": {
          runId: "run-1",
          status: "in_progress",
          events: [],
          lastEventAt: Date.now(),
          toolStates: {},
        },
      },
    })

    handlePicoMessage(
      {
        type: "run.status",
        session_id: "sess-1",
        payload: {
          run_id: "run-1",
          status: "unknown",
        },
      },
      "sess-1",
    )

    const state = getChatState()
    expect(state.isTyping).toBe(false)
    expect(state.activeRunId).toBe("run-1")
    expect(state.activityRuns["run-1"]?.status).toBe("stale")
  })

  it("allows late terminal events to transition stale runs", () => {
    updateChatStore({
      isTyping: false,
      activeRunId: "run-1",
      activityRuns: {
        "run-1": {
          runId: "run-1",
          status: "stale",
          events: [],
          lastEventAt: Date.now(),
          toolStates: {},
        },
      },
    })

    handlePicoMessage(
      activityEvent({
        event_id: "evt-complete",
        event_type: "run.completed",
        data: { status: "completed" },
      }),
      "sess-1",
    )

    const state = getChatState()
    expect(state.activityRuns["run-1"]?.status).toBe("completed")
    expect(state.activeRunId).toBe("")
  })

  it("drops tool.progress after local terminal tool state", () => {
    handlePicoMessage(
      activityEvent({
        event_id: "evt-tool-complete",
        event_type: "tool.completed",
        sequence: 2,
        data: {
          step_id: "step-tool",
          tool_call_id: "tc-1",
          result_preview: "done",
        },
      }),
      "sess-1",
    )

    handlePicoMessage(
      activityEvent({
        event_id: "evt-tool-progress",
        event_type: "tool.progress",
        sequence: 3,
        data: {
          step_id: "step-tool",
          tool_call_id: "tc-1",
          message: "still running",
        },
      }),
      "sess-1",
    )

    const state = getChatState()
    expect(state.activityRuns["run-1"]?.events.map((event) => event.event_id)).toEqual([
      "evt-tool-complete",
    ])
  })
})
