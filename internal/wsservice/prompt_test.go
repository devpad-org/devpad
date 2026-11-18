package wsservice

import (
	"context"
	"strings"
	"testing"

	aitools "github.com/devpad-org/devpad/internal/ai/tools"
)

func TestServicesPromptNamesTheVariablesOfEachServiceWithAContainer(t *testing.T) {
	services := []*WorkspaceService{
		{WorkspaceID: 5, ServiceType: ServicePostgres, ContainerID: "c1", Status: StatusRunning, Config: `{"port":5432,"defaultUser":"devpad","defaultPass":"s3cret","defaultDb":"devpad"}`},
		{WorkspaceID: 5, ServiceType: ServiceMeilisearch, ContainerID: "c2", Status: StatusStopped, Config: `{"port":7700,"defaultPass":"master"}`},
		// Never started, so it has no container and nothing reaches it.
		{WorkspaceID: 5, ServiceType: ServiceMariaDB, Status: StatusStopped, Config: `{"port":3306}`},
	}

	got := servicesPrompt(services)
	for _, want := range []string{
		"## Services",
		"- postgres: `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DATABASE`\n",
		"- meilisearch: `MEILISEARCH_HOST`, `MEILISEARCH_PORT`, `MEILISEARCH_URL`, `MEILISEARCH_MASTER_KEY`, `MEILISEARCH_API_KEY`, `MEILI_MASTER_KEY` (stopped)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected the prompt to contain %q, got:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"mariadb", "s3cret", "master\n", "=master"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("expected the prompt not to contain %q, got:\n%s", unwanted, got)
		}
	}

	if got := servicesPrompt(services[2:]); got != "" {
		t.Errorf("expected no prompt when no service has a container, got:\n%s", got)
	}
}

func TestPromptProviderContributesTheWorkspacesServices(t *testing.T) {
	repo := &fakeRepository{services: map[int64]*WorkspaceService{
		1: {ID: 1, WorkspaceID: 5, ServiceType: ServicePostgres, ContainerID: "c1", Status: StatusRunning, Config: `{"port":5432}`},
	}}
	p := NewPromptProvider(NewService(repo, &fakeContainerManager{}))

	got := p.Contribute(context.Background(), aitools.Scope{UserID: 1, WorkspaceID: 5}).Prompt
	if !strings.Contains(got, "`POSTGRES_HOST`") {
		t.Fatalf("expected the workspace's postgres in the prompt, got:\n%s", got)
	}
	if got := p.Contribute(context.Background(), aitools.Scope{UserID: 1, WorkspaceID: 6}).Prompt; got != "" {
		t.Fatalf("expected nothing for a workspace without services, got:\n%s", got)
	}
}
