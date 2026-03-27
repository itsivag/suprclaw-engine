package agent

import (
	"context"
	"errors"
	"testing"
)

func TestIsContextOverflowError_MatchesKnownProviderMessages(t *testing.T) {
	cases := []string{
		"context_length_exceeded: too many tokens",
		"Your input exceeds the context window of this model",
		"prompt is too long: 213462 tokens > 200000 maximum",
		"input is too long for requested model",
		"The input token count (1196265) exceeds the maximum number of tokens allowed (1048575)",
		"This model's maximum prompt length is 131072 but the request contains 537812 tokens",
		"Please reduce the length of the messages or completion",
		"This endpoint's maximum context length is 65536 tokens",
		"the request exceeds the available context size, try increasing it",
		"tokens to keep from the initial prompt is greater than the context length",
		"invalid params, context window exceeds limit",
		"Your request exceeded model token limit: 240000 (requested: 270000)",
		"Prompt contains 90000 tokens and is too large for model with 32768 maximum context length",
		"model_context_window_exceeded",
		"request_too_large",
		"request size exceeds context window",
		"max_tokens would exceed context",
		"input length exceeds context",
		"HTTP 413 too large",
		"413 status code (no body)",
		"InvalidParameter: Total tokens of image and text exceed max message tokens",
	}

	for _, tc := range cases {
		if !isContextOverflowError(errors.New(tc)) {
			t.Fatalf("expected true for: %q", tc)
		}
	}
}

func TestIsContextOverflowError_IgnoresNonOverflowErrors(t *testing.T) {
	cases := []error{
		nil,
		context.Canceled,
		context.DeadlineExceeded,
		errors.New("rate limit exceeded"),
		errors.New("unauthorized"),
		errors.New("context overflow handling docs mention this term"),
		errors.New("network timeout while connecting"),
	}

	for _, tc := range cases {
		if isContextOverflowError(tc) {
			t.Fatalf("expected false for: %v", tc)
		}
	}
}
