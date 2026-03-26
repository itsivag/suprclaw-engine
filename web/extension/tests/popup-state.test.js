import test from "node:test";
import assert from "node:assert/strict";

import { popupActionState } from "../popup-state.js";

test("popupActionState for connected detached tab", () => {
  const state = popupActionState({ connected: true, attached: false });
  assert.equal(state.connectLabel, "Disconnect Relay");
  assert.equal(state.attachDisabled, false);
  assert.equal(state.detachDisabled, true);
});

test("popupActionState for disconnected tab", () => {
  const state = popupActionState({ connected: false, attached: false });
  assert.equal(state.connectLabel, "Connect Relay");
  assert.equal(state.attachDisabled, true);
  assert.equal(state.detachDisabled, true);
});
