package provider

import "github.com/devpad-org/devpad/internal/ai/domain"

// Registry resolves providers and models without exposing storage concerns.
type Registry interface {
	All() []Adapter
	ByProviderID(providerID string) (Adapter, bool)
	ResolveModel(modelID string) (Adapter, domain.Model, bool)
}

type registry struct {
	adapters     []Adapter
	byProviderID map[string]Adapter
}

// NewRegistry creates a model/provider resolver for the supplied adapters.
func NewRegistry(adapters ...Adapter) Registry {
	reg := &registry{
		adapters:     make([]Adapter, 0, len(adapters)),
		byProviderID: make(map[string]Adapter, len(adapters)),
	}

	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}

		providerID := adapter.ProviderID()
		reg.adapters = append(reg.adapters, adapter)
		if _, exists := reg.byProviderID[providerID]; !exists {
			reg.byProviderID[providerID] = adapter
		}
	}

	return reg
}

func (r *registry) All() []Adapter {
	return append([]Adapter(nil), r.adapters...)
}

func (r *registry) ByProviderID(providerID string) (Adapter, bool) {
	adapter, ok := r.byProviderID[providerID]
	return adapter, ok
}

func (r *registry) ResolveModel(modelID string) (Adapter, domain.Model, bool) {
	for _, adapter := range r.adapters {
		for _, model := range adapter.Models() {
			if model.ID == modelID {
				return adapter, model, true
			}
		}
	}

	return nil, domain.Model{}, false
}
