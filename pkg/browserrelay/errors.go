package browserrelay

import "errors"

var (
	ErrUnsupportedAction       = errors.New("unsupported browser relay action")
	ErrInvalidTargetID         = errors.New("invalid target id")
	ErrAgentBrowserUnavailable = errors.New("agent-browser runtime unavailable")
	ErrSessionNotFound         = errors.New("agent-browser session not found")
)
