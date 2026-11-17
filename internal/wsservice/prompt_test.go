package wsservice

import (
	"strings"
	"testing"
)

func TestServicesPromptNamesTheVariablesInTheEnvironment(t *testing.T) {
	services := []*WorkspaceService{
		{WorkspaceID: 5, ServiceType: ServicePostgres, ContainerID: "c1", Status: StatusRunning, Config: `{"port":5432,"defaultUser":"devpad","defaultPass":"s3cret","defaultDb":"devpad"}`},
		{WorkspaceID: 5, ServiceType: ServiceMeilisearch, ContainerID: "c2", Status: StatusStopped, Config: `{"port":7700,"defaultPass":"master"}`},
		// Added after the container was made: not in its environment.
		{WorkspaceID: 5, ServiceType: ServiceMongoDB, ContainerID: "c3", Status: StatusRunning, Config: `{"port":27017}`},
		// Never started, so it has no container.
		{WorkspaceID: 5, ServiceType: ServiceMariaDB, Status: StatusStopped, Config: `{"port":3306}`},
	}
	inEnv := map[string]bool{}
	for _, name := range []string{"POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DATABASE", "MEILISEARCH_HOST", "MEILISEARCH_URL", "MARIADB_HOST"} {
		inEnv[name] = true
	}

	got := servicesPrompt(services, inEnv)
	for _, want := range []string{
		"## Services",
		"- postgres: `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DATABASE`\n",
		"- meilisearch: `MEILISEARCH_HOST`, `MEILISEARCH_URL` (stopped)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected the prompt to contain %q, got:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"mongodb", "mariadb", "s3cret", "master"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("expected the prompt not to contain %q, got:\n%s", unwanted, got)
		}
	}

	if got := servicesPrompt(services, map[string]bool{}); got != "" {
		t.Errorf("expected no prompt when no service is in the environment, got:\n%s", got)
	}
}
