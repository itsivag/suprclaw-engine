import { badgeForState, buildTargets, normalizeRelayURL } from "./relay-core.js";
import {
  relayPairingURL,
  relaySessionStopURL,
  relaySetupURL,
  relayStatusURL,
  validateRelayRequest
} from "./relay-protocol.js";

const STORAGE_KEY = "suprclawRelaySettings";
const RELAY_SUBPROTOCOL = "suprclaw-relay";
const DEFAULT_SETTINGS = {
  relayUrl: "ws://127.0.0.1:18800/browser-relay/extension",
  token: "",
  desiredConnected: false
};

let settings = { ...DEFAULT_SETTINGS };
let socket = null;
let heartbeatTimer = null;
let reconnectTimer = null;
let reconnectAttempts = 0;
let connectInFlight = null;
let connectionState = "disconnected";
let hardStoppedByServer = false;
const attachedTargets = {};

async function loadSettings() {
  const data = await chrome.storage.sync.get(STORAGE_KEY);
  settings = { ...DEFAULT_SETTINGS, ...(data[STORAGE_KEY] || {}) };
  settings.relayUrl = normalizeRelayURL(settings.relayUrl);
  settings.desiredConnected = Boolean(settings.desiredConnected);
}

async function saveSettings(nextSettings) {
  settings = {
    ...settings,
    ...nextSettings,
    relayUrl: normalizeRelayURL(nextSettings.relayUrl || settings.relayUrl),
    desiredConnected: Boolean(
      typeof nextSettings.desiredConnected === "boolean"
        ? nextSettings.desiredConnected
        : settings.desiredConnected
    )
  };
  await chrome.storage.sync.set({ [STORAGE_KEY]: settings });
}

function setBadge(state) {
  connectionState = state;
  const badge = badgeForState(state);
  chrome.action.setBadgeText({ text: badge.text });
  chrome.action.setBadgeBackgroundColor({ color: badge.color });
}

function clearHeartbeat() {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer);
    heartbeatTimer = null;
  }
}

function cancelReconnect() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
}

function nextReconnectDelayMs() {
  const base = Math.min(30000, 1000 * Math.pow(2, Math.min(reconnectAttempts, 5)));
  const jitter = Math.floor(Math.random() * 700);
  return base + jitter;
}

function scheduleReconnect() {
  if (!settings.desiredConnected || hardStoppedByServer) {
    return;
  }
  if (reconnectTimer || (socket && socket.readyState === WebSocket.OPEN)) {
    return;
  }
  const delay = nextReconnectDelayMs();
  reconnectTimer = setTimeout(async () => {
    reconnectTimer = null;
    reconnectAttempts += 1;
    await connectRelay({ persistDesired: false }).catch(() => {
      // retry scheduling happens in close handler
    });
  }, delay);
}

async function publishTargets(kind = "heartbeat") {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }
  const tabs = await chrome.tabs.query({});
  const targets = buildTargets(tabs, attachedTargets);
  socket.send(JSON.stringify({ type: kind, targets }));
}

function startHeartbeat() {
  clearHeartbeat();
  heartbeatTimer = setInterval(() => {
    publishTargets("heartbeat").catch(() => {
      // reflected by websocket close
    });
  }, 10000);
}

async function disconnectRelay({ persistDesired = true } = {}) {
  cancelReconnect();
  clearHeartbeat();
  if (persistDesired) {
    await saveSettings({ desiredConnected: false });
  }
  if (socket) {
    try {
      socket.close(1000, "disconnect");
    } catch {
      // ignore
    }
  }
  socket = null;
  setBadge("disconnected");
}

function isHardStopClose(event) {
  const reason = String(event?.reason || "").toLowerCase();
  if (reason.includes("hard_stop") || reason.includes("hard stop")) {
    return true;
  }
  return false;
}

async function autoSetupRelay({ relayUrl, tokenHint }) {
  const setupURL = relaySetupURL(relayUrl);
  const headers = {};
  if (tokenHint) {
    headers.Authorization = `Bearer ${tokenHint}`;
  }

  try {
    const resp = await fetch(setupURL, {
      method: "POST",
      headers,
      credentials: "include"
    });
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      return {
        ok: false,
        status: resp.status,
        error: data.error || "setup failed",
        url: setupURL
      };
    }

    const nextRelayUrl = normalizeRelayURL(data.extension_ws_url || relayUrl);
    const nextToken = String(data.token || tokenHint || "").trim();
    if (!nextToken) {
      return {
        ok: false,
        error: "setup did not return token",
        url: setupURL
      };
    }

    await saveSettings({
      relayUrl: nextRelayUrl,
      token: nextToken
    });
    return {
      ok: true,
      relayUrl: nextRelayUrl,
      hasToken: true,
      token: nextToken,
      setupURL
    };
  } catch (err) {
    return {
      ok: false,
      error: err.message || "setup request failed",
      url: setupURL
    };
  }
}

