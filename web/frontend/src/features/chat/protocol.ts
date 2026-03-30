import { normalizeUnixTimestamp } from "@/features/chat/state"
import {
  type ActivityEventEnvelope,
  type AgentInfo,
  type ActivityRunState,
  type ChatMessage,
  type ToolLifecycleStatus,
  updateChatStore,
} from "@/store/chat"

export interface PicoMessage {
  type?: string
  id?: string
  session_id?: string
  timestamp?: number | string
  payload?: Record<string, unknown>
  event_type?: string
  run_id?: string
  sequence?: number
  data?: Record<string, unknown>
  v?: string
}

type RunStatusValue = "in_progress" | "completed" | "failed" | "unknown"

function isActivityEventEnvelope(
  message: PicoMessage,
): message is ActivityEventEnvelope {
  return (
    typeof message.event_type === "string" &&
    typeof message.run_id === "string" &&
    typeof message.sequence === "number" &&
    typeof message.session_id === "string"
  )
}

function normalizeEventTimestamp(timestamp: string): number {
  const parsed = Date.parse(timestamp)
  if (Number.isNaN(parsed)) {
    return Date.now()
  }
  return parsed
}

function asString(value: unknown): string {
  return typeof value === "string" ? value : ""
}

function buildDefaultRunState(runId: string): ActivityRunState {
  return {
    runId,
    status: "in_progress",
    events: [],
    lastEventAt: Date.now(),
    toolStates: {},
  }
}

function toolStateKey(runId: string, toolCallID: string): string {
  return `${runId}:${toolCallID}`
}

function withToolState(
  toolStates: Record<string, ToolLifecycleStatus>,
  key: string,
  status: ToolLifecycleStatus,
): Record<string, ToolLifecycleStatus> {
  if (!key || toolStates[key] === status) {
    return toolStates
  }
  return {
    ...toolStates,
    [key]: status,
  }
}

function isRunStatusValue(value: unknown): value is RunStatusValue {
  return (
    value === "in_progress" ||
    value === "completed" ||
    value === "failed" ||
    value === "unknown"
  )
}

function appendAssistantMessage(
  event: ActivityEventEnvelope,
  current: ChatMessage[],
): ChatMessage[] {
  const text = (event.data?.text as string) || ""
  if (!text) {
    return current
  }
  const messageId = (event.data?.message_id as string) || event.event_id
  if (current.some((msg) => msg.id === messageId)) {
    return current
  }
  return [
    ...current,
    {
      id: messageId,
      role: "assistant" as const,
      content: text,
      timestamp: normalizeEventTimestamp(event.timestamp),
    },
  ]
}

function applyActivityEvent(event: ActivityEventEnvelope) {
  updateChatStore((prev) => {
    const existingRun =
      prev.activityRuns[event.run_id] || buildDefaultRunState(event.run_id)
    const eventData = event.data || {}
    const toolCallID = asString(eventData.tool_call_id)
    const stateKey = toolCallID ? toolStateKey(event.run_id, toolCallID) : ""
    const currentToolStatus = stateKey ? existingRun.toolStates[stateKey] : ""
    const shouldDropProgress =
      event.event_type === "tool.progress" &&
      currentToolStatus !== "" &&
      currentToolStatus !== "in_progress"
    if (shouldDropProgress) {
      return prev
    }

    const alreadySeen = existingRun.events.some(
      (entry) => entry.event_id === event.event_id,
    )
    const shouldRecordEvent = !alreadySeen
    const nextEvents = shouldRecordEvent
      ? [...existingRun.events, event].sort(
          (left, right) => left.sequence - right.sequence,
        )
      : existingRun.events
    let nextToolStates = existingRun.toolStates
    if (shouldRecordEvent && stateKey) {
      switch (event.event_type) {
        case "tool.called":
        case "tool.progress":
          nextToolStates = withToolState(nextToolStates, stateKey, "in_progress")
          break
        case "tool.completed":
          nextToolStates = withToolState(nextToolStates, stateKey, "completed")
          break
        case "tool.failed":
          nextToolStates = withToolState(nextToolStates, stateKey, "failed")
          break
        default:
          break
      }
    }

    let nextStatus = existingRun.status
    let nextIsTyping = prev.isTyping
    let nextActiveRunId = prev.activeRunId
    let nextMessages = prev.messages

    switch (event.event_type) {
      case "run.started":
        nextStatus = "in_progress"
        nextIsTyping = true
        nextActiveRunId = event.run_id
        break
      case "run.completed":
        nextStatus = "completed"
        if (prev.activeRunId === event.run_id) {
          nextIsTyping = false
          nextActiveRunId = ""
        }
        break
      case "run.failed":
        nextStatus = "failed"
        if (prev.activeRunId === event.run_id) {
          nextIsTyping = false
          nextActiveRunId = ""
        }
        break
      case "message.completed":
        nextMessages = appendAssistantMessage(event, prev.messages)
        break
      case "error.raised": {
        const scope = asString(eventData.scope)
        if (scope === "run" && prev.activeRunId === event.run_id) {
          nextIsTyping = false
        }
        break
      }
      default:
        break
    }

    return {
      activityRuns: {
        ...prev.activityRuns,
        [event.run_id]: {
          ...existingRun,
          status: nextStatus,
          events: nextEvents,
          lastEventAt: shouldRecordEvent ? Date.now() : existingRun.lastEventAt,
          toolStates: nextToolStates,
        },
      },
      activeRunId: nextActiveRunId,
      isTyping: nextIsTyping,
      typingStatus: "",
      messages: nextMessages,
    }
  })
}

