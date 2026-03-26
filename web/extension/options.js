const relayUrlEl = document.getElementById("relayUrl");
const tokenEl = document.getElementById("token");
const autoSetupBtn = document.getElementById("autoSetupBtn");
const saveBtn = document.getElementById("saveBtn");
const testBtn = document.getElementById("testBtn");
const diagnosticsEl = document.getElementById("diagnostics");

function send(message) {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage(message, (resp) => {
      resolve(resp || { ok: false, error: "no response" });
    });
  });
}

async function load() {
  const state = await send({ type: "getState" });
  relayUrlEl.value = state.relayUrl || "";
  tokenEl.value = "";
  diagnosticsEl.textContent = JSON.stringify(state, null, 2);
}

saveBtn.addEventListener("click", async () => {
  const payload = {
    relayUrl: relayUrlEl.value
  };
  if (tokenEl.value.trim()) {
    payload.token = tokenEl.value;
  }
  const resp = await send({
    type: "saveSettings",
    payload
  });
  diagnosticsEl.textContent = JSON.stringify(resp, null, 2);
});

testBtn.addEventListener("click", async () => {
  const resp = await send({
    type: "testConnection",
    payload: {
      relayUrl: relayUrlEl.value,
      token: tokenEl.value
    }
  });
  diagnosticsEl.textContent = JSON.stringify(resp, null, 2);
});

autoSetupBtn.addEventListener("click", async () => {
  const resp = await send({
    type: "autoSetup",
    payload: {
      relayUrl: relayUrlEl.value,
      token: tokenEl.value
    }
  });
  diagnosticsEl.textContent = JSON.stringify(resp, null, 2);
  if (resp && resp.ok) {
    relayUrlEl.value = resp.relayUrl || relayUrlEl.value;
    tokenEl.value = "";
  }
});

load();
