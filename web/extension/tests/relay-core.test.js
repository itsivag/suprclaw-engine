import test from "node:test";
import assert from "node:assert/strict";

import { badgeForState, buildTargets, normalizeRelayURL, parseTargetID } from "../relay-core.js";

test("normalizeRelayURL fills protocol and extension path", () => {
  assert.equal(normalizeRelayURL("127.0.0.1:18800"), "ws://127.0.0.1:18800/agent-browser/extension");
  assert.equal(
    normalizeRelayURL("https://relay.example.com/path"),
    "wss://relay.example.com/path"
  );
});

test("normalizeRelayURL upgrades legacy browser-relay path", () => {
  assert.equal(
    normalizeRelayURL("wss://api.suprclaw.com/browser-relay/extension"),
    "wss://api.suprclaw.com/agent-browser/extension"
  );
  assert.equal(
    normalizeRelayURL("https://api.suprclaw.com/browser-relay"),
    "wss://api.suprclaw.com/agent-browser/extension"
  );
});

test("badgeForState returns expected labels", () => {
  assert.deepEqual(badgeForState("connected"), { text: "ON", color: "#1f9d55" });
  assert.deepEqual(badgeForState("connecting"), { text: "…", color: "#b08900" });
  assert.deepEqual(badgeForState("disconnected"), { text: "!", color: "#c53030" });
});

test("buildTargets marks attached tabs", () => {
  const targets = buildTargets(
    [
      { id: 11, title: "A", url: "https://a.example" },
      { id: 22, title: "B", url: "https://b.example" }
    ],
    { "22": true }
  );

  assert.equal(targets.length, 2);
  assert.equal(targets[0].attached, false);
  assert.equal(targets[1].attached, true);
});

test("parseTargetID validates integers", () => {
  assert.equal(parseTargetID("7"), 7);
  assert.equal(parseTargetID("0"), null);
  assert.equal(parseTargetID("abc"), null);
});
