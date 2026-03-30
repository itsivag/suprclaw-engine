import { getDefaultStore } from "jotai"
import { toast } from "sonner"

import { getPicoToken } from "@/api/pico"
import {
  loadSessionMessages,
  mergeHistoryMessages,
} from "@/features/chat/history"
import { type PicoMessage, handlePicoMessage } from "@/features/chat/protocol"
import {
  clearStoredSessionId,
  generateSessionId,
  readStoredSessionId,
} from "@/features/chat/state"
import {
  invalidateSocket,
  isCurrentSocket,
  normalizeWsUrlForBrowser,
} from "@/features/chat/websocket"
import i18n from "@/i18n"
import { getChatState, updateChatStore } from "@/store/chat"
import { type GatewayState, gatewayAtom } from "@/store/gateway"

const store = getDefaultStore()

let wsRef: WebSocket | null = null
let isConnecting = false
let msgIdCounter = 0
let activeSessionIdRef = getChatState().activeSessionId
let activeAgentIdRef = ""
let initialized = false
let unsubscribeGateway: (() => void) | null = null
let hydratePromise: Promise<void> | null = null
let connectionGeneration = 0
let reconnectTimer: number | null = null
let reconnectAttempts = 0
let shouldMaintainConnection = false
let runStatusWatchdogTimer: number | null = null
let lastRunStatusProbeAt = 0
let lastRunStatusProbeKey = ""

const RUN_STATUS_INACTIVITY_MS = 10_000
const RUN_STATUS_WATCHDOG_INTERVAL_MS = 1_000
const RUN_STATUS_PROBE_COOLDOWN_MS = 5_000

function clearReconnectTimer() {
  if (reconnectTimer !== null) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
}

function clearRunStatusWatchdog() {
  if (runStatusWatchdogTimer !== null) {
    window.clearInterval(runStatusWatchdogTimer)
    runStatusWatchdogTimer = null
  }
}

function sendRunStatusGet(
  socket: WebSocket,
  sessionId: string,
  runId?: string,
): void {
  if (socket.readyState !== WebSocket.OPEN) {
    return
  }
  const payload: Record<string, unknown> = {}
  if (runId) {
    payload.run_id = runId
  }
  socket.send(
    JSON.stringify({
      type: "run.status.get",
      id: `run-status-${Date.now()}`,
      session_id: sessionId,
      payload,
    }),
  )
}

function startRunStatusWatchdog({
  socket,
  generation,
  sessionId,
}: {
  socket: WebSocket
  generation: number
  sessionId: string
}) {
  clearRunStatusWatchdog()
  runStatusWatchdogTimer = window.setInterval(() => {
    if (
      !isCurrentSocket({
        socket,
        currentSocket: wsRef,
        generation,
        currentGeneration: connectionGeneration,
        sessionId,
        currentSessionId: activeSessionIdRef,
      })
    ) {
      clearRunStatusWatchdog()
      return
    }

    const state = getChatState()
    const activeRunId = state.activeRunId
    if (!activeRunId) {
      lastRunStatusProbeAt = 0
      lastRunStatusProbeKey = ""
      return
    }
    const run = state.activityRuns[activeRunId]
    if (!run || run.status !== "in_progress") {
      return
    }
    const now = Date.now()
    if (now - run.lastEventAt < RUN_STATUS_INACTIVITY_MS) {
      return
    }
    const probeKey = `${sessionId}:${activeRunId}`
    if (
      probeKey === lastRunStatusProbeKey &&
      now-lastRunStatusProbeAt < RUN_STATUS_PROBE_COOLDOWN_MS
    ) {
      return
    }
    sendRunStatusGet(socket, sessionId, activeRunId)
    lastRunStatusProbeKey = probeKey
    lastRunStatusProbeAt = now
  }, RUN_STATUS_WATCHDOG_INTERVAL_MS)
}

function shouldReconnectFor(generation: number, sessionId: string): boolean {
  return (
    shouldMaintainConnection &&
    generation === connectionGeneration &&
    sessionId === activeSessionIdRef &&
    store.get(gatewayAtom).status === "running"
  )
}

function scheduleReconnect(generation: number, sessionId: string) {
  if (!shouldReconnectFor(generation, sessionId) || reconnectTimer !== null) {
    return
  }

  const delay = Math.min(1000 * 2 ** reconnectAttempts, 5000)
  reconnectAttempts += 1
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = null
    if (!shouldReconnectFor(generation, sessionId)) {
      return
    }
    void connectChat()
  }, delay)
}

function needsActiveSessionHydration(): boolean {
  const state = getChatState()
  const storedSessionId = readStoredSessionId()

  return Boolean(
    storedSessionId &&
    storedSessionId === state.activeSessionId &&
    !state.hasHydratedActiveSession,
  )
}

