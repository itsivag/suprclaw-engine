package browserrelay

import "strings"

const (
	extensionTargetPrefix    = "ext:"
	agentBrowserTargetPrefix = "ab:"
)

func normalizeExtensionTargetID(raw string) string {
	id := strings.TrimSpace(raw)
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, extensionTargetPrefix) {
		return id
	}
	return extensionTargetPrefix + id
}

func extensionTargetRawID(targetID string) string {
	id := strings.TrimSpace(targetID)
	if strings.HasPrefix(id, extensionTargetPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(id, extensionTargetPrefix))
	}
	return id
}

func isExtensionTargetID(targetID string) bool {
	return strings.HasPrefix(strings.TrimSpace(targetID), extensionTargetPrefix)
}

func BuildAgentBrowserTargetID(sessionID, pageRef string) string {
	sid := strings.TrimSpace(sessionID)
	ref := strings.TrimSpace(pageRef)
	if ref == "" {
		ref = "main"
	}
	return agentBrowserTargetPrefix + sid + ":" + ref
}

func ParseAgentBrowserTargetID(targetID string) (sessionID, pageRef string, ok bool) {
	id := strings.TrimSpace(targetID)
	if id == "" {
		return "", "", false
	}
	if strings.HasPrefix(id, agentBrowserTargetPrefix) {
		trimmed := strings.TrimPrefix(id, agentBrowserTargetPrefix)
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			return "", "", false
		}
		sessionID = strings.TrimSpace(parts[0])
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			pageRef = strings.TrimSpace(parts[1])
		} else {
			pageRef = "main"
		}
		return sessionID, pageRef, true
	}
	// Allow bare session IDs for session.close workflows.
	return id, "main", true
}

func isAgentBrowserTargetID(targetID string) bool {
	return strings.HasPrefix(strings.TrimSpace(targetID), agentBrowserTargetPrefix)
}
