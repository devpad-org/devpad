package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// noopAI satisfies aiRouteHandler so the real route table can be built
// without the AI module.
type noopAI struct{}

func (noopAI) Handler(http.ResponseWriter, *http.Request)                  {}
func (noopAI) HandleListModels(http.ResponseWriter, *http.Request)         {}
func (noopAI) HandleListAgents(http.ResponseWriter, *http.Request)         {}
func (noopAI) HandleCreateAgent(http.ResponseWriter, *http.Request)        {}
func (noopAI) HandleUpdateAgent(http.ResponseWriter, *http.Request)        {}
func (noopAI) HandleDeleteAgent(http.ResponseWriter, *http.Request)        {}
func (noopAI) HandleChat(http.ResponseWriter, *http.Request)               {}
func (noopAI) HandleAgentChat(http.ResponseWriter, *http.Request)          {}
func (noopAI) HandleApproveCommand(http.ResponseWriter, *http.Request)     {}
func (noopAI) HandleAnswerQuestion(http.ResponseWriter, *http.Request)     {}
func (noopAI) HandleCreateAgentRun(http.ResponseWriter, *http.Request)     {}
func (noopAI) HandleListAgentRuns(http.ResponseWriter, *http.Request)      {}
func (noopAI) HandleGetAgentRun(http.ResponseWriter, *http.Request)        {}
func (noopAI) HandleAgentRunEvents(http.ResponseWriter, *http.Request)     {}
func (noopAI) HandleCancelAgentRun(http.ResponseWriter, *http.Request)     {}
func (noopAI) HandleListAttention(http.ResponseWriter, *http.Request)      {}
func (noopAI) HandleListConversations(http.ResponseWriter, *http.Request)  {}
func (noopAI) HandleCreateConversation(http.ResponseWriter, *http.Request) {}
func (noopAI) HandleDeleteConversation(http.ResponseWriter, *http.Request) {}
func (noopAI) HandleGetMessages(http.ResponseWriter, *http.Request)        {}
func (noopAI) HandleSaveMessages(http.ResponseWriter, *http.Request)       {}
func (noopAI) HandleListProviders(http.ResponseWriter, *http.Request)      {}
func (noopAI) HandleUpdateProvider(http.ResponseWriter, *http.Request)     {}

// Workspaces only come into existence as a project's workspace or an agent
// sandbox, so the API has no way to create or rename a standalone one.
func TestRoutesHaveNoStandaloneWorkspaceCreation(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, noopAI{}, nil)

	// Nothing under /api matches; only the SPA fallback would answer.
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/workspaces", nil),
		httptest.NewRequest(http.MethodPut, "/api/workspaces/1", nil),
	} {
		if _, pattern := mux.Handler(req); pattern != "/" {
			t.Errorf("%s %s matched %q, want only the SPA fallback", req.Method, req.URL.Path, pattern)
		}
	}

	// Projects are how a workspace is made now.
	if _, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, "/api/projects/1/workspace", nil)); pattern != "POST /api/projects/{id}/workspace" {
		t.Fatalf("expected the project workspace route, got %q", pattern)
	}
}
