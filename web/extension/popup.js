const statusEl = document.getElementById("status");
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
  statusEl.textContent = state.connected ? "Connected" : "Disconnected";
  if (state.hardStopped) {
    statusEl.textContent = "Hard Stopped";
  } else if (state.desiredConnected && !state.connected) {
    statusEl.textContent = "Reconnecting";
  }
  statusEl.dataset.state = state.connected ? "connected" : "disconnected";
  pairBtn.disabled = false;
}

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
