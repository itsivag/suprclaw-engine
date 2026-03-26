package agent

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/providers"
)

const (
	defaultContextGuardSafetyMarginTokens     = 2048
	defaultContextGuardTargetInputRatio       = 0.78
	defaultContextGuardEmergencyInputRatio    = 0.60
	defaultContextGuardMaxCompactionPasses    = 3
	defaultContextGuardPreserveRecentMessages = 6

	estimatorFixedReserveTokens = 512
)

type contextBudget struct {
	ContextWindow         int
	RequestedOutputTokens int
	SafetyMarginTokens    int
	EffectiveInputLimit   int
	TargetInputTokens     int
	EmergencyInputTokens  int
}

type compactionStage string

const (
	compactionStageNone      compactionStage = "none"
	compactionStagePrune     compactionStage = "stage_1_prune"
	compactionStageSummary   compactionStage = "stage_2_summary"
	compactionStageEmergency compactionStage = "stage_3_emergency"
)

func normalizeContextGuard(guard config.ContextGuardConfig) config.ContextGuardConfig {
	if guard == (config.ContextGuardConfig{}) {
		guard.Enabled = true
	}
	if guard.SafetyMarginTokens <= 0 {
		guard.SafetyMarginTokens = defaultContextGuardSafetyMarginTokens
	}
	if guard.TargetInputRatio <= 0 || guard.TargetInputRatio > 1 {
		guard.TargetInputRatio = defaultContextGuardTargetInputRatio
	}
	if guard.EmergencyInputRatio <= 0 || guard.EmergencyInputRatio > 1 {
		guard.EmergencyInputRatio = defaultContextGuardEmergencyInputRatio
	}
	if guard.MaxCompactionPasses <= 0 {
		guard.MaxCompactionPasses = defaultContextGuardMaxCompactionPasses
	}
	if guard.PreserveRecentMessages <= 0 {
		guard.PreserveRecentMessages = defaultContextGuardPreserveRecentMessages
	}
	return guard
}

func computeContextBudget(contextWindow, requestedOutput int, guard config.ContextGuardConfig) contextBudget {
	g := normalizeContextGuard(guard)

	if contextWindow < 0 {
		contextWindow = 0
	}
	if requestedOutput < 0 {
		requestedOutput = 0
	}
	effective := contextWindow - requestedOutput - g.SafetyMarginTokens
	if effective < 0 {
		effective = 0
	}

	target := int(float64(effective) * g.TargetInputRatio)
	if target <= 0 {
		target = effective
	}

	emergency := int(float64(effective) * g.EmergencyInputRatio)
	if emergency <= 0 {
		emergency = effective
	}

	return contextBudget{
		ContextWindow:         contextWindow,
		RequestedOutputTokens: requestedOutput,
		SafetyMarginTokens:    g.SafetyMarginTokens,
		EffectiveInputLimit:   effective,
		TargetInputTokens:     target,
		EmergencyInputTokens:  emergency,
	}
}

func (al *AgentLoop) countInputTokens(
	ctx context.Context,
	provider providers.LLMProvider,
	model string,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	options map[string]any,
) (int, string) {
	if counter, ok := provider.(providers.TokenCountCapable); ok {
		tokens, err := counter.CountTokens(ctx, messages, tools, model, options)
		if err == nil && tokens > 0 {
			return tokens, "provider"
		}
		if err != nil {
			logger.WarnCF("agent", "Provider token count failed, using estimator", map[string]any{
				"model": model,
				"error": err.Error(),
			})
		}
	}

	est := al.estimateTokens(messages)
	// Conservative fallback: estimator + 10% overhead + fixed reserve.
	est = est + est/10 + estimatorFixedReserveTokens
	if est < estimatorFixedReserveTokens {
		est = estimatorFixedReserveTokens
	}
	return est, "estimator"
}

func getRequestedOutputTokens(llmOpts map[string]any, defaultValue int) int {
	if llmOpts != nil {
		if v, ok := llmOpts["max_tokens"]; ok {
			switch n := v.(type) {
			case int:
				if n > 0 {
					return n
				}
			case int64:
				if n > 0 {
					return int(n)
				}
			case float64:
				if n > 0 {
					return int(n)
				}
			}
		}
	}
	if defaultValue < 0 {
		return 0
	}
	return defaultValue
}

