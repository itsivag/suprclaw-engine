package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/providers"
)

const (
	defaultContextGuardSafetyMarginTokens     = 2048
	defaultContextGuardTargetInputRatio       = 0.78
	defaultContextGuardPrecheckTriggerRatio   = 0.85
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

type contextPrecheckDecision struct {
	ShouldRunPrecheck    bool
	PredictedInputTokens int
	CountSource          string
	Budget               contextBudget
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
	if guard.PrecheckTriggerRatio <= 0 || guard.PrecheckTriggerRatio > 1 {
		guard.PrecheckTriggerRatio = defaultContextGuardPrecheckTriggerRatio
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

func estimateInputTokensWithOverhead(tokens int) int {
	// Conservative fallback: estimator + 10% overhead + fixed reserve.
	est := tokens + tokens/10 + estimatorFixedReserveTokens
	if est < estimatorFixedReserveTokens {
		return estimatorFixedReserveTokens
	}
	return est
}

func latestUsageAnchor(
	messages []providers.Message,
) (int, *providers.UsageInfo, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "assistant" || msg.Usage == nil || msg.Usage.TotalTokens <= 0 {
			continue
		}
		return i, msg.Usage, true
	}
	return -1, nil, false
}

func (al *AgentLoop) estimateInputTokens(messages []providers.Message) (int, string) {
	if idx, usage, ok := latestUsageAnchor(messages); ok {
		tailTokens := 0
		if idx+1 < len(messages) {
			tailTokens = al.estimateTokens(messages[idx+1:])
		}
		return usage.TotalTokens + estimateInputTokensWithOverhead(tailTokens), "usage_anchor"
	}
	return estimateInputTokensWithOverhead(al.estimateTokens(messages)), "estimator"
}

func (al *AgentLoop) selectInputTokensForBudget(
	messages []providers.Message,
) (int, string) {
	return al.estimateInputTokens(messages)
}

func budgetCheckSignature(
	messages []providers.Message,
	model string,
	tools []providers.ToolDefinition,
	llmOpts map[string]any,
	forceEmergency bool,
) string {
	h := fnv.New64a()
	write := func(parts ...string) {
		for _, part := range parts {
			_, _ = h.Write([]byte(part))
			_, _ = h.Write([]byte{0})
		}
	}

	write("force_emergency", strconv.FormatBool(forceEmergency))
	write("model", model)
	if maxTokens := getRequestedOutputTokens(llmOpts, 0); maxTokens > 0 {
		write("max_tokens", strconv.Itoa(maxTokens))
	}

	for _, msg := range messages {
		write("role", msg.Role, "content", msg.Content, "tool_call_id", msg.ToolCallID, "reasoning", msg.ReasoningContent)
		if msg.Usage != nil {
			write(
				"usage_prompt_tokens", strconv.Itoa(msg.Usage.PromptTokens),
				"usage_completion_tokens", strconv.Itoa(msg.Usage.CompletionTokens),
				"usage_total_tokens", strconv.Itoa(msg.Usage.TotalTokens),
			)
		}
		write("media_count", strconv.Itoa(len(msg.Media)))
		for _, media := range msg.Media {
			write("media", media)
		}
		for _, tc := range msg.ToolCalls {
			write("tool_call", tc.ID, tc.Name)
			if tc.Function != nil {
				write("func_name", tc.Function.Name, "func_args", tc.Function.Arguments)
			}
			if len(tc.Arguments) > 0 {
				if raw, err := json.Marshal(tc.Arguments); err == nil {
					write("args_json", string(raw))
				}
			}
		}
	}

	for _, td := range tools {
		write("tool_def", td.Type, td.Function.Name, td.Function.Description)
		if raw, err := json.Marshal(td.Function.Parameters); err == nil {
			write("tool_params", string(raw))
		}
	}

	return strconv.FormatUint(h.Sum64(), 16)
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

	predicted, countSource := al.selectInputTokensForBudget(
		messages,
	)
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

		predicted, countSource = al.selectInputTokensForBudget(
			working,
		)
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

func (al *AgentLoop) decideContextBudgetPrecheck(
	agent *AgentInstance,
	messages []providers.Message,
	llmOpts map[string]any,
	candidates []providers.FallbackCandidate,
	forceEmergency bool,
) contextPrecheckDecision {
	guard := normalizeContextGuard(agent.ContextGuard)
	requestedOutput := getRequestedOutputTokens(llmOpts, agent.MaxTokens)
	contextWindow := al.resolveContextWindowForTurn(agent, candidates, guard, requestedOutput)
	budget := computeContextBudget(contextWindow, requestedOutput, guard)

	predicted, countSource := al.selectInputTokensForBudget(messages)
	hasUsageAnchor := countSource == "usage_anchor"

	shouldRun := true
	if !forceEmergency && hasUsageAnchor {
		triggerThreshold := float64(budget.EffectiveInputLimit) * guard.PrecheckTriggerRatio
		shouldRun = float64(predicted) >= triggerThreshold
	}

	return contextPrecheckDecision{
		ShouldRunPrecheck:    shouldRun,
		PredictedInputTokens: predicted,
		CountSource:          countSource,
		Budget:               budget,
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
		opts.SessionKey,
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
