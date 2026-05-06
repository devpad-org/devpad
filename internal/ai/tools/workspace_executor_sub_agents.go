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
	childAgentDefaultWaitSeconds = 120
	childAgentMaxWaitSeconds     = 600
	childAgentMaxWaitRunCount    = 20
)

type subAgentWaitResult struct {
	RunID    int64  `json:"runId"`
	Status   string `json:"status,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Error    string `json:"error,omitempty"`
	TimedOut bool   `json:"timedOut,omitempty"`
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

	model, _ := params["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		model = req.Model
	}

	child, err := e.childRunner.StartChildAgentRun(ctx, ChildAgentRunRequest{
		ParentRunID:    req.CurrentRunID,
		UserID:         req.UserID,
		WorkspaceID:    req.WorkspaceID,
		ConversationID: req.ConversationID,
		Model:          model,
		Prompt:         prompt,
		Thinking:       cloneThinking(req.Thinking),
	})
	if err != nil {
		return toolFailure("spawn_sub_agent", "Error: %v", err)
	}

	response := map[string]any{
		"runId":          child.ID,
		"parentRunId":    child.ParentRunID,
		"workspaceId":    child.WorkspaceID,
		"conversationId": child.ConversationID,
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
				response["message"] = fmt.Sprintf("Sub-agent run #%d is still running after %d seconds.", child.ID, timeoutSeconds)
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
					results[index] = subAgentWaitResult{RunID: runID, TimedOut: true}
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
		"message": fmt.Sprintf(
			"Waited for %d sub-agent run(s): %d completed, %d failed, %d timed out.",
			len(results),
			completed,
			failed,
			timedOut,
		),
	}

	return marshalToolResponse("wait_for_sub_agents", response)
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
