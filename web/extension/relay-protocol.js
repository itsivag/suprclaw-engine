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

export function relayPairingURL(inputRelayURL) {
  return relayAPIURL(inputRelayURL, "pairing");
}

function relayAPIURL(inputRelayURL, endpoint) {
  const relayUrl = normalizeRelayURL(inputRelayURL);
  return relayUrl
    .replace(/^ws:/i, "http:")
    .replace(/^wss:/i, "https:")
    .replace(/\/browser-relay\/extension.*$/, `/api/browser-relay/${endpoint}`);
}