async function connectRelay({ persistDesired = true } = {}) {
  if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
    return { ok: true, state: connectionState, relayUrl: settings.relayUrl };
  }
  if (connectInFlight) {
    return connectInFlight;
  }

  if (!settings.token) {
    const setupResult = await autoSetupRelay({
      relayUrl: settings.relayUrl,
      tokenHint: ""
    });
    if (!setupResult.ok) {
      setBadge("disconnected");
      return {
        ok: false,
        error: setupResult.error || "token is required",
        status: setupResult.status
      };
    }
  }

  if (!settings.relayUrl) {
    setBadge("disconnected");
    return { ok: false, error: "relay URL is required" };
  }

  if (persistDesired) {
    await saveSettings({ desiredConnected: true });
  }

  hardStoppedByServer = false;
  setBadge("connecting");

  connectInFlight = new Promise((resolve) => {
    const ws = new WebSocket(settings.relayUrl, [RELAY_SUBPROTOCOL, `token.${settings.token}`]);
    let settled = false;

    const settle = (payload) => {
      if (settled) return;
      settled = true;
      connectInFlight = null;
      resolve(payload);
    };

    ws.onopen = async () => {
      socket = ws;
      reconnectAttempts = 0;
      cancelReconnect();
      setBadge("connected");
      startHeartbeat();
      await publishTargets("hello");
      settle({ ok: true, state: "connected", relayUrl: settings.relayUrl });
    };

    ws.onmessage = async (event) => {
      try {
        const message = JSON.parse(event.data);
        await handleRelayMessage(message);
      } catch (err) {
        console.warn("Relay message parse failed", err);
      }
    };

    ws.onerror = () => {
      setBadge("disconnected");
      settle({ ok: false, error: "websocket error" });
    };

    ws.onclose = async (event) => {
      if (socket === ws) {
        socket = null;
      }
      clearHeartbeat();

      if (isHardStopClose(event)) {
        hardStoppedByServer = true;
        await saveSettings({ desiredConnected: false });
        setBadge("stopped");
        settle({ ok: false, error: "relay hard stopped" });
        return;
      }

      setBadge("disconnected");
      settle({ ok: false, error: "websocket closed" });
      if (settings.desiredConnected) {
        scheduleReconnect();
      }
    };
  });

  return connectInFlight;
}

async function relayResponse(requestId, result, error) {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }
  socket.send(JSON.stringify({
    type: "response",
    requestId,
    result,
    error
  }));
}

async function handleRelayMessage(message) {
  const validated = validateRelayRequest(message, attachedTargets);
  if (!validated.ok) {
    if (validated.requestId) {
      await relayResponse(validated.requestId, null, validated.error);
    }
    return;
  }

  const { requestId, method, targetID } = validated;

  if (method === "Target.activateTarget") {
    await chrome.tabs.update(targetID, { active: true });
    await relayResponse(requestId, { ok: true }, "");
    return;
  }

  chrome.debugger.sendCommand(
    { tabId: targetID },
    method,
    message.params || {},
    async (result) => {
      const runtimeError = chrome.runtime.lastError;
      if (runtimeError) {
        await relayResponse(requestId, null, runtimeError.message || "debugger command failed");
        return;
      }
      await relayResponse(requestId, result || {}, "");
    }
  );
}

async function attachCurrentTab() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab || typeof tab.id !== "number") {
    return { ok: false, error: "no active tab" };
  }

  await new Promise((resolve, reject) => {
    chrome.debugger.attach({ tabId: tab.id }, "1.3", () => {
      const runtimeError = chrome.runtime.lastError;
      if (runtimeError) {
        reject(new Error(runtimeError.message || "attach failed"));
      } else {
        resolve();
      }
    });
  });

  attachedTargets[String(tab.id)] = true;
  if (socket && socket.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify({ type: "attach", targetId: String(tab.id) }));
  }
  await publishTargets("targets");
  return { ok: true, targetId: String(tab.id) };
}

async function detachCurrentTab() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab || typeof tab.id !== "number") {
    return { ok: false, error: "no active tab" };
  }

  await new Promise((resolve, reject) => {
    chrome.debugger.detach({ tabId: tab.id }, () => {
      const runtimeError = chrome.runtime.lastError;
      if (runtimeError) {
        reject(new Error(runtimeError.message || "detach failed"));
      } else {
        resolve();
      }
    });
  });

  delete attachedTargets[String(tab.id)];
  if (socket && socket.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify({ type: "detach", targetId: String(tab.id) }));
  }
  await publishTargets("targets");
  return { ok: true, targetId: String(tab.id) };
}

