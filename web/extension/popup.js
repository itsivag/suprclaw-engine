const statusEl = document.getElementById("status");
const tabStateEl = document.getElementById("tabState");
const optionsBtn = document.getElementById("optionsBtn");
const attachBtn = document.getElementById("attachBtn");
const detachBtn = document.getElementById("detachBtn");
const pairBtn = document.getElementById("pairBtn");
const pairingPanel = document.getElementById("pairingPanel");
const pairQrImg = document.getElementById("pairQrImg");
const pairMeta = document.getElementById("pairMeta");
const pairClaimUrl = document.getElementById("pairClaimUrl");
let connectInProgress = false;

function send(message) {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage(message, (resp) => {
      resolve(resp || { ok: false, error: "no response" });
    });
  });
}

async function refresh() {
  let state = await send({ type: "getState" });
  if (
    !connectInProgress &&
    !state.connected &&
    !state.hardStopped &&
    (state.hasToken || state.hasFirebaseAuthToken)
  ) {
    connectInProgress = true;
    await send({ type: "connect" });
    state = await send({ type: "getState" });
    connectInProgress = false;
  }
  statusEl.textContent = state.connected ? "Connected" : "Disconnected";
  if (state.hardStopped) {
    statusEl.textContent = "Hard Stopped";
  } else if (state.desiredConnected && !state.connected) {
    statusEl.textContent = "Reconnecting";
  }
  statusEl.dataset.state = state.connected ? "connected" : "disconnected";
  const tabLabel = state.activeTabId ? `Tab ${state.activeTabId}` : "No active tab";
  const attachLabel = state.attached ? "attached" : "not attached";
  tabStateEl.textContent = `${tabLabel} • ${attachLabel}`;
  attachBtn.disabled = !state.connected || state.hardStopped || state.attached || !state.activeTabId;
  detachBtn.disabled = !state.connected || state.hardStopped || !state.attached || !state.activeTabId;
  pairBtn.disabled = false;
}

optionsBtn.addEventListener("click", () => {
  chrome.runtime.openOptionsPage();
});

attachBtn.addEventListener("click", async () => {
  const resp = await send({ type: "attachCurrentTab" });
  if (!resp || !resp.ok) {
    pairMeta.textContent = resp && resp.error ? `Attach failed: ${resp.error}` : "Attach failed";
    pairingPanel.classList.remove("hidden");
  }
  await refresh();
});

detachBtn.addEventListener("click", async () => {
  const resp = await send({ type: "detachCurrentTab" });
  if (!resp || !resp.ok) {
    pairMeta.textContent = resp && resp.error ? `Detach failed: ${resp.error}` : "Detach failed";
    pairingPanel.classList.remove("hidden");
  }
  await refresh();
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
