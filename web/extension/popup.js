const statusEl = document.getElementById("status");
const tabStateEl = document.getElementById("tabState");
const attachBtn = document.getElementById("attachBtn");
const detachBtn = document.getElementById("detachBtn");
const pairBtn = document.getElementById("pairBtn");
const pairingPanel = document.getElementById("pairingPanel");
const pairQrImg = document.getElementById("pairQrImg");
const pairMeta = document.getElementById("pairMeta");
const pairClaimUrl = document.getElementById("pairClaimUrl");
const pairingCard = document.getElementById("pairingCard");
const controlsCard = document.getElementById("controlsCard");
const pairHint = document.getElementById("pairHint");
const authGate = document.getElementById("authGate");
const signInBtn = document.getElementById("signInBtn");
const mainContent = document.getElementById("mainContent");

const menuBtn = document.getElementById("menuBtn");
const menuPanel = document.getElementById("menuPanel");
const manualSetupBtn = document.getElementById("manualSetupBtn");

let connectInProgress = false;
let pairingAutoRequested = false;
let autoSetupInFlight = false;
let lastAutoSetupRelayUrl = "";

function send(message) {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage(message, (resp) => {
      resolve(resp || { ok: false, error: "no response" });
    });
  });
}

function isPaired(state) {
  return Boolean(state.hasToken);
}

function getFirebaseAuthState(state) {
  if (!state || !state.hasFirebaseAuthToken) {
    return { usable: false, reason: "missing" };
  }
  const expiresAt = Number(state.firebaseAuthExpiresAtEpochSeconds) || 0;
  if (expiresAt > 0) {
    const now = Math.floor(Date.now() / 1000);
    if (expiresAt <= now + 30) {
      return { usable: false, reason: "expired" };
    }
  }
  return { usable: true, reason: "valid" };
}

function canAutoSetupFromAuth(state) {
  return Boolean(getFirebaseAuthState(state).usable && !state.hasToken && state.relayUrl);
}

function renderAuthGate(authState) {
  const shouldShow = !authState.usable;
  if (authGate) {
    authGate.classList.toggle("hidden", !shouldShow);
  }
  if (mainContent) {
    mainContent.classList.toggle("hidden", shouldShow);
  }
  if (shouldShow) {
    setMenuOpen(false);
  }
  return shouldShow;
}

function setMenuOpen(open) {
  if (!menuPanel || !menuBtn) {
    return;
  }
  menuPanel.classList.toggle("hidden", !open);
  menuBtn.setAttribute("aria-expanded", open ? "true" : "false");
}

function renderPairing(resp, fallbackErrorPrefix = "Pairing failed") {
  if (!pairingPanel || !pairMeta || !pairQrImg || !pairClaimUrl) {
    return;
  }
  pairingPanel.classList.remove("hidden");

  if (!resp || !resp.ok) {
    pairMeta.textContent = resp && resp.error ? `${fallbackErrorPrefix}: ${resp.error}` : fallbackErrorPrefix;
    pairQrImg.removeAttribute("src");
    pairClaimUrl.removeAttribute("href");
    return;
  }

  const expiresAt = resp.expires_at ? new Date(resp.expires_at) : null;
  const expiresLabel = expiresAt && !Number.isNaN(expiresAt.getTime())
    ? expiresAt.toLocaleTimeString()
    : "soon";

  pairQrImg.src = `${resp.qr_svg_url}${resp.qr_svg_url.includes("?") ? "&" : "?"}t=${Date.now()}`;
  pairMeta.textContent = `Code ${resp.code} • Expires ${expiresLabel}`;
  pairClaimUrl.href = resp.claim_url;
  pairClaimUrl.textContent = "Open claim URL";
}

async function createPairing({ silent = false } = {}) {
  const resp = await send({ type: "createPairing", payload: { ttlSeconds: 180 } });
  if (!resp || !resp.ok) {
    if (!silent) {
      renderPairing(resp, "Pairing failed");
    } else if (pairHint) {
      pairHint.textContent = resp && resp.error
        ? `QR unavailable: ${resp.error}`
        : "QR unavailable right now. Open Manual Setup from the menu.";
    }
    return resp;
  }
  renderPairing(resp);
  return resp;
}