function setActiveSessionId(sessionId: string) {
  activeSessionIdRef = sessionId
  updateChatStore({ activeSessionId: sessionId })
}

export function setActiveAgent(agentId: string) {
  activeAgentIdRef = agentId
  updateChatStore({ activeAgentId: agentId })
}

function disconnectChatInternal({
  clearDesiredConnection,
}: {
  clearDesiredConnection: boolean
}) {
  connectionGeneration += 1
  clearReconnectTimer()
  clearRunStatusWatchdog()
  lastRunStatusProbeAt = 0
  lastRunStatusProbeKey = ""

  if (clearDesiredConnection) {
    shouldMaintainConnection = false
  }

  const socket = wsRef
  wsRef = null
  isConnecting = false

  invalidateSocket(socket)

  updateChatStore({
    connectionState: "disconnected",
    isTyping: false,
    typingStatus: "",
    activeRunId: "",
    activityRuns: {},
  })
}

export async function connectChat() {
  if (
    store.get(gatewayAtom).status !== "running" ||
    needsActiveSessionHydration()
  ) {
    return
  }

  if (
    isConnecting ||
    (wsRef &&
      (wsRef.readyState === WebSocket.OPEN ||
        wsRef.readyState === WebSocket.CONNECTING))
  ) {
    return
  }

  const generation = connectionGeneration + 1
  connectionGeneration = generation
  isConnecting = true
  clearReconnectTimer()
  updateChatStore({ connectionState: "connecting" })

  try {
    const { token, ws_url } = await getPicoToken()
    const sessionId = activeSessionIdRef

    if (generation !== connectionGeneration) {
      isConnecting = false
      return
    }

    if (!token) {
      console.error("No pico token available")
      updateChatStore({ connectionState: "error" })
      isConnecting = false
      scheduleReconnect(generation, sessionId)
      return
    }

    const finalWsUrl = normalizeWsUrlForBrowser(ws_url)
    const url = `${finalWsUrl}?session_id=${encodeURIComponent(sessionId)}`
    const socket = new WebSocket(url, [`token.${token}`])

    if (generation !== connectionGeneration) {
      isConnecting = false
      invalidateSocket(socket)
      return
    }

    socket.onopen = () => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }
      updateChatStore({ connectionState: "connected" })
      isConnecting = false
      reconnectAttempts = 0
      const activeRunId = getChatState().activeRunId
      sendRunStatusGet(socket, sessionId, activeRunId || undefined)
      startRunStatusWatchdog({ socket, generation, sessionId })
    }

    socket.onmessage = (event) => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }

      try {
        const message = JSON.parse(event.data) as PicoMessage
        handlePicoMessage(message, sessionId)
      } catch {
        console.warn("Non-JSON message from pico:", event.data)
      }
    }

    socket.onclose = () => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }
      wsRef = null
      isConnecting = false
      clearRunStatusWatchdog()
      updateChatStore({
        connectionState: "disconnected",
        isTyping: false,
        typingStatus: "",
      })
      scheduleReconnect(generation, sessionId)
    }

    socket.onerror = () => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }
      isConnecting = false
      clearRunStatusWatchdog()
      updateChatStore({ connectionState: "error" })
      scheduleReconnect(generation, sessionId)
    }

    wsRef = socket
  } catch (error) {
    if (generation !== connectionGeneration) {
      isConnecting = false
      return
    }
    console.error("Failed to connect to pico:", error)
    updateChatStore({ connectionState: "error" })
    isConnecting = false
    scheduleReconnect(generation, activeSessionIdRef)
  }
}

export function disconnectChat() {
  disconnectChatInternal({ clearDesiredConnection: true })
}

export async function hydrateActiveSession() {
  if (hydratePromise) {
    return hydratePromise
  }

  const state = getChatState()
  const storedSessionId = readStoredSessionId()

  if (
    !storedSessionId ||
    state.hasHydratedActiveSession ||
    storedSessionId !== state.activeSessionId
  ) {
    if (!state.hasHydratedActiveSession) {
      updateChatStore({ hasHydratedActiveSession: true })
    }
    return
  }

  hydratePromise = loadSessionMessages(storedSessionId)
    .then((historyMessages) => {
      const currentState = getChatState()
      if (currentState.activeSessionId !== storedSessionId) {
        return
      }

      if (currentState.messages.length > 0) {
        updateChatStore({
          messages: mergeHistoryMessages(
            historyMessages,
            currentState.messages,
          ),
          hasHydratedActiveSession: true,
          activeRunId: "",
          activityRuns: {},
        })
        return
      }

      updateChatStore({
        messages: historyMessages,
        isTyping: false,
        hasHydratedActiveSession: true,
        typingStatus: "",
        activeRunId: "",
        activityRuns: {},
      })
    })
    .catch((error) => {
      console.error("Failed to restore last session history:", error)

      const currentState = getChatState()
      if (currentState.activeSessionId !== storedSessionId) {
        return
      }

      if (currentState.messages.length > 0) {
        updateChatStore({ hasHydratedActiveSession: true })
        return
      }

      clearStoredSessionId()
      updateChatStore({
        messages: [],
        isTyping: false,
        hasHydratedActiveSession: true,
        typingStatus: "",
        activeRunId: "",
        activityRuns: {},
      })
    })
    .finally(() => {
      hydratePromise = null
    })

  return hydratePromise
}

