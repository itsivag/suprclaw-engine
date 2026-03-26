const relayUrlEl = document.getElementById("relayUrl");
const tokenEl = document.getElementById("token");
const saveBtn = document.getElementById("saveBtn");
const testBtn = document.getElementById("testBtn");
const googleSignInBtn = document.getElementById("googleSignInBtn");
const googleSignOutBtn = document.getElementById("googleSignOutBtn");
const authStatusEl = document.getElementById("authStatus");
const diagnosticsEl = document.getElementById("diagnostics");

function send(message) {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage(message, (resp) => {
      resolve(resp || { ok: false, error: "no response" });
    });
  });
}

function shouldAutoSetupFromAuth(state) {
  return Boolean(state && state.hasFirebaseAuthToken && !state.hasToken);
}

async function runAutoSetup() {
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
  return resp;
}

async function load() {
  let state = await send({ type: "getState" });
  if (shouldAutoSetupFromAuth(state)) {
    const autoSetupResp = await runAutoSetup();
    if (autoSetupResp && autoSetupResp.ok) {
      state = await send({ type: "getState" });
    }
  }
  relayUrlEl.value = state.relayUrl || "";
  tokenEl.value = "";
  renderAuthStatus(state);
  diagnosticsEl.textContent = JSON.stringify(state, null, 2);
}

function renderAuthStatus(state) {
  if (state && state.hasFirebaseAuthToken) {
    const expiry = Number(state.firebaseAuthExpiresAtEpochSeconds) || 0;
    const when = expiry > 0 ? new Date(expiry * 1000).toLocaleString() : "unknown";
    authStatusEl.textContent = `Google auth linked. Token expiry: ${when}`;
    return;
  }
  authStatusEl.textContent = "Not signed in. Use Google Sign-In before Auto Setup.";
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

googleSignInBtn.addEventListener("click", async () => {
  const resp = await send({
    type: "authGoogleSignIn",
    payload: {
      relayUrl: relayUrlEl.value
    }
  });
  diagnosticsEl.textContent = JSON.stringify(resp, null, 2);
  let state = await send({ type: "getState" });
  if (resp && resp.ok && shouldAutoSetupFromAuth(state)) {
    const autoSetupResp = await runAutoSetup();
    if (autoSetupResp && autoSetupResp.ok) {
      state = await send({ type: "getState" });
    }
  }
  renderAuthStatus(state);
});

googleSignOutBtn.addEventListener("click", async () => {
  const resp = await send({ type: "authGoogleSignOut" });
  diagnosticsEl.textContent = JSON.stringify(resp, null, 2);
  const state = await send({ type: "getState" });
  renderAuthStatus(state);
});

load();
