package wsservice

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/workspace"
)

// PromptProvider tells each run which services its workspace has and the
// environment variables that reach them. It offers no tools.
type PromptProvider struct {
	service    Service
	workspaces workspace.AccessService
	container  container.Manager
}

var _ aitools.ToolProvider = (*PromptProvider)(nil)

// NewPromptProvider creates a PromptProvider.
func NewPromptProvider(service Service, workspaces workspace.AccessService, cm container.Manager) *PromptProvider {
	return &PromptProvider{service: service, workspaces: workspaces, container: cm}
}

func (p *PromptProvider) Contribute(ctx context.Context, scope aitools.Scope) aitools.Contribution {
	services, err := p.service.List(ctx, scope.WorkspaceID)
	if err != nil {
		// A run goes on without it, as it would without any services.
		log.Printf("workspace %d: listing services for a run: %v", scope.WorkspaceID, err)
		return aitools.Contribution{}
	}
	if len(services) == 0 {
		return aitools.Contribution{}
	}
	// A service added after the workspace's container was made is not in
	// its environment, so it is not offered.
	ws, err := p.workspaces.Get(ctx, scope.UserID, scope.WorkspaceID)
	if err != nil || ws.ContainerID == "" {
		return aitools.Contribution{}
	}
	env, err := p.container.GetEnv(ctx, ws.ContainerID)
	if err != nil {
		log.Printf("workspace %d: reading its environment for a run: %v", scope.WorkspaceID, err)
		return aitools.Contribution{}
	}
	inEnv := make(map[string]bool, len(env))
	for _, v := range env {
		name, _, _ := strings.Cut(v, "=")
		inEnv[name] = true
	}
	return aitools.Contribution{Prompt: servicesPrompt(services, inEnv)}
}

func (p *PromptProvider) ExecuteTool(context.Context, aitools.ExecutionRequest) (domain.ToolResultPart, bool) {
	return domain.ToolResultPart{}, false
}

// servicesPrompt lists a workspace's services by the names of the
// environment variables that reach them, never their values: the agent can
// read those from its environment.
func servicesPrompt(services []*WorkspaceService, inEnv map[string]bool) string {
	var lines []string
	for _, svc := range services {
		def, ok := lookupDefinition(svc.ServiceType)
		if !ok || svc.ContainerID == "" {
			continue
		}
		var cfg ServiceConfig
		if err := json.Unmarshal([]byte(svc.Config), &cfg); err != nil {
			continue
		}
		vars := def.connectionVars(svc.ServiceType, sidecarContainerName(svc.WorkspaceID, svc.ServiceType), cfg)
		names := make([]string, 0, len(vars))
		for _, v := range vars {
			name, _, _ := strings.Cut(v, "=")
			if inEnv[name] {
				names = append(names, "`"+name+"`")
			}
		}
		if len(names) == 0 {
			continue
		}
		line := fmt.Sprintf("- %s: %s", svc.ServiceType, strings.Join(names, ", "))
		if svc.Status != StatusRunning {
			line += " (stopped)"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Services\n\n" +
		"This workspace has services of its own, which no other workspace shares. " +
		"Their connection details are in your environment, under these variables:\n\n" +
		strings.Join(lines, "\n") + "\n\n" +
		"Use them to run and test the code.\n"
}