func (al *AgentLoop) resolveContextWindowForTurn(
	agent *AgentInstance,
	candidates []providers.FallbackCandidate,
	guard config.ContextGuardConfig,
	requestedOutput int,
) int {
	maxWindow := agent.ContextWindow

	for _, c := range candidates {
		key := providers.ModelKey(c.Provider, c.Model)
		if w := agent.CandidateContextWindows[key]; w > maxWindow {
			maxWindow = w
		}
	}

	minRequired := requestedOutput + guard.SafetyMarginTokens + 1024
	if maxWindow < minRequired {
		maxWindow = minRequired
	}
	return maxWindow
}

func (al *AgentLoop) orderCandidatesByContextWindow(
	agent *AgentInstance,
	candidates []providers.FallbackCandidate,
) []providers.FallbackCandidate {
	if len(candidates) <= 1 {
		return candidates
	}

	type rankedCandidate struct {
		idx       int
		candidate providers.FallbackCandidate
		window    int
	}

	ranked := make([]rankedCandidate, 0, len(candidates))
	for i, c := range candidates {
		key := providers.ModelKey(c.Provider, c.Model)
		window := agent.CandidateContextWindows[key]
		if window <= 0 {
			window = agent.ContextWindow
		}
		ranked = append(ranked, rankedCandidate{
			idx:       i,
			candidate: c,
			window:    window,
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].window > ranked[j].window
	})

	ordered := make([]providers.FallbackCandidate, 0, len(candidates))
	for _, r := range ranked {
		ordered = append(ordered, r.candidate)
	}
	return ordered
}

func (al *AgentLoop) compactToBudget(
	ctx context.Context,
	agent *AgentInstance,
	messages []providers.Message,
	opts processOptions,
	model string,
	tools []providers.ToolDefinition,
	llmOpts map[string]any,
	candidates []providers.FallbackCandidate,
	forceEmergency bool,
) ([]providers.Message, error) {
	guard := normalizeContextGuard(agent.ContextGuard)
	if !guard.Enabled {
		return messages, nil
	}

	requestedOutput := getRequestedOutputTokens(llmOpts, agent.MaxTokens)
	contextWindow := al.resolveContextWindowForTurn(agent, candidates, guard, requestedOutput)
	budget := computeContextBudget(contextWindow, requestedOutput, guard)

	predicted, countSource := al.countInputTokens(ctx, agent.Provider, model, messages, tools, llmOpts)
	al.logBudgetDecision(agent, opts, budget, compactionStageNone, predicted, countSource)

	if predicted <= budget.EffectiveInputLimit && !forceEmergency {
		return messages, nil
	}

	al.compactionTriggeredTotal.Add(1)
	working := cloneMessages(messages)

	var stages []compactionStage
	if forceEmergency {
		stages = []compactionStage{compactionStageEmergency}
	} else {
		stages = []compactionStage{
			compactionStagePrune,
			compactionStageSummary,
			compactionStageEmergency,
		}
	}

	maxPasses := guard.MaxCompactionPasses
	if maxPasses > len(stages) {
		maxPasses = len(stages)
	}

	for i := 0; i < maxPasses; i++ {
		stage := stages[i]
		changed := false

		switch stage {
		case compactionStagePrune:
			working, changed = pruneLowValueHistoricalPayloads(working, guard.PreserveRecentMessages)
		case compactionStageSummary:
			working, changed = al.summarizeAndRebuildMessages(agent, opts, working)
		case compactionStageEmergency:
			next := emergencyTrimMessages(working, guard.EmergencyInputRatio, guard.PreserveRecentMessages)
			changed = !messagesEqual(working, next)
			working = next
		}

		predicted, countSource = al.countInputTokens(ctx, agent.Provider, model, working, tools, llmOpts)
		al.logBudgetDecision(agent, opts, budget, stage, predicted, countSource)

		if !changed {
			al.compactionStageFailTotal.Add(1)
		}

		if predicted <= budget.EffectiveInputLimit {
			return working, nil
		}
	}

	al.contextBudgetUnfitTotal.Add(1)
	return nil, &RequestError{
		Code: ErrCodeContextBudgetUnfit,
		Message: fmt.Sprintf(
			"context budget unfit: predicted_input_tokens=%d effective_input_limit=%d",
			predicted,
			budget.EffectiveInputLimit,
		),
	}
}

