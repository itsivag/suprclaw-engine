import { normalizeRelayURL, parseTargetID } from "./relay-core.js";

export function validateRelayRequest(message, attachedTargets) {
  if (!message || message.type !== "request") {
    return { ok: false, error: "unsupported message type" };
  }

  const requestId = message.requestId;
  const method = message.method;
  const targetID = parseTargetID(message.targetId);
  if (!requestId || !method || !targetID) {
    return { ok: false, requestId, error: "invalid request envelope" };
  }
  if (!attachedTargets[String(targetID)]) {
    return { ok: false, requestId, error: "target is not attached in extension" };
  }

  return { ok: true, requestId, method, targetID };
}

export function relayStatusURL(inputRelayURL) {
  return relayAPIURL(inputRelayURL, "status");
}

export function relaySetupURL(inputRelayURL) {
  return relayAPIURL(inputRelayURL, "setup");
}

export function relayBootstrapTokenURL(inputRelayURL) {
  return relayAPIURL(inputRelayURL, "bootstrap-token");
}

export function relayAuthGoogleExtensionURL(inputRelayURL, redirectURI) {
  const authBase = relayAuthBaseURL(inputRelayURL);
  const url = new URL("/auth/firebase/google/extension", authBase);
  url.searchParams.set("redirect_uri", String(redirectURI || ""));
  return url.toString();
}

export function relayPairingURL(inputRelayURL) {
  return relayAPIURL(inputRelayURL, "pairing");
}

export function relaySessionStateURL(inputRelayURL) {
  return relayAPIURL(inputRelayURL, "session/state");
}

export function relaySessionStopURL(inputRelayURL) {
  return relayAPIURL(inputRelayURL, "session/stop");
}

function relayAPIURL(inputRelayURL, endpoint) {
  const relayUrl = normalizeRelayURL(inputRelayURL);
  return relayUrl
    .replace(/^ws:/i, "http:")
    .replace(/^wss:/i, "https:")
    .replace(/\/browser-relay\/extension.*$/, `/api/browser-relay/${endpoint}`);
}

function relayAuthBaseURL(inputRelayURL) {
  const relay = new URL(normalizeRelayURL(inputRelayURL));
  const scheme = relay.protocol.toLowerCase() === "wss:" ? "https:" : "http:";
  const localHosts = new Set(["localhost", "127.0.0.1"]);
  if (localHosts.has(relay.hostname)) {
    return `${scheme}//${relay.host}`;
  }
  const authHost = relay.hostname.startsWith("api.")
    ? `auth.${relay.hostname.slice(4)}`
    : `auth.${relay.hostname}`;
  return `${scheme}//${authHost}`;
}
