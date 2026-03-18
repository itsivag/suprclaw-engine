package heartbeat

import "strings"

// HeartbeatOKToken is the sentinel the agent includes when it has nothing to report.
const HeartbeatOKToken = "HEARTBEAT_OK"

// defaultAckMaxChars is the default number of trailing characters to strip
// after the HEARTBEAT_OK token before deciding the response is effectively empty.
const defaultAckMaxChars = 60

// StripResult holds the result of stripping the heartbeat token.
type StripResult struct {
	Text       string
	ShouldSkip bool // true when the response is effectively just an acknowledgment
}

// StripHeartbeatToken removes HEARTBEAT_OK markers and decides whether delivery
// should be suppressed.
//
// Rules:
//   - If the response contains HEARTBEAT_OK, strip the token.
//   - Strip up to ackMaxChars of trailing whitespace/punctuation after the token.
//   - If the remaining text (trimmed) is empty or shorter than ackMaxChars,
//     set ShouldSkip = true.
//
// ackMaxChars ≤ 0 uses defaultAckMaxChars.
func StripHeartbeatToken(response string, ackMaxChars int) StripResult {
	if ackMaxChars <= 0 {
		ackMaxChars = defaultAckMaxChars
	}

	idx := strings.Index(response, HeartbeatOKToken)
	if idx < 0 {
		// No token present — deliver as-is.
		return StripResult{Text: response, ShouldSkip: false}
	}

	// Remove the token itself.
	before := response[:idx]
	after := response[idx+len(HeartbeatOKToken):]

	// Strip leading whitespace/punctuation from the tail (up to ackMaxChars).
	trimmed := strings.TrimLeft(after, " \t\n\r.,!?;:")
	// If the tail after stripping is longer than ackMaxChars it's real content.
	if len(trimmed) > ackMaxChars {
		// Keep the substantive tail content.
		combined := strings.TrimSpace(before + " " + trimmed)
		return StripResult{Text: combined, ShouldSkip: false}
	}

	// The remaining text is just noise after the token.
	remaining := strings.TrimSpace(before)
	if remaining == "" {
		return StripResult{Text: "", ShouldSkip: true}
	}

	// There was content before the token — that might be real. If it's short, skip.
	if len(remaining) <= ackMaxChars {
		return StripResult{Text: remaining, ShouldSkip: true}
	}

	return StripResult{Text: remaining, ShouldSkip: false}
}

// IsEffectivelyEmpty returns true for blank/whitespace-only responses.
func IsEffectivelyEmpty(response string) bool {
	return strings.TrimSpace(response) == ""
}
