import { popupActionState } from "./popup-state.js";

const statusEl = document.getElementById("status");
const tabMetaEl = document.getElementById("tabMeta");
const connectBtn = document.getElementById("connectBtn");
const hardStopBtn = document.getElementById("hardStopBtn");
const attachBtn = document.getElementById("attachBtn");
const detachBtn = document.getElementById("detachBtn");
const optionsBtn = document.getElementById("optionsBtn");
const pairBtn = document.getElementById("pairBtn");
const pairingPanel = document.getElementById("pairingPanel");
const pairQrImg = document.getElementById("pairQrImg");
const pairMeta = document.getElementById("pairMeta");
const pairClaimUrl = document.getElementById("pairClaimUrl");

function send(message) {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage(message, (resp) => {
      resolve(resp || { ok: false, error: "no response" });
    });
  });
}

async function refresh() {
  const state = await send({ type: "getState" });
  const ui = popupActionState({ connected: Boolean(state.connected), attached: Boolean(state.attached) });

  statusEl.textContent = ui.statusLabel;
  if (state.hardStopped) {
    statusEl.textContent = "Hard Stopped";
  } else if (state.desiredConnected && !state.connected) {
    statusEl.textContent = "Reconnecting";
  }
  statusEl.dataset.state = state.connected ? "connected" : "disconnected";
  connectBtn.textContent = ui.connectLabel;
  attachBtn.disabled = ui.attachDisabled;
  detachBtn.disabled = ui.detachDisabled;
  hardStopBtn.disabled = !state.hasToken;

  const tabLabel = state.activeTabId ? `Tab ${state.activeTabId}` : "No active tab";
  tabMetaEl.textContent = `${tabLabel} • ${state.attached ? "Attached" : "Detached"}`;
  pairBtn.disabled = !state.connected;
}

connectBtn.addEventListener("click", async () => {
  const current = await send({ type: "getState" });
  if (current.connected) {
    await send({ type: "disconnect" });
  } else {
    await send({ type: "connect" });
  }
  await refresh();
});

hardStopBtn.addEventListener("click", async () => {
  await send({ type: "hardStop" });
  await refresh();
});

attachBtn.addEventListener("click", async () => {
  await send({ type: "attachCurrentTab" });
  await refresh();
});

detachBtn.addEventListener("click", async () => {
  await send({ type: "detachCurrentTab" });
  await refresh();
});

optionsBtn.addEventListener("click", () => {
  chrome.runtime.openOptionsPage();
});

pairBtn.addEventListener("click", async () => {
  const resp = await send({ type: "createPairing", payload: { ttlSeconds: 180 } });
  if (!resp || !resp.ok) {
    pairingPanel.classList.remove("hidden");
    pairMeta.textContent = resp && resp.error ? `Pairing failed: ${resp.error}` : "Pairing failed";
    pairQrImg.removeAttribute("src");
    pairClaimUrl.removeAttribute("href");
    return;
  }

  const expiresAt = resp.expires_at ? new Date(resp.expires_at) : null;
  const expiresLabel = expiresAt && !Number.isNaN(expiresAt.getTime())
    ? expiresAt.toLocaleTimeString()
    : "soon";

  pairingPanel.classList.remove("hidden");
  pairQrImg.src = `${resp.qr_svg_url}${resp.qr_svg_url.includes("?") ? "&" : "?"}t=${Date.now()}`;
  pairMeta.textContent = `Code ${resp.code} • Expires ${expiresLabel}`;
  pairClaimUrl.href = resp.claim_url;
  pairClaimUrl.textContent = "Open claim URL";
});

refresh();