async function hardStopRelay() {
  if (!settings.token) {
    return { ok: false, error: "token is required" };
  }
  const stopUrl = relaySessionStopURL(settings.relayUrl);
  try {
    const resp = await fetch(stopUrl, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${settings.token}`
      },
      credentials: "include"
    });
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      return {
        ok: false,
        status: resp.status,
        error: data.error || "hard stop failed"
      };
    }
    await disconnectRelay({ persistDesired: false });
    await saveSettings({ desiredConnected: false });
    hardStoppedByServer = true;
    setBadge("stopped");
    return { ok: true, ...data };
  } catch (err) {
    return { ok: false, error: err.message || "hard stop request failed" };
  }
}

async function diagnostics() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  return {
    state: connectionState,
    connected: Boolean(socket && socket.readyState === WebSocket.OPEN),
    relayUrl: settings.relayUrl,
    hasToken: Boolean(settings.token),
    desiredConnected: Boolean(settings.desiredConnected),
    hardStopped: hardStoppedByServer,
    activeTabId: tab && typeof tab.id === "number" ? String(tab.id) : "",
    attached: tab && typeof tab.id === "number" ? Boolean(attachedTargets[String(tab.id)]) : false
  };
}

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  (async () => {
    switch (message?.type) {
      case "getState":
        sendResponse({ ok: true, ...(await diagnostics()) });
        break;
      case "saveSettings":
        await saveSettings(message.payload || {});
        sendResponse({ ok: true });
        break;
      case "connect":
        sendResponse(await connectRelay({ persistDesired: true }));
        break;
      case "disconnect":
        await disconnectRelay({ persistDesired: true });
        sendResponse({ ok: true });
        break;
      case "hardStop":
        sendResponse(await hardStopRelay());
        break;
      case "attachCurrentTab":
        sendResponse(await attachCurrentTab());
        break;
      case "detachCurrentTab":
        sendResponse(await detachCurrentTab());
        break;
      case "testConnection": {
        const relayUrl = normalizeRelayURL((message.payload && message.payload.relayUrl) || settings.relayUrl);
        const testToken = (message.payload && message.payload.token) || settings.token;
        const statusURL = relayStatusURL(relayUrl);
        try {
          const resp = await fetch(statusURL, {
            headers: testToken ? { Authorization: `Bearer ${testToken}` } : {},
            credentials: "include"
          });
          sendResponse({ ok: resp.ok, status: resp.status, url: statusURL });
        } catch (err) {
          sendResponse({ ok: false, error: err.message || "connection test failed", url: statusURL });
        }
        break;
      }
      case "autoSetup": {
        const relayUrl = normalizeRelayURL((message.payload && message.payload.relayUrl) || settings.relayUrl);
        const setupToken = String((message.payload && message.payload.token) || settings.token || "").trim();
        sendResponse(await autoSetupRelay({ relayUrl, tokenHint: setupToken }));
        break;
      }
      case "createPairing": {
        const pairingURL = relayPairingURL(settings.relayUrl);
        if (!settings.token) {
          const setupResult = await autoSetupRelay({
            relayUrl: settings.relayUrl,
            tokenHint: ""
          });
          if (!setupResult.ok) {
            sendResponse({
              ok: false,
              status: setupResult.status,
              error: setupResult.error || "token is required before creating QR pairing",
              url: setupResult.url || pairingURL
            });
            break;
          }
        }
        const ttlSeconds = Number(message.payload && message.payload.ttlSeconds) || 180;
        try {
          const resp = await fetch(pairingURL, {
            method: "POST",
            headers: {
              Authorization: `Bearer ${settings.token}`,
              "Content-Type": "application/json"
            },
            credentials: "include",
            body: JSON.stringify({ ttl_seconds: ttlSeconds })
          });
          const data = await resp.json().catch(() => ({}));
          if (!resp.ok) {
            sendResponse({
              ok: false,
              status: resp.status,
              error: data.error || "pairing creation failed",
              url: pairingURL
            });
            break;
          }
          sendResponse({ ok: true, ...data });
        } catch (err) {
          sendResponse({ ok: false, error: err.message || "pairing request failed", url: pairingURL });
        }
        break;
      }
      default:
        sendResponse({ ok: false, error: "unknown message type" });
    }
  })().catch((err) => {
    sendResponse({ ok: false, error: err.message || "unexpected error" });
  });

  return true;
});

chrome.debugger.onEvent.addListener((source, method, params) => {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }
  if (!source || typeof source.tabId !== "number") {
    return;
  }
  socket.send(JSON.stringify({
    type: "event",
    targetId: String(source.tabId),
    method,
    params
  }));
});

chrome.debugger.onDetach.addListener((source) => {
  if (!source || typeof source.tabId !== "number") {
    return;
  }
  delete attachedTargets[String(source.tabId)];
  if (socket && socket.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify({ type: "detached", targetId: String(source.tabId) }));
  }
});

async function bootstrap() {
  await loadSettings();
  if (settings.desiredConnected) {
    await connectRelay({ persistDesired: false });
    return;
  }
  setBadge("disconnected");
}

chrome.runtime.onInstalled.addListener(async () => {
  await bootstrap();
});

chrome.runtime.onStartup.addListener(async () => {
  await bootstrap();
});

bootstrap();
