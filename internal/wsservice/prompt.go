package wsservice

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aitools "github.com/devpad-org/devpad/internal/ai/tools"
)

// PromptProvider tells each run which services its workspace has and the
// environment variables that reach them. It offers no tools.
type PromptProvider struct {
	service Service
}

var _ aitools.ToolProvider = (*PromptProvider)(nil)

// NewPromptProvider creates a PromptProvider.
func NewPromptProvider(service Service) *PromptProvider {
	return &PromptProvider{service: service}
}

// Contribute names the variables of each service with a container: those
// are in the container's environment or, for a service added since it was
// made, given by the agent to every process it starts.
func (p *PromptProvider) Contribute(ctx context.Context, scope aitools.Scope) aitools.Contribution {
	services, err := p.service.List(ctx, scope.WorkspaceID)
	if err != nil {
		// A run goes on without it, as it would without any services.
		log.Printf("workspace %d: listing services for a run: %v", scope.WorkspaceID, err)
		return aitools.Contribution{}
	}
	return aitools.Contribution{Prompt: servicesPrompt(services)}
}

func (p *PromptProvider) ExecuteTool(context.Context, aitools.ExecutionRequest) (domain.ToolResultPart, bool) {
	return domain.ToolResultPart{}, false
}

// servicesPrompt lists a workspace's services by the names of the
// environment variables that reach them, never their values: the agent can
// read those from its environment.
func servicesPrompt(services []*WorkspaceService) string {
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
			names = append(names, "`"+name+"`")
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
