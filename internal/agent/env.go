package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// SetServiceEnv replaces the variables of the workspace's services, which
// the agent gives every terminal and command it starts from now on, over
// the container's own environment.
func (c *Client) SetServiceEnv(ctx context.Context, env []string) error {
	if env == nil {
		env = []string{}
	}
	payload, err := json.Marshal(map[string][]string{"env": env})
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/api/env/services", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doNoContent(req, "set service environment")
}
