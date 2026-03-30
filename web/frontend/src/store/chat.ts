import { atom, getDefaultStore } from "jotai"

import {
  getInitialActiveSessionId,
  writeStoredSessionId,
} from "@/features/chat/state"

export interface ChatMessage {
  id: string
  role: "user" | "assistant"
  content: string
  timestamp: number | string
}

export type ConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "error"

export interface AgentInfo {
  id: string
  name: string
}

export interface ActivityEventEnvelope {
  v: string
  event_id: string
  event_type: string
  timestamp: string
  sequence: number
  session_id: string
  run_id: string
  parent_run_id?: string | null
  agent_id?: string | null
  trace_id?: string | null
  span_id?: string | null
  idempotency_key?: string
  replay?: boolean
  data: Record<string, unknown>
}

export type ActivityRunStatus = "in_progress" | "completed" | "failed"

export interface ActivityRunState {
  runId: string
  status: ActivityRunStatus
  events: ActivityEventEnvelope[]
}

export interface ChatStoreState {
  messages: ChatMessage[]
  connectionState: ConnectionState
  isTyping: boolean
  typingStatus: string
  activeSessionId: string
  hasHydratedActiveSession: boolean
  agents: AgentInfo[]
  activeAgentId: string
  activityRuns: Record<string, ActivityRunState>
  activeRunId: string
}

type ChatStorePatch = Partial<ChatStoreState>

const DEFAULT_CHAT_STATE: ChatStoreState = {
  messages: [],
  connectionState: "disconnected",
  isTyping: false,
  typingStatus: "",
  activeSessionId: getInitialActiveSessionId(),
  hasHydratedActiveSession: false,
  agents: [],
  activeAgentId: "",
  activityRuns: {},
  activeRunId: "",
}

export const chatAtom = atom<ChatStoreState>(DEFAULT_CHAT_STATE)

const store = getDefaultStore()

export function getChatState() {
  return store.get(chatAtom)
}

export function updateChatStore(
  patch:
    | ChatStorePatch
    | ((prev: ChatStoreState) => ChatStorePatch | ChatStoreState),
) {
  store.set(chatAtom, (prev) => {
    const nextPatch = typeof patch === "function" ? patch(prev) : patch
    const next = { ...prev, ...nextPatch }

    if (next.activeSessionId !== prev.activeSessionId) {
      writeStoredSessionId(next.activeSessionId)
    }

    return next
  })
}
