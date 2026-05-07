package domain

import (
	"errors"
	"testing"
)

func TestValidateThinkingRequest_AllowsEffortForSupportedModel(t *testing.T) {
	model := Model{
		Thinking: ThinkingCapability{
			Supported:        true,
			EnabledByDefault: false,
			CanDisable:       true,
			SupportedEfforts: []string{"low", "medium"},
			DefaultEffort:    "medium",
		},
	}
	req := ChatRequest{
		Thinking: &ThinkingConfig{Effort: "low"},
	}

	if err := ValidateThinkingRequest(model, req); err != nil {
		t.Fatalf("expected effort to validate, got %v", err)
	}
	if !ThinkingEnabledForRequest(model, req) {
		t.Fatal("expected effort selection to enable thinking")
	}
}

func TestValidateThinkingRequest_RejectsUnsupportedEffort(t *testing.T) {
	model := Model{
		Thinking: ThinkingCapability{
			Supported:        true,
			CanDisable:       true,
			SupportedEfforts: []string{"low", "medium"},
			DefaultEffort:    "medium",
		},
	}
	req := ChatRequest{
		Thinking: &ThinkingConfig{Effort: "xhigh"},
	}

	err := ValidateThinkingRequest(model, req)
	if !errors.Is(err, ErrThinkingEffortInvalid) {
		t.Fatalf("expected invalid effort error, got %v", err)
	}
}

func TestValidateThinkingRequest_RejectsEffortWhenThinkingDisabled(t *testing.T) {
	disabled := false
	model := Model{
		Thinking: ThinkingCapability{
			Supported:        true,
			EnabledByDefault: true,
			CanDisable:       true,
			SupportedEfforts: []string{"low", "medium"},
			DefaultEffort:    "medium",
		},
	}
	req := ChatRequest{
		Thinking: &ThinkingConfig{Enabled: &disabled, Effort: "low"},
	}

	err := ValidateThinkingRequest(model, req)
	if !errors.Is(err, ErrThinkingEffortRequiresThinking) {
		t.Fatalf("expected effort requires thinking error, got %v", err)
	}
}

func TestValidateThinkingRequest_RejectsEffortWhenSelectionUnsupported(t *testing.T) {
	model := Model{
		Thinking: ThinkingCapability{
			Supported:  true,
			CanDisable: true,
		},
	}
	req := ChatRequest{
		Thinking: &ThinkingConfig{Effort: "medium"},
	}

	err := ValidateThinkingRequest(model, req)
	if !errors.Is(err, ErrThinkingEffortNotSupported) {
		t.Fatalf("expected effort not supported error, got %v", err)
	}
}
