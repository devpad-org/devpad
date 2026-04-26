package domain

// Model describes an AI model offered by a provider.
type Model struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	ProviderID string             `json:"providerId"`
	Thinking   ThinkingCapability `json:"thinking"`
}

// ThinkingCapability describes whether a model supports reasoning/thinking mode.
type ThinkingCapability struct {
	Supported        bool `json:"supported"`
	EnabledByDefault bool `json:"enabledByDefault"`
	CanDisable       bool `json:"canDisable"`
}

// ModelInfo is a model with its provider's enabled/configured status.
type ModelInfo struct {
	Model
	ProviderName string `json:"providerName"`
	Configured   bool   `json:"configured"`
}

// ModelByID returns the matching model when present.
func ModelByID(models []Model, modelID string) (Model, bool) {
	for _, model := range models {
		if model.ID == modelID {
			return model, true
		}
	}

	return Model{}, false
}
