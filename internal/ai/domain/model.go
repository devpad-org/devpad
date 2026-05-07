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
	Supported        bool     `json:"supported"`
	EnabledByDefault bool     `json:"enabledByDefault"`
	CanDisable       bool     `json:"canDisable"`
	SupportedEfforts []string `json:"supportedEfforts,omitempty"`
	// DefaultEffort is the effort selected when thinking is enabled and no effort is requested.
	DefaultEffort string `json:"defaultEffort,omitempty"`
}

// SupportsEffortSelection reports whether the model exposes selectable effort levels.
func (c ThinkingCapability) SupportsEffortSelection() bool {
	return len(c.SupportedEfforts) > 0
}

// SupportsEffort reports whether the supplied effort is one of the model's supported levels.
func (c ThinkingCapability) SupportsEffort(effort string) bool {
	for _, supported := range c.SupportedEfforts {
		if supported == effort {
			return true
		}
	}
	return false
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