async function autoSetupIfNeeded(state) {
  if (!canAutoSetupFromAuth(state) || autoSetupInFlight) {
    return state;
  }

  // Avoid re-running the same failed/finished setup continuously during refresh polling.
  if (lastAutoSetupRelayUrl === state.relayUrl) {
    return state;
  }

  autoSetupInFlight = true;
  lastAutoSetupRelayUrl = state.relayUrl;
  try {
    await send({
      type: "autoSetup",
      payload: {
        relayUrl: state.relayUrl,
        token: ""
      }
    });
  } finally {
    autoSetupInFlight = false;
  }

  return send({ type: "getState" });
}

async function refresh() {
  let state = await send({ type: "getState" });
  const authState = getFirebaseAuthState(state);
  if (renderAuthGate(authState)) {
    return;
  }

  state = await autoSetupIfNeeded(state);

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

  if (statusEl) {
    statusEl.textContent = state.connected ? "Connected" : "Disconnected";
    if (state.hardStopped) {
      statusEl.textContent = "Hard Stopped";
    } else if (state.desiredConnected && !state.connected) {
      statusEl.textContent = "Reconnecting";
    }
    statusEl.dataset.state = state.connected ? "connected" : "disconnected";
  }

  if (tabStateEl) {
    const tabLabel = state.activeTabId ? `Tab ${state.activeTabId}` : "No active tab";
    const attachLabel = state.attached ? "attached" : "not attached";
    tabStateEl.textContent = `${tabLabel} • ${attachLabel}`;
  }

  const paired = isPaired(state);
  if (pairingCard) {
    pairingCard.classList.toggle("hidden", paired);
  }
  if (controlsCard) {
    controlsCard.classList.toggle("hidden", !paired);
  }

  if (attachBtn) {
    attachBtn.disabled = !paired || !state.connected || state.hardStopped || state.attached || !state.activeTabId;
  }
  if (detachBtn) {
    detachBtn.disabled = !paired || !state.connected || state.hardStopped || !state.attached || !state.activeTabId;
  }
  if (pairBtn) {
    pairBtn.disabled = paired;
  }

  if (!paired && !pairingAutoRequested) {
    pairingAutoRequested = true;
    await createPairing({ silent: true });
  }

  if (paired) {
    pairingAutoRequested = false;
    if (pairingPanel) {
      pairingPanel.classList.add("hidden");
    }
  }
}

if (menuBtn && menuPanel) {
  menuBtn.addEventListener("click", () => {
    const willOpen = menuPanel.classList.contains("hidden");
    setMenuOpen(willOpen);
  });
  document.addEventListener("click", (event) => {
    if (!menuPanel.classList.contains("hidden")) {
      const target = event.target;
      if (target instanceof Node && !menuPanel.contains(target) && !menuBtn.contains(target)) {
        setMenuOpen(false);
      }
    }
  });
}

if (manualSetupBtn) {
  manualSetupBtn.addEventListener("click", () => {
    setMenuOpen(false);
    chrome.runtime.openOptionsPage();
  });
}

if (signInBtn) {
  signInBtn.addEventListener("click", async () => {
    signInBtn.disabled = true;
    try {
      const state = await send({ type: "getState" });
      const resp = await send({
        type: "authGoogleSignIn",
        payload: {
          relayUrl: state.relayUrl
        }
      });
      if (!resp || !resp.ok) return;
    } finally {
      signInBtn.disabled = false;
      await refresh();
    }
  });
}

if (attachBtn) {
  attachBtn.addEventListener("click", async () => {
    const resp = await send({ type: "attachCurrentTab" });
    if (!resp || !resp.ok) {
      renderPairing({ ok: false, error: resp && resp.error ? `Attach failed: ${resp.error}` : "Attach failed" }, "");
    }
    await refresh();
  });
}

if (detachBtn) {
  detachBtn.addEventListener("click", async () => {
    const resp = await send({ type: "detachCurrentTab" });
    if (!resp || !resp.ok) {
      renderPairing({ ok: false, error: resp && resp.error ? `Detach failed: ${resp.error}` : "Detach failed" }, "");
    }
    await refresh();
  });
}

if (pairBtn) {
  pairBtn.addEventListener("click", async () => {
    await createPairing({ silent: false });
  });
}

refresh();
