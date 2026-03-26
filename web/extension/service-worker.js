import { badgeForState, buildTargets, normalizeRelayURL } from "./relay-core.js";
import {
  relayAuthGoogleExtensionURL,
  relayBootstrapTokenURL,
  relayPairingURL,
  relaySessionStopURL,
  relaySetupURL,
  relayStatusURL,
  validateRelayRequest
} from "./relay-protocol.js";

const STORAGE_KEY = "suprclawRelaySettings";
const ATTACHED_TARGETS_STORAGE_KEY = "suprclawAttachedTargets";
const ATTACHED_TARGET_TTL_MS = 1000 * 60 * 60 * 24 * 7;
const RELAY_SUBPROTOCOL = "suprclaw-relay";
const CLOUD_RELAY_DEFAULT_URL = "wss://api.suprclaw.com/browser-relay/extension";
const LEGACY_LOCAL_DEFAULT_URL = "ws://127.0.0.1:18800/browser-relay/extension";
const DEFAULT_SETTINGS = {
  relayUrl: CLOUD_RELAY_DEFAULT_URL,
  token: "",
  firebaseAuthToken: "",
  firebaseAuthExpiresAtEpochSeconds: 0,
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
const attachedTargetLease = {};

function nowMs() {
  return Date.now();
}

function normalizeTargetId(targetId) {
  return String(targetId || "").trim();
}

function clearInMemoryAttachedTargets() {
  for (const key of Object.keys(attachedTargets)) {
    delete attachedTargets[key];
  }
  for (const key of Object.keys(attachedTargetLease)) {
    delete attachedTargetLease[key];
  }
}

function setAttachedTarget(targetId, ttlMs = ATTACHED_TARGET_TTL_MS) {
  const key = normalizeTargetId(targetId);
  if (!key) return;
  const expiresAtEpochMs = nowMs() + Math.max(30000, Number(ttlMs) || ATTACHED_TARGET_TTL_MS);
  attachedTargets[key] = true;
  attachedTargetLease[key] = { expiresAtEpochMs };
}

function clearAttachedTarget(targetId) {
  const key = normalizeTargetId(targetId);
  if (!key) return;
  delete attachedTargets[key];
  delete attachedTargetLease[key];
}

function pruneExpiredAttachedTargets() {
  const ts = nowMs();
  let changed = false;
  for (const [targetId, lease] of Object.entries(attachedTargetLease)) {
    const expiresAtEpochMs = Number(lease && lease.expiresAtEpochMs) || 0;
    if (expiresAtEpochMs <= ts) {
      clearAttachedTarget(targetId);
      changed = true;
    }
  }
  return changed;
}

function snapshotAttachedTargetsForStorage() {
  const ts = nowMs();
  const out = {};
  for (const [targetId, lease] of Object.entries(attachedTargetLease)) {
    const expiresAtEpochMs = Number(lease && lease.expiresAtEpochMs) || 0;
    if (expiresAtEpochMs > ts) {
      out[targetId] = { expiresAtEpochMs };
    }
  }
  return out;
}

async function persistAttachedTargets() {
  await chrome.storage.local.set({
    [ATTACHED_TARGETS_STORAGE_KEY]: snapshotAttachedTargetsForStorage()
  });
}

async function loadAttachedTargets() {
  clearInMemoryAttachedTargets();
  const data = await chrome.storage.local.get(ATTACHED_TARGETS_STORAGE_KEY);
  const stored = data[ATTACHED_TARGETS_STORAGE_KEY] || {};
  const ts = nowMs();
  for (const [targetIdRaw, lease] of Object.entries(stored)) {
    const targetId = normalizeTargetId(targetIdRaw);
    const expiresAtEpochMs = Number(lease && lease.expiresAtEpochMs) || 0;
    if (!targetId || expiresAtEpochMs <= ts) {
      continue;
    }
    attachedTargets[targetId] = true;
    attachedTargetLease[targetId] = { expiresAtEpochMs };
  }
  await persistAttachedTargets();
}

function debuggerAttach(tabId) {
  return new Promise((resolve, reject) => {
    chrome.debugger.attach({ tabId }, "1.3", () => {
      const runtimeError = chrome.runtime.lastError;
      if (runtimeError) {
        reject(new Error(runtimeError.message || "attach failed"));
      } else {
        resolve();
      }
    });
  });
}

function debuggerDetach(tabId) {
  return new Promise((resolve, reject) => {
    chrome.debugger.detach({ tabId }, () => {
      const runtimeError = chrome.runtime.lastError;
      if (runtimeError) {
        reject(new Error(runtimeError.message || "detach failed"));
      } else {
        resolve();
      }
    });
  });
}

function debuggerGetTargets() {
  return new Promise((resolve, reject) => {
    chrome.debugger.getTargets((targets) => {
      const runtimeError = chrome.runtime.lastError;
      if (runtimeError) {
        reject(new Error(runtimeError.message || "getTargets failed"));
        return;
      }
      resolve(Array.isArray(targets) ? targets : []);
    });
  });
}

function canAttachToTab(tab) {
  const tabUrl = String(tab && tab.url ? tab.url : "");
  if (!tabUrl) return true;
  return !(
    tabUrl.startsWith("chrome://") ||
    tabUrl.startsWith("chrome-extension://") ||
    tabUrl.startsWith("edge://") ||
    tabUrl.startsWith("about:")
  );
}

async function restoreAttachedTargets() {
  if (pruneExpiredAttachedTargets()) {
    await persistAttachedTargets();
  }
  const targetIds = Object.keys(attachedTargets);
  if (!targetIds.length) return;

  const [tabs, debuggerTargets] = await Promise.all([
    chrome.tabs.query({}),
    debuggerGetTargets().catch(() => [])
  ]);
  const tabById = new Map(tabs.map((tab) => [tab.id, tab]));
  const debuggerByTabId = new Map();
  for (const target of debuggerTargets) {
    if (typeof target.tabId === "number") {
      debuggerByTabId.set(target.tabId, target);
    }
  }

  let changed = false;
  for (const targetId of targetIds) {
    const tabId = Number(targetId);
    if (!Number.isFinite(tabId)) {
      clearAttachedTarget(targetId);
      changed = true;
      continue;
    }
    const tab = tabById.get(tabId);
    if (!tab || !canAttachToTab(tab)) {
      clearAttachedTarget(targetId);
      changed = true;
      continue;
    }

    const dbg = debuggerByTabId.get(tabId);
    if (dbg && dbg.attached) {
      continue;
    }

    try {
      await debuggerAttach(tabId);
    } catch (err) {
      const message = String(err && err.message ? err.message : "");
      if (message.toLowerCase().includes("already attached")) {
        continue;
      }
      clearAttachedTarget(targetId);
      changed = true;
    }
  }
  if (changed) {
    await persistAttachedTargets();
  }
}

async function loadSettings() {
  const data = await chrome.storage.sync.get(STORAGE_KEY);
  const stored = data[STORAGE_KEY] || {};
  settings = { ...DEFAULT_SETTINGS, ...stored };
  settings.relayUrl = normalizeRelayURL(settings.relayUrl);
  settings.firebaseAuthToken = String(settings.firebaseAuthToken || "").trim();
  settings.firebaseAuthExpiresAtEpochSeconds = Number(settings.firebaseAuthExpiresAtEpochSeconds) || 0;
  settings.desiredConnected = Boolean(settings.desiredConnected);
  if (!hasUsableFirebaseAuthToken()) {
    settings.firebaseAuthToken = "";
    settings.firebaseAuthExpiresAtEpochSeconds = 0;
  }

  // Upgrade older extension installs that persisted the localhost relay default.
  const normalizedLegacyDefault = normalizeRelayURL(LEGACY_LOCAL_DEFAULT_URL);
  if (settings.relayUrl === normalizedLegacyDefault) {
    settings.relayUrl = normalizeRelayURL(CLOUD_RELAY_DEFAULT_URL);
    await chrome.storage.sync.set({ [STORAGE_KEY]: settings });
  }
}

async function saveSettings(nextSettings) {
  settings = {
    ...settings,
    ...nextSettings,
    relayUrl: normalizeRelayURL(nextSettings.relayUrl || settings.relayUrl),
    firebaseAuthToken: String(
      typeof nextSettings.firebaseAuthToken === "string"
        ? nextSettings.firebaseAuthToken
        : settings.firebaseAuthToken || ""
    ).trim(),
    firebaseAuthExpiresAtEpochSeconds: Number(
      typeof nextSettings.firebaseAuthExpiresAtEpochSeconds === "number"
        ? nextSettings.firebaseAuthExpiresAtEpochSeconds
        : settings.firebaseAuthExpiresAtEpochSeconds || 0
    ) || 0,
    desiredConnected: Boolean(
      typeof nextSettings.desiredConnected === "boolean"
        ? nextSettings.desiredConnected
        : settings.desiredConnected
    )
  };
  await chrome.storage.sync.set({ [STORAGE_KEY]: settings });
}

function decodeJwtExpEpoch(token) {
  const raw = String(token || "").trim();
  if (!raw) return 0;
  const parts = raw.split(".");
  if (parts.length < 2) return 0;
  try {
    const normalized = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    const payloadJson = atob(normalized);
    const payload = JSON.parse(payloadJson);
    return Number(payload.exp) || 0;
  } catch {
    return 0;
  }
}

function hasUsableFirebaseAuthToken() {
  if (!settings.firebaseAuthToken) return false;
  const exp = Number(settings.firebaseAuthExpiresAtEpochSeconds) || 0;
  if (!exp) return true;
  const now = Math.floor(Date.now() / 1000);
  return exp > (now + 30);
}

function launchWebAuthFlow(url, interactive) {
  return new Promise((resolve, reject) => {
    chrome.identity.launchWebAuthFlow(
      { url, interactive: Boolean(interactive) },
      (responseURL) => {
        const runtimeError = chrome.runtime.lastError;
        if (runtimeError) {
          reject(new Error(runtimeError.message || "launchWebAuthFlow failed"));
          return;
        }
        resolve(String(responseURL || ""));
      }
    );
  });
}

async function signInWithGoogle({ relayUrl, interactive = true } = {}) {
  const targetRelayUrl = normalizeRelayURL(relayUrl || settings.relayUrl);
  const redirectUri = chrome.identity.getRedirectURL("firebase-google");
  const authURL = relayAuthGoogleExtensionURL(targetRelayUrl, redirectUri);
  try {
    const responseURL = await launchWebAuthFlow(authURL, interactive);
    if (!responseURL) {
      return { ok: false, error: "no response URL from auth flow", url: authURL };
    }
    const parsed = new URL(responseURL);
    const hash = parsed.hash.startsWith("#") ? parsed.hash.slice(1) : parsed.hash;
    const params = new URLSearchParams(hash);
    const authError = String(params.get("error") || "").trim();
    if (authError) {
      return { ok: false, error: authError, url: authURL };
    }
    const idToken = String(params.get("id_token") || "").trim();
    if (!idToken) {
      return { ok: false, error: "missing id_token in auth response", url: authURL };
    }
    const expiry = decodeJwtExpEpoch(idToken);
    await saveSettings({
      relayUrl: targetRelayUrl,
      firebaseAuthToken: idToken,
      firebaseAuthExpiresAtEpochSeconds: expiry
    });
    return {
      ok: true,
      hasFirebaseAuthToken: true,
      firebaseAuthExpiresAtEpochSeconds: expiry
    };
  } catch (err) {
    return {
      ok: false,
      error: err.message || "google sign-in failed",
      url: authURL
    };
  }
}

async function signOutGoogle() {
  await saveSettings({
    firebaseAuthToken: "",
    firebaseAuthExpiresAtEpochSeconds: 0
  });
  return { ok: true };
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
  if (pruneExpiredAttachedTargets()) {
    await persistAttachedTargets();
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
  let bootstrapToken = String(tokenHint || "").trim();

  if (!bootstrapToken) {
    const bootstrapAuthToken = hasUsableFirebaseAuthToken() ? settings.firebaseAuthToken : "";
    const bootstrapURL = relayBootstrapTokenURL(relayUrl);
    try {
      const bootstrapResp = await fetch(bootstrapURL, {
        method: "POST",
        credentials: "include",
        headers: {
          "Content-Type": "application/json",
          ...(bootstrapAuthToken ? { Authorization: `Bearer ${bootstrapAuthToken}` } : {})
        },
        body: JSON.stringify({ ttl_seconds: 120 })
      });
      const bootstrapData = await bootstrapResp.json().catch(() => ({}));
      if (!bootstrapResp.ok) {
        const unauthorizedHint = bootstrapResp.status === 401
          ? "unauthorized (sign in with Google first)"
          : "";
        return {
          ok: false,
          status: bootstrapResp.status,
          error: bootstrapData.error || unauthorizedHint || "bootstrap token request failed",
          url: bootstrapURL
        };
      }
      bootstrapToken = String(bootstrapData.bootstrap_token || "").trim();
      if (!bootstrapToken) {
        return {
          ok: false,
          error: "bootstrap token response missing token",
          url: bootstrapURL
        };
      }
    } catch (err) {
      return {
        ok: false,
        error: err.message || "bootstrap token request failed",
        url: bootstrapURL
      };
    }
  }

  if (bootstrapToken) {
    headers.Authorization = `Bearer ${bootstrapToken}`;
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

  await debuggerAttach(tab.id);
  setAttachedTarget(String(tab.id));
  await persistAttachedTargets();
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

  await debuggerDetach(tab.id);
  clearAttachedTarget(String(tab.id));
  await persistAttachedTargets();
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
    hasFirebaseAuthToken: Boolean(settings.firebaseAuthToken),
    firebaseAuthExpiresAtEpochSeconds: Number(settings.firebaseAuthExpiresAtEpochSeconds) || 0,
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
      case "authGoogleSignIn": {
        const relayUrl = normalizeRelayURL((message.payload && message.payload.relayUrl) || settings.relayUrl);
        sendResponse(await signInWithGoogle({ relayUrl, interactive: true }));
        break;
      }
      case "authGoogleSignOut":
        sendResponse(await signOutGoogle());
        break;
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
  clearAttachedTarget(String(source.tabId));
  persistAttachedTargets().catch(() => {});
  if (socket && socket.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify({ type: "detached", targetId: String(source.tabId) }));
  }
});

chrome.tabs.onRemoved.addListener((tabId) => {
  clearAttachedTarget(String(tabId));
  persistAttachedTargets().catch(() => {});
});

async function bootstrap() {
  await loadSettings();
  await loadAttachedTargets();
  await restoreAttachedTargets();
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
