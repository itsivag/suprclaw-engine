import { popupActionState } from "./popup-state.js";

const statusEl = document.getElementById("status");
const tabMetaEl = document.getElementById("tabMeta");
const connectBtn = document.getElementById("connectBtn");
const attachBtn = document.getElementById("attachBtn");
const detachBtn = document.getElementById("detachBtn");
const optionsBtn = document.getElementById("optionsBtn");

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
  statusEl.dataset.state = state.connected ? "connected" : "disconnected";
  connectBtn.textContent = ui.connectLabel;
  attachBtn.disabled = ui.attachDisabled;
  detachBtn.disabled = ui.detachDisabled;

  const tabLabel = state.activeTabId ? `Tab ${state.activeTabId}` : "No active tab";
  tabMetaEl.textContent = `${tabLabel} • ${state.attached ? "Attached" : "Detached"}`;
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

refresh();
