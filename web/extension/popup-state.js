export function popupActionState({ connected, attached }) {
  return {
    connectLabel: connected ? "Disconnect Relay" : "Connect Relay",
    attachDisabled: !connected || attached,
    detachDisabled: !connected || !attached,
    statusLabel: connected ? "Connected" : "Disconnected"
  };
}
