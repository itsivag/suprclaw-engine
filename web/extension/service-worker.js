import { badgeForState, buildTargets, normalizeRelayURL } from "./relay-core.js";
import { relayStatusURL, validateRelayRequest } from "./relay-protocol.js";

const STORAGE_KEY = "suprclawRelaySettings";
const DEFAULT_SETTINGS = {
  relayUrl: "ws://127.0.0.1:18800/browser-relay/extension",
  token: ""
};

let settings = { ...DEFAULT_SETTINGS };
let socket = null;
let heartbeatTimer = null;
let connectionState = "disconnected";
const attachedTargets = {};

async function loadSettings() {
  const data = await chrome.storage.sync.get(STORAGE_KEY);
  settings = { ...DEFAULT_SETTINGS, ...(data[STORAGE_KEY] || {}) };
  settings.relayUrl = normalizeRelayURL(settings.relayUrl);
}

async function saveSettings(nextSettings) {
  settings = {
    ...settings,
    ...nextSettings,
    relayUrl: normalizeRelayURL(nextSettings.relayUrl || settings.relayUrl)
  };
  await chrome.storage.sync.set({ [STORAGE_KEY]: settings });
}

function setBadge(state) {
  connectionState = state;
  const badge = badgeForState(state);
  chrome.action.setBadgeText({ text: badge.text });
  chrome.action.setBadgeBackgroundColor({ color: badge.color });
}

async function publishTargets(kind = "heartbeat") {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }
  const tabs = await chrome.tabs.query({});
  const targets = buildTargets(tabs, attachedTargets);
  socket.send(JSON.stringify({ type: kind, targets }));
}

function clearHeartbeat() {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer);
    heartbeatTimer = null;
  }
}

function startHeartbeat() {
  clearHeartbeat();
  heartbeatTimer = setInterval(() => {
    publishTargets("heartbeat").catch(() => {
      // heartbeat failures are reflected by socket close/errors
    });
  }, 10000);
}

function disconnectRelay() {
  clearHeartbeat();
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

async function connectRelay() {
  if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
    return { ok: true, state: connectionState, relayUrl: settings.relayUrl };
  }

  if (!settings.token) {
    setBadge("disconnected");
    return { ok: false, error: "token is required" };
  }

  if (!settings.relayUrl) {
    setBadge("disconnected");
    return { ok: false, error: "relay URL is required" };
  }

  setBadge("connecting");

  return new Promise((resolve) => {
    const ws = new WebSocket(settings.relayUrl, [`token.${settings.token}`]);

    ws.onopen = async () => {
      socket = ws;
      setBadge("connected");
      startHeartbeat();
      await publishTargets("hello");
      resolve({ ok: true, state: "connected", relayUrl: settings.relayUrl });
    };

    ws.onmessage = async (event) => {
      try {
        const message = JSON.parse(event.data);
        await handleRelayMessage(message);
      } catch (err) {
        // invalid relay payload, ignore
        console.warn("Relay message parse failed", err);
      }
    };

    ws.onerror = () => {
      setBadge("disconnected");
      resolve({ ok: false, error: "websocket error" });
    };

    ws.onclose = () => {
      if (socket === ws) {
        socket = null;
      }
      clearHeartbeat();
      setBadge("disconnected");
    };
  });
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

async function diagnostics() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  return {
    state: connectionState,
    connected: Boolean(socket && socket.readyState === WebSocket.OPEN),
    relayUrl: settings.relayUrl,
    hasToken: Boolean(settings.token),
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
        sendResponse(await connectRelay());
        break;
      case "disconnect":
        disconnectRelay();
        sendResponse({ ok: true });
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
            headers: testToken ? { Authorization: `Bearer ${testToken}` } : {}
          });
          sendResponse({ ok: resp.ok, status: resp.status, url: statusURL });
        } catch (err) {
          sendResponse({ ok: false, error: err.message || "connection test failed", url: statusURL });
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

chrome.runtime.onInstalled.addListener(async () => {
  await loadSettings();
  setBadge("disconnected");
});

chrome.runtime.onStartup.addListener(async () => {
  await loadSettings();
  setBadge("disconnected");
});

loadSettings().then(() => setBadge("disconnected"));
