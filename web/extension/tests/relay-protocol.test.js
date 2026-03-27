import test from "node:test";
import assert from "node:assert/strict";

import {
  relayAuthGoogleExtensionURL,
  relayBootstrapTokenURL,
  relayPairingURL,
  relaySessionStateURL,
  relaySessionStopURL,
  relaySetupURL,
  relayStatusURL,
  validateRelayRequest
} from "../relay-protocol.js";

test("validateRelayRequest rejects unattached targets", () => {
  const result = validateRelayRequest({
    type: "request",
    requestId: "r1",
    targetId: "9",
    method: "Page.navigate"
  }, {});

  assert.equal(result.ok, false);
  assert.equal(result.error, "target is not attached in extension");
});

test("validateRelayRequest accepts attached targets", () => {
  const result = validateRelayRequest({
    type: "request",
    requestId: "r1",
    targetId: "9",
    method: "Page.navigate"
  }, { "9": true });

  assert.equal(result.ok, true);
  assert.equal(result.targetID, 9);
});

test("relayStatusURL maps extension WS URL to status endpoint", () => {
  assert.equal(
    relayStatusURL("ws://127.0.0.1:18800/agent-browser/extension"),
    "http://127.0.0.1:18800/api/agent-browser/status"
  );
});

test("relay setup and pairing urls map from extension URL", () => {
  assert.equal(
    relaySetupURL("wss://api.suprclaw.com/agent-browser/extension"),
    "https://api.suprclaw.com/api/agent-browser/setup"
  );
  assert.equal(
    relayBootstrapTokenURL("wss://api.suprclaw.com/agent-browser/extension"),
    "https://api.suprclaw.com/api/agent-browser/bootstrap-token"
  );
  assert.equal(
    relayAuthGoogleExtensionURL(
      "wss://api.suprclaw.com/agent-browser/extension",
      "https://example.chromiumapp.org/callback"
    ),
    "https://auth.suprclaw.com/auth/firebase/google/extension?redirect_uri=https%3A%2F%2Fexample.chromiumapp.org%2Fcallback"
  );
  assert.equal(
    relayPairingURL("wss://api.suprclaw.com/agent-browser/extension"),
    "https://api.suprclaw.com/api/agent-browser/pairing"
  );
  assert.equal(
    relaySessionStateURL("wss://api.suprclaw.com/agent-browser/extension"),
    "https://api.suprclaw.com/api/agent-browser/session/state"
  );
  assert.equal(
    relaySessionStopURL("wss://api.suprclaw.com/agent-browser/extension"),
    "https://api.suprclaw.com/api/agent-browser/session/stop"
  );
  assert.equal(
    relayAuthGoogleExtensionURL(
      "ws://127.0.0.1:18800/agent-browser/extension",
      "https://example.chromiumapp.org/local"
    ),
    "http://127.0.0.1:18800/auth/firebase/google/extension?redirect_uri=https%3A%2F%2Fexample.chromiumapp.org%2Flocal"
  );
});
