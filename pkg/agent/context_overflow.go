package agent

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var (
	contextOverflowPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\brequest_too_large\b`),
		regexp.MustCompile(`(?i)request exceeds the maximum size`),
		regexp.MustCompile(`(?i)context length exceeded`),
		regexp.MustCompile(`(?i)maximum context length`),
		regexp.MustCompile(`(?i)prompt is too long`),
		regexp.MustCompile(`(?i)prompt too long`),
		regexp.MustCompile(`(?i)input is too long for requested model`),
		regexp.MustCompile(`(?i)exceeds the context window`),
		regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),
		regexp.MustCompile(`(?i)maximum prompt length is \d+`),
		regexp.MustCompile(`(?i)reduce the length of the messages`),
		regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),
		regexp.MustCompile(`(?i)exceeds the limit of \d+`),
		regexp.MustCompile(`(?i)exceeds the available context size`),
		regexp.MustCompile(`(?i)greater than the context length`),
		regexp.MustCompile(`(?i)context window exceeds limit`),
		regexp.MustCompile(`(?i)exceeded model token limit`),
		regexp.MustCompile(`(?i)too large for model with \d+ maximum context length`),
		regexp.MustCompile(`(?i)model_context_window_exceeded`),
		regexp.MustCompile(`(?i)context[_ ]window[_ ]exceeded`),
		regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
		regexp.MustCompile(`(?i)\btoo many tokens\b`),
		regexp.MustCompile(`(?i)\btoken limit exceeded\b`),
	}

	// Anchored "head" matcher to avoid triggering on arbitrary in-message discussion
	// about context overflow in provider error wrappers.
	contextOverflowHeadPattern = regexp.MustCompile(
		`(?i)^(?:context overflow:|request_too_large\b|request size exceeds\b|request exceeds the maximum size\b|context length exceeded\b|maximum context length\b|prompt is too long\b|exceeds model context window\b)`,
	)
	cerebrasNoBodyPattern = regexp.MustCompile(`(?i)^4(00|13)\s*(status code)?\s*\(no body\)`)
)

func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)

	if contextOverflowHeadPattern.MatchString(lower) {
		return true
	}
	for _, p := range contextOverflowPatterns {
		if p.MatchString(lower) {
			return true
		}
	}
	if cerebrasNoBodyPattern.MatchString(lower) {
		return true
	}

	hasRequestSizeExceeds := strings.Contains(lower, "request size") && strings.Contains(lower, "exceeds")
	hasContextWindow := strings.Contains(lower, "context") && strings.Contains(lower, "window")
	if hasRequestSizeExceeds && hasContextWindow {
		return true
	}
	if strings.Contains(lower, "max_tokens") &&
		strings.Contains(lower, "exceed") &&
		strings.Contains(lower, "context") {
		return true
	}
	if strings.Contains(lower, "input length") &&
		strings.Contains(lower, "exceed") &&
		strings.Contains(lower, "context") {
		return true
	}
	if strings.Contains(lower, "invalidparameter") &&
		(strings.Contains(lower, "context") || strings.Contains(lower, "message tokens") || strings.Contains(lower, "token")) &&
		(strings.Contains(lower, "exceed") || strings.Contains(lower, "too long")) {
		return true
	}
	if strings.Contains(lower, "413") && strings.Contains(lower, "too large") {
		return true
	}

	return false
}