export function sendChatMessage(content: string) {
  if (!wsRef || wsRef.readyState !== WebSocket.OPEN) {
    console.warn("WebSocket not connected")
    return false
  }

  const socket = wsRef
  const id = `msg-${++msgIdCounter}-${Date.now()}`

  updateChatStore((prev) => ({
    messages: [
      ...prev.messages,
      { id, role: "user", content, timestamp: Date.now() },
    ],
    isTyping: true,
    activeRunId:
      prev.activeRunId &&
      prev.activityRuns[prev.activeRunId]?.status === "in_progress"
        ? prev.activeRunId
        : "",
  }))

  try {
    const payload: Record<string, unknown> = { content }
    if (activeAgentIdRef) payload.agent_id = activeAgentIdRef
    socket.send(
      JSON.stringify({
        type: "message.send",
        id,
        payload,
      }),
    )
    return true
  } catch (error) {
    console.error("Failed to send pico message:", error)
    updateChatStore((prev) => ({
      messages: prev.messages.filter((message) => message.id !== id),
      isTyping: false,
      typingStatus: "",
      activeRunId: "",
      activityRuns: {},
    }))
    return false
  }
}

export async function switchChatSession(sessionId: string) {
  if (sessionId === activeSessionIdRef) {
    return
  }

  try {
    const historyMessages = await loadSessionMessages(sessionId)

    disconnectChatInternal({ clearDesiredConnection: false })
    setActiveSessionId(sessionId)
    updateChatStore({
      messages: historyMessages,
      isTyping: false,
      hasHydratedActiveSession: true,
      typingStatus: "",
      activeRunId: "",
      activityRuns: {},
    })

    if (store.get(gatewayAtom).status === "running") {
      shouldMaintainConnection = true
      await connectChat()
    }
  } catch (error) {
    console.error("Failed to load session history:", error)
    toast.error(i18n.t("chat.historyOpenFailed"))
  }
}

export async function newChatSession() {
  if (getChatState().messages.length === 0) {
    return
  }

  disconnectChatInternal({ clearDesiredConnection: false })
  setActiveSessionId(generateSessionId())
  activeAgentIdRef = ""
  updateChatStore({
    messages: [],
    isTyping: false,
    hasHydratedActiveSession: true,
    activeAgentId: "",
    typingStatus: "",
    activeRunId: "",
    activityRuns: {},
  })

  if (store.get(gatewayAtom).status === "running") {
    shouldMaintainConnection = true
    await connectChat()
  }
}

export function initializeChatStore() {
  if (initialized) {
    return
  }

  initialized = true
  activeSessionIdRef = getChatState().activeSessionId
  let lastGatewayStatus: GatewayState | null = null

  const syncConnectionWithGateway = (force: boolean = false) => {
    const gatewayStatus = store.get(gatewayAtom).status
    if (!force && gatewayStatus === lastGatewayStatus) {
      return
    }
    lastGatewayStatus = gatewayStatus

    if (gatewayStatus === "running") {
      shouldMaintainConnection = true
      if (needsActiveSessionHydration()) {
        return
      }
      void connectChat()
      return
    }

    if (gatewayStatus === "stopped" || gatewayStatus === "error") {
      disconnectChatInternal({ clearDesiredConnection: true })
    }
  }

  unsubscribeGateway = store.sub(gatewayAtom, syncConnectionWithGateway)

  if (!readStoredSessionId()) {
    updateChatStore({ hasHydratedActiveSession: true })
    syncConnectionWithGateway(true)
    return
  }

  void hydrateActiveSession().finally(() => {
    if (!initialized) {
      return
    }
    syncConnectionWithGateway(true)
  })
}

export function teardownChatStore() {
  unsubscribeGateway?.()
  unsubscribeGateway = null
  initialized = false
  disconnectChat()
}