function applyRunStatus(payload: Record<string, unknown>) {
  const status = payload.status
  if (!isRunStatusValue(status)) {
    return
  }
  const payloadRunID = asString(payload.run_id)

  updateChatStore((prev) => {
    const targetRunID = payloadRunID || prev.activeRunId
    const nextRuns = { ...prev.activityRuns }
    if (targetRunID) {
      const existing = nextRuns[targetRunID] || buildDefaultRunState(targetRunID)
      let nextStatus = existing.status
      if (status === "unknown") {
        nextStatus = "stale"
      } else if (status === "in_progress") {
        nextStatus = "in_progress"
      } else if (status === "completed") {
        nextStatus = "completed"
      } else if (status === "failed") {
        nextStatus = "failed"
      }
      nextRuns[targetRunID] = {
        ...existing,
        status: nextStatus,
      }
    }

    let nextActiveRunID = prev.activeRunId
    let nextIsTyping = prev.isTyping
    switch (status) {
      case "in_progress":
        if (targetRunID) {
          nextActiveRunID = targetRunID
          nextIsTyping = true
        }
        break
      case "completed":
      case "failed":
        nextIsTyping = false
        if (!targetRunID || prev.activeRunId === targetRunID) {
          nextActiveRunID = ""
        }
        break
      case "unknown":
        nextIsTyping = false
        nextActiveRunID = targetRunID
        break
      default:
        break
    }

    return {
      activityRuns: nextRuns,
      activeRunId: nextActiveRunID,
      isTyping: nextIsTyping,
      typingStatus: "",
    }
  })
}

export function handlePicoMessage(
  message: PicoMessage,
  expectedSessionId: string,
) {
  if (isActivityEventEnvelope(message)) {
    if (message.session_id !== expectedSessionId) {
      return
    }
    applyActivityEvent(message)
    return
  }

  if (message.session_id && message.session_id !== expectedSessionId) {
    return
  }

  const payload = message.payload || {}

  switch (message.type) {
    case "message.create": {
      const content = (payload.content as string) || ""
      const messageId = (payload.message_id as string) || `pico-${Date.now()}`
      const timestamp =
        message.timestamp !== undefined &&
        Number.isFinite(Number(message.timestamp))
          ? normalizeUnixTimestamp(Number(message.timestamp))
          : Date.now()

      updateChatStore((prev) => ({
        messages: [
          ...prev.messages,
          {
            id: messageId,
            role: "assistant",
            content,
            timestamp,
          },
        ],
      }))
      break
    }

    case "message.update": {
      const content = (payload.content as string) || ""
      const messageId = payload.message_id as string
      if (!messageId) {
        break
      }

      updateChatStore((prev) => ({
        messages: prev.messages.map((msg) =>
          msg.id === messageId ? { ...msg, content } : msg,
        ),
      }))
      break
    }

    case "typing.start":
    case "typing.status":
    case "typing.stop":
      break

    case "error":
      console.error("Pico error:", payload)
      updateChatStore({ isTyping: false })
      break

    case "pong":
      break

    case "run.status":
      applyRunStatus(payload)
      break

    case "agent.list": {
      const agents = (payload.agents as AgentInfo[]) || []
      const defaultAgent = (payload.default as string) || ""
      updateChatStore((prev) => ({
        agents,
        activeAgentId: prev.activeAgentId || defaultAgent,
      }))
      break
    }

    default:
      console.log("Unknown pico message type:", message.type)
  }
}
