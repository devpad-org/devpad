package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// ProcessInfo describes a process running inside a workspace container.
type ProcessInfo struct {
	PID            int    `json:"pid"`
	PPID           int    `json:"ppid"`
	User           string `json:"user"`
	State          string `json:"state"`
	Command        string `json:"command"`
	MemoryBytes    uint64 `json:"memoryBytes"`
	CPUTimeSeconds uint64 `json:"cpuTimeSeconds"`
	Killable       bool   `json:"killable"`
}

// ProcessList is the response returned by the workspace agent process API.
type ProcessList struct {
	Processes []ProcessInfo `json:"processes"`
}

// ListProcesses lists processes running inside the workspace container.
func (c *Client) ListProcesses(ctx context.Context) (*ProcessList, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/processes", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}

	var result ProcessList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

// KillProcess sends SIGTERM to a process inside the workspace container.
func (c *Client) KillProcess(ctx context.Context, pid int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/processes/"+url.PathEscape(fmt.Sprintf("%d", pid))+"/kill", nil)
	if err != nil {
		return err
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("killing process: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return parseError(resp)
	}
	return nil
}
