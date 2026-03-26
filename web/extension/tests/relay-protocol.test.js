import test from "node:test";
import assert from "node:assert/strict";

import { relayStatusURL, validateRelayRequest } from "../relay-protocol.js";

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
    relayStatusURL("ws://127.0.0.1:18800/browser-relay/extension"),
    "http://127.0.0.1:18800/api/browser-relay/status"
  );
});
