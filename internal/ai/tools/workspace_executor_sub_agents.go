package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

const (
	childAgentDefaultWaitSeconds = 600
	childAgentMaxWaitSeconds     = 600
	childAgentMaxWaitRunCount    = 20
)

type subAgentWaitResult struct {
	RunID    int64  `json:"runId"`
	Status   string `json:"status,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Error    string `json:"error,omitempty"`
	TimedOut bool   `json:"timedOut,omitempty"`
	// StillRunning says the wait ended, not the child: it can be waited on again.
	StillRunning bool `json:"stillRunning,omitempty"`
}

type availableAgentPayload struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Purpose   string `json:"purpose,omitempty"`
	Scope     string `json:"scope"`
	IsDefault bool   `json:"isDefault"`
	IsGlobal  bool   `json:"isGlobal"`
	// Model and Effort are what a child of the agent runs on when
	// spawn_sub_agent names neither.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// subAgentModelPayload is a model a child of the run can use.
type subAgentModelPayload struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Current       bool     `json:"current,omitempty"`
	Vision        bool     `json:"vision"`
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
	// Prices are in US dollars per million tokens, left out when unknown.
	InputPrice  *float64 `json:"inputUsdPerMillionTokens,omitempty"`
	OutputPrice *float64 `json:"outputUsdPerMillionTokens,omitempty"`
}

func (e *WorkspaceExecutor) listAvailableAgents(ctx context.Context, req ExecutionRequest) domain.ToolResultPart {
	if e.agentLister == nil {
		return toolFailure("list_available_agents", "Error: agent lister is not available")
	}
	if req.UserID <= 0 {
		return toolFailure("list_available_agents", "Error: user ID is required")
	}
	if req.WorkspaceID <= 0 {
		return toolFailure("list_available_agents", "Error: workspace ID is required")
	}

	agents, err := e.agentLister.ListAgents(ctx, req.UserID, req.WorkspaceID)
	if err != nil {
		return toolFailure("list_available_agents", "Error: %v", err)
	}

	payload := make([]availableAgentPayload, 0, len(agents))
	for _, agent := range agents {
		entry := availableAgentPayload{
			ID:        agent.ID,
			Name:      agent.Name,
			Purpose:   agent.Purpose,
			Scope:     agentScope(agent),
			IsDefault: agent.IsDefault,
			IsGlobal:  agent.IsGlobal,
		}
		model, thinking, err := e.childModel(ctx, req, agent.ID, "", "")
		if err != nil {
			return toolFailure("list_available_agents", "Error: %v", err)
		}
		entry.Model = model
		if thinking != nil {
			entry.Effort = thinking.Effort
		}
		payload = append(payload, entry)
	}

	models, err := e.subAgentModels(ctx, req)
	if err != nil {
		return toolFailure("list_available_agents", "Error: %v", err)
	}

	return marshalToolResponse("list_available_agents", map[string]any{
		"agents":       payload,
		"currentModel": req.Model,
		"models":       models,
		"message": "Use an agent id as spawn_sub_agent.agent_id to assign a child run to that agent. " +
			"A child runs on its agent's model and effort unless you pass one of these models as spawn_sub_agent.model, " +
			"and an effort that model supports as spawn_sub_agent.effort. Only these models, from the current model's provider, can run a child.",
	})
}

// subAgentModels lists the models a child of the run can use: those of the
// run's own model's provider, which the user has a key for.
func (e *WorkspaceExecutor) subAgentModels(ctx context.Context, req ExecutionRequest) ([]subAgentModelPayload, error) {
	if e.models == nil {
		return nil, nil
	}
	current, err := e.models.FindModel(req.Model)
	if err != nil {
		return nil, fmt.Errorf("looking up the current model %s: %w", req.Model, err)
	}
	all, err := e.models.ListModels(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("listing models: %w", err)
	}
	models := []subAgentModelPayload{}
	for _, m := range all {
		if m.ProviderID != current.ProviderID || !m.Configured {
			continue
		}
		entry := subAgentModelPayload{
			ID:            m.ID,
			Name:          m.Name,
			Current:       m.ID == current.ID,
			Vision:        m.Vision,
			Efforts:       m.Thinking.SupportedEfforts,
			DefaultEffort: m.Thinking.DefaultEffort,
		}
		if m.Pricing != nil {
			input, output := m.Pricing.Input*1e6, m.Pricing.Output*1e6
			entry.InputPrice, entry.OutputPrice = &input, &output
		}
		models = append(models, entry)
	}
	return models, nil
}

// childModel picks what a child of agentID runs on: model when it is named,
// else the user's default for the agent when that is from the parent's
// provider, else the parent's model. An effort, when given, must be one the
// picked model supports. Without one, a child on the default takes the
// default's effort, one on the parent's model the parent's thinking, and any
// other its model's default. The run service refuses a child on another
// provider's model, so naming one fails there.
func (e *WorkspaceExecutor) childModel(ctx context.Context, req ExecutionRequest, agentID, model, effort string) (string, *domain.ThinkingConfig, error) {
	var thinking *domain.ThinkingConfig
	if model == "" && e.defaults != nil {
		d, err := e.defaults.DefaultsFor(ctx, req.UserID, agentID, "")
		if err != nil {
			return "", nil, fmt.Errorf("looking up the agent's default model: %w", err)
		}
		if d != nil && e.sameProvider(d.Model, req.Model) {
			model = d.Model
			if d.Effort != "" {
				thinking = &domain.ThinkingConfig{Effort: d.Effort}
			}
		}
	}
	if model == "" {
		model = req.Model
	}
	if effort != "" {
		if e.models == nil {
			return "", nil, errors.New("cannot check the effort: no model catalog is available")
		}
		m, err := e.models.FindModel(model)
		if err != nil {
			return "", nil, fmt.Errorf("looking up model %s: %w", model, err)
		}
		if err := domain.ValidateThinkingEffort(m, effort); err != nil {
			return "", nil, fmt.Errorf("effort %q for %s: %w", effort, model, err)
		}
		return model, &domain.ThinkingConfig{Effort: effort}, nil
	}
	if model == req.Model && thinking == nil {
		thinking = cloneThinking(req.Thinking)
	}
	return model, thinking, nil
}

func (e *WorkspaceExecutor) spawnSubAgent(ctx context.Context, req ExecutionRequest, params map[string]any) domain.ToolResultPart {
	if req.CurrentRunID <= 0 {
		return toolFailure("spawn_sub_agent", "Error: spawn_sub_agent is only available from a persisted agent run")
	}
	if e.childRunner == nil {
		return toolFailure("spawn_sub_agent", "Error: sub-agent runner is not available")
	}

	prompt, _ := params["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return toolFailure("spawn_sub_agent", "Error: prompt is required")
	}

	agentID, _ := params["agent_id"].(string)
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = domain.DefaultAgentID
	}

	model, _ := params["model"].(string)
	effort, _ := params["effort"].(string)
	model, thinking, err := e.childModel(ctx, req, agentID, strings.TrimSpace(model), strings.TrimSpace(effort))
	if err != nil {
		return toolFailure("spawn_sub_agent", "Error: %v", err)
	}

	child, err := e.childRunner.StartChildAgentRun(ctx, ChildAgentRunRequest{
		ParentRunID:    req.CurrentRunID,
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		ConversationID: req.ConversationID,
		AgentID:        agentID,
		Model:          model,
		Prompt:         prompt,
		Thinking:       thinking,
	})
	if err != nil {
		return toolFailure("spawn_sub_agent", "Error: %v", err)
	}

	response := map[string]any{
		"runId":          child.ID,
		"parentRunId":    child.ParentRunID,
		"workspaceId":    child.WorkspaceID,
		"conversationId": child.ConversationID,
		"agentId":        child.AgentID,
		"model":          child.Model,
		"status":         child.Status,
		"message":        fmt.Sprintf("Sub-agent run #%d started.", child.ID),
	}

	waitForResult, _ := params["wait_for_result"].(bool)
	if waitForResult {
		timeoutSeconds := childAgentWaitSeconds(params)

		waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
		result, waitErr := e.childRunner.WaitChildAgentRun(waitCtx, req.UserID, req.CurrentRunID, child.ID)
		cancel()
		if waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) {
				response["timedOut"] = true
				response["stillRunning"] = true
				response["message"] = fmt.Sprintf("Sub-agent run #%d is still running after %d seconds. Call wait_for_sub_agents with this run ID to collect its result.", child.ID, timeoutSeconds)
				return marshalToolResponse("spawn_sub_agent", response)
			}
			return toolFailure("spawn_sub_agent", "Error waiting for sub-agent result: %v", waitErr)
		}
		response["status"] = result.Status
		response["summary"] = result.Summary
		if result.Error != "" {
			response["error"] = result.Error
			response["message"] = fmt.Sprintf("Sub-agent run #%d finished with an error.", child.ID)
		} else {
			response["message"] = fmt.Sprintf("Sub-agent run #%d completed.", child.ID)
		}
	}

	return marshalToolResponse("spawn_sub_agent", response)
}

func agentScope(agent domain.Agent) string {
	switch {
	case agent.IsDefault:
		return "default"
	case agent.IsBuiltIn:
		return "built-in"
	case agent.IsGlobal:
		return "global"
	default:
		return "workspace"
	}
}

// sameProvider reports whether two models are from the same provider. It is
// false when either cannot be found.
func (e *WorkspaceExecutor) sameProvider(a, b string) bool {
	if e.models == nil {
		return false
	}
	ma, err := e.models.FindModel(a)
	if err != nil {
		return false
	}
	mb, err := e.models.FindModel(b)
	if err != nil {
		return false
	}
	return ma.ProviderID == mb.ProviderID
}

func (e *WorkspaceExecutor) waitForSubAgents(ctx context.Context, req ExecutionRequest, params map[string]any) domain.ToolResultPart {
	if req.CurrentRunID <= 0 {
		return toolFailure("wait_for_sub_agents", "Error: wait_for_sub_agents is only available from a persisted agent run")
	}
	if e.childRunner == nil {
		return toolFailure("wait_for_sub_agents", "Error: sub-agent runner is not available")
	}

	runIDs, err := parseSubAgentRunIDs(params)
	if err != nil {
		return toolFailure("wait_for_sub_agents", "Error: %v", err)
	}

	timeoutSeconds := childAgentWaitSeconds(params)
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	results := make([]subAgentWaitResult, len(runIDs))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	setFirstErr := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	hasFirstErr := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return firstErr != nil
	}

	for index, runID := range runIDs {
		wg.Add(1)
		go func(index int, runID int64) {
			defer wg.Done()

			result, waitErr := e.childRunner.WaitChildAgentRun(waitCtx, req.UserID, req.CurrentRunID, runID)
			if waitErr != nil {
				if errors.Is(waitErr, context.DeadlineExceeded) {
					results[index] = subAgentWaitResult{RunID: runID, TimedOut: true, StillRunning: true}
					return
				}
				if errors.Is(waitErr, context.Canceled) && hasFirstErr() {
					return
				}
				setFirstErr(fmt.Errorf("waiting for sub-agent run #%d: %w", runID, waitErr))
				return
			}

			results[index] = childAgentResultPayload(runID, result)
		}(index, runID)
	}

	wg.Wait()

	mu.Lock()
	err = firstErr
	mu.Unlock()
	if err != nil {
		return toolFailure("wait_for_sub_agents", "Error: %v", err)
	}

	completed, failed, timedOut := summarizeSubAgentWaitResults(results)
	response := map[string]any{
		"results":   results,
		"completed": completed,
		"failed":    failed,
		"timedOut":  timedOut,
		"message":   subAgentWaitMessage(results, completed, failed, timedOut),
	}

	return marshalToolResponse("wait_for_sub_agents", response)
}

func subAgentWaitMessage(results []subAgentWaitResult, completed, failed, timedOut int) string {
	message := fmt.Sprintf(
		"Waited for %d sub-agent run(s): %d completed, %d failed, %d still running.",
		len(results),
		completed,
		failed,
		timedOut,
	)
	if timedOut == 0 {
		return message
	}

	running := make([]string, 0, timedOut)
	for _, result := range results {
		if result.TimedOut {
			running = append(running, fmt.Sprintf("#%d", result.RunID))
		}
	}
	return fmt.Sprintf(
		"%s Still working: %s. Call wait_for_sub_agents again with those run IDs to collect their results.",
		message,
		strings.Join(running, ", "),
	)
}

func parseSubAgentRunIDs(params map[string]any) ([]int64, error) {
	values, ok := params["run_ids"].([]any)
	if !ok {
		if value, ok := params["run_id"]; ok {
			values = []any{value}
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("run_ids is required")
	}
	if len(values) > childAgentMaxWaitRunCount {
		return nil, fmt.Errorf("run_ids cannot contain more than %d IDs", childAgentMaxWaitRunCount)
	}

	runIDs := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		number, ok := value.(float64)
		if !ok {
			return nil, fmt.Errorf("run_ids must contain numeric IDs")
		}
		runID := int64(number)
		if runID <= 0 || float64(runID) != number {
			return nil, fmt.Errorf("run_ids must contain positive integer IDs")
		}
		if _, exists := seen[runID]; exists {
			return nil, fmt.Errorf("run_ids contains duplicate ID %d", runID)
		}
		seen[runID] = struct{}{}
		runIDs = append(runIDs, runID)
	}

	return runIDs, nil
}

func childAgentResultPayload(runID int64, result *ChildAgentRunResult) subAgentWaitResult {
	if result == nil {
		return subAgentWaitResult{RunID: runID}
	}
	resultRunID := result.RunID
	if resultRunID == 0 {
		resultRunID = runID
	}
	return subAgentWaitResult{
		RunID:   resultRunID,
		Status:  result.Status,
		Summary: result.Summary,
		Error:   result.Error,
	}
}

func summarizeSubAgentWaitResults(results []subAgentWaitResult) (completed, failed, timedOut int) {
	for _, result := range results {
		if result.TimedOut {
			timedOut++
			continue
		}
		switch result.Status {
		case string(domain.AgentRunCompleted):
			completed++
		case string(domain.AgentRunFailed), string(domain.AgentRunCancelled):
			failed++
		}
	}
	return completed, failed, timedOut
}

func childAgentWaitSeconds(params map[string]any) int {
	timeoutSeconds := childAgentDefaultWaitSeconds
	if value, ok := params["timeout_seconds"].(float64); ok {
		timeoutSeconds = int(value)
	}
	return clamp(timeoutSeconds, 1, childAgentMaxWaitSeconds)
}
