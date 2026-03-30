import { normalizeUnixTimestamp } from "@/features/chat/state"
import {
  type ActivityEventEnvelope,
  type AgentInfo,
  type ChatMessage,
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
    const existingRun = prev.activityRuns[event.run_id] || {
      runId: event.run_id,
      status: "in_progress" as const,
      events: [],
    }
    const alreadySeen = existingRun.events.some(
      (entry) => entry.event_id === event.event_id,
    )
    const nextEvents = alreadySeen
      ? existingRun.events
      : [...existingRun.events, event].sort(
          (left, right) => left.sequence - right.sequence,
        )

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
      case "error.raised":
        if (prev.activeRunId === event.run_id) {
          nextIsTyping = false
        }
        break
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
        },
      },
      activeRunId: nextActiveRunId,
      isTyping: nextIsTyping,
      typingStatus: "",
      messages: nextMessages,
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
