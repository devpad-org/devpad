package openaichat

import (
	"net/http"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/domain"
	aiprovider "github.com/devpad-org/devpad/internal/ai/provider"
	"github.com/devpad-org/devpad/internal/ai/testkit"
)

func TestAdapterContract(t *testing.T) {
	testkit.RunAdapterContract(t, testkit.AdapterContract{
		NewAdapter: func() aiprovider.Adapter {
			return NewAdapter()
		},
		Configure: func(adapter aiprovider.Adapter, serverURL string, client *http.Client) {
			tested := adapter.(*Adapter)
			tested.baseURL = serverURL
			tested.client = client
		},
		ExpectedProviderID:   "openai",
		ExpectedProviderName: "OpenAI",
		ExpectedProtocol:     aiprovider.ProtocolOpenAIChat,
		ExpectedModelIDs:     []string{"gpt-5.4"},
		Request: aiprovider.StreamRequest{
			Model: "gpt-5.4",
			Turns: []domain.Turn{domain.NewTextTurn(domain.RoleUser, "hello")},
		},
	})
}
