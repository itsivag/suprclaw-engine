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

export interface ChatStoreState {
  messages: ChatMessage[]
  connectionState: ConnectionState
  isTyping: boolean
  typingStatus: string
  activeSessionId: string
  hasHydratedActiveSession: boolean
  agents: AgentInfo[]
  activeAgentId: string
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