func (al *AgentLoop) summarizeAndRebuildMessages(
	agent *AgentInstance,
	opts processOptions,
	current []providers.Message,
) ([]providers.Message, bool) {
	beforeSummary := agent.Sessions.GetSummary(opts.SessionKey)
	beforeHistory := agent.Sessions.GetHistory(opts.SessionKey)

	al.summarizeSession(agent, opts.SessionKey)

	afterSummary := agent.Sessions.GetSummary(opts.SessionKey)
	afterHistory := agent.Sessions.GetHistory(opts.SessionKey)
	rebuilt := agent.ContextBuilder.BuildMessages(
		afterHistory,
		afterSummary,
		"",
		nil,
		opts.Channel,
		opts.ChatID,
		opts.SenderID,
		opts.SenderDisplayName,
	)

	changed := beforeSummary != afterSummary || !messagesEqual(beforeHistory, afterHistory) || !messagesEqual(current, rebuilt)
	return rebuilt, changed
}

func pruneLowValueHistoricalPayloads(messages []providers.Message, preserveRecent int) ([]providers.Message, bool) {
	if len(messages) == 0 {
		return messages, false
	}
	if preserveRecent <= 0 {
		preserveRecent = defaultContextGuardPreserveRecentMessages
	}

	trimBefore := len(messages) - preserveRecent
	if trimBefore < 1 {
		trimBefore = 1
	}

	updated := cloneMessages(messages)
	changed := false

	for i := 1; i < trimBefore; i++ {
		msg := &updated[i]
		switch {
		case msg.Role == "tool" && len(msg.Content) > 1200:
			msg.Content = compactText(msg.Content, 450, "\n[tool output truncated for context budget]")
			changed = true
		case msg.Role == "assistant" && len(msg.ToolCalls) == 0 && len(msg.Content) > 1800:
			msg.Content = compactText(msg.Content, 800, "\n[assistant content truncated for context budget]")
			changed = true
		case msg.Role == "assistant" && len(msg.ToolCalls) > 0 && len(msg.Content) > 800:
			msg.Content = compactText(msg.Content, 300, "\n[assistant tool preface truncated]")
			changed = true
		}
	}

	return updated, changed
}

func compactText(text string, keepRunes int, suffix string) string {
	if keepRunes < 0 {
		keepRunes = 0
	}
	runes := []rune(text)
	if len(runes) <= keepRunes {
		return text
	}
	trimmed := strings.TrimSpace(string(runes[:keepRunes]))
	return trimmed + suffix
}

