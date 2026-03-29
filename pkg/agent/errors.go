package agent

const (
	ErrCodeModelNotFound            = "MODEL_NOT_FOUND"
	ErrCodeModelUnavailable         = "MODEL_UNAVAILABLE"
	ErrCodeModelModalityUnsupported = "MODEL_MODALITY_UNSUPPORTED"
	ErrCodeContextBudgetUnfit       = "CONTEXT_BUDGET_UNFIT"
	ErrCodeAgentNotFound            = "AGENT_NOT_FOUND"
)

// RequestError is a typed, client-visible error for per-request validation failures.
// It is handled specially in Run() and sent as a typed error event (not assistant text).
type RequestError struct {
	Code    string
	Message string
	// Optional observability fields for downstream clients/logging.
	ResolvedAgentID string
	RouteMatchedBy  string
}

func (e *RequestError) Error() string { return e.Code + ": " + e.Message }
