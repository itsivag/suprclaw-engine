export function normalizeRelayURL(input) {
  const raw = String(input || "").trim();
  if (!raw) {
    return "";
  }

  let url = raw;
  if (!/^wss?:\/\//i.test(url) && /^https?:\/\//i.test(url)) {
    url = url.replace(/^http/i, "ws");
  }
  if (!/^wss?:\/\//i.test(url)) {
    url = `ws://${url}`;
  }

  const parsed = new URL(url);
  const legacyPrefix = "/browser-relay";
  if (parsed.pathname === legacyPrefix || parsed.pathname.startsWith(`${legacyPrefix}/`)) {
    parsed.pathname = parsed.pathname.replace(/^\/browser-relay(?=\/|$)/, "/agent-browser");
  }
  if (!parsed.pathname || parsed.pathname === "/" || parsed.pathname === "/agent-browser") {
    parsed.pathname = "/agent-browser/extension";
  }
  return parsed.toString();
}

export function badgeForState(state) {
  switch (state) {
    case "connected":
      return { text: "ON", color: "#1f9d55" };
    case "connecting":
      return { text: "…", color: "#b08900" };
    default:
      return { text: "!", color: "#c53030" };
  }
}

export function buildTargets(tabs, attachedMap) {
  return (tabs || [])
    .filter((tab) => typeof tab.id === "number")
    .map((tab) => ({
      id: String(tab.id),
      type: "page",
      title: tab.title || "",
      url: tab.url || "",
      attached: Boolean(attachedMap[String(tab.id)]),
      source: "extension"
    }));
}

export function parseTargetID(targetID) {
  const id = Number(targetID);
  if (!Number.isInteger(id) || id <= 0) {
    return null;
  }
  return id;
}