func emergencyTrimMessages(messages []providers.Message, retentionRatio float64, preserveRecent int) []providers.Message {
	if len(messages) <= 2 {
		return cloneMessages(messages)
	}
	if retentionRatio <= 0 || retentionRatio > 1 {
		retentionRatio = defaultContextGuardEmergencyInputRatio
	}
	if preserveRecent <= 0 {
		preserveRecent = defaultContextGuardPreserveRecentMessages
	}

	system := messages[0]
	conversationCount := len(messages) - 1
	keepConversation := int(math.Ceil(float64(conversationCount) * retentionRatio))
	if keepConversation < 1 {
		keepConversation = 1
	}

	start := len(messages) - keepConversation
	if start < 1 {
		start = 1
	}

	recentStart := len(messages) - preserveRecent
	if recentStart < 1 {
		recentStart = 1
	}
	if recentStart < start {
		start = recentStart
	}

	if idx := findLatestUserMessageIndex(messages); idx > 0 && idx < start {
		start = idx
	}

	if boundary := findLatestToolBoundaryStart(messages); boundary > 0 && boundary < start {
		start = boundary
	}

	conversation := cloneMessages(messages[start:])
	if boundary := findLatestToolBoundaryStart(messages); boundary >= start {
		localBoundary := boundary - start
		if localBoundary >= 0 && localBoundary < len(conversation) &&
			conversation[localBoundary].Role == "assistant" &&
			len(conversation[localBoundary].ToolCalls) > 0 {
			prevValid := localBoundary > 0 &&
				(conversation[localBoundary-1].Role == "user" || conversation[localBoundary-1].Role == "tool")
			if !prevValid {
				bridge := -1
				for i := localBoundary - 1; i >= 0; i-- {
					if conversation[i].Role == "user" || conversation[i].Role == "tool" {
						bridge = i
						break
					}
				}
				if bridge >= 0 {
					fixed := make([]providers.Message, 0, len(conversation)-(localBoundary-bridge-1))
					fixed = append(fixed, conversation[:bridge+1]...)
					fixed = append(fixed, conversation[localBoundary:]...)
					conversation = fixed
				} else {
					for i := boundary - 1; i >= 1; i-- {
						if messages[i].Role == "user" || messages[i].Role == "tool" {
							conversation = append([]providers.Message{messages[i]}, conversation...)
							break
						}
					}
				}
			}
		}
	}

	trimmed := make([]providers.Message, 0, 1+len(conversation))
	trimmed = append(trimmed, system)
	trimmed = append(trimmed, conversation...)
	trimmed = sanitizeHistoryForProvider(trimmed)
	if len(trimmed) == 0 {
		return []providers.Message{system}
	}
	if trimmed[0].Role != "system" {
		trimmed = append([]providers.Message{system}, trimmed...)
	}
	return trimmed
}

func findLatestUserMessageIndex(messages []providers.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return i
		}
	}
	return -1
}

func findLatestToolBoundaryStart(messages []providers.Message) int {
	for i := len(messages) - 1; i >= 1; i-- {
		msg := messages[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}

		expected := make(map[string]struct{}, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				expected[tc.ID] = struct{}{}
			}
		}
		if len(expected) == 0 {
			continue
		}

		for j := i + 1; j < len(messages); j++ {
			next := messages[j]
			if next.Role != "tool" {
				break
			}
			if _, ok := expected[next.ToolCallID]; ok {
				delete(expected, next.ToolCallID)
			}
		}

		if len(expected) == 0 {
			return i
		}
	}
	return -1
}

func cloneMessages(messages []providers.Message) []providers.Message {
	out := make([]providers.Message, len(messages))
	copy(out, messages)
	return out
}

func messagesEqual(a, b []providers.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role ||
			a[i].Content != b[i].Content ||
			a[i].ToolCallID != b[i].ToolCallID ||
			len(a[i].ToolCalls) != len(b[i].ToolCalls) {
			return false
		}
	}
	return true
}

func (al *AgentLoop) logBudgetDecision(
	agent *AgentInstance,
	opts processOptions,
	budget contextBudget,
	stage compactionStage,
	predicted int,
	countSource string,
) {
	fields := map[string]any{
		"agent_id":                 agent.ID,
		"session_key":              opts.SessionKey,
		"compaction_stage":         string(stage),
		"predicted_input_tokens":   predicted,
		"effective_input_limit":    budget.EffectiveInputLimit,
		"requested_output_tokens":  budget.RequestedOutputTokens,
		"safety_margin":            budget.SafetyMarginTokens,
		"context_window":           budget.ContextWindow,
		"target_input_tokens":      budget.TargetInputTokens,
		"emergency_input_tokens":   budget.EmergencyInputTokens,
		"count_source":             countSource,
		"compaction_triggered":     al.compactionTriggeredTotal.Load(),
		"compaction_stage_fail":    al.compactionStageFailTotal.Load(),
		"context_budget_unfit":     al.contextBudgetUnfitTotal.Load(),
		"provider_context_400":     al.providerContext400Total.Load(),
		"context_guard_debug_dump": agent.ContextGuard.DebugDump,
	}
	if stage == compactionStageNone {
		logger.DebugCF("agent", "Context budget pre-check", fields)
		return
	}
	logger.DebugCF("agent", "Context budget compaction stage", fields)
}
