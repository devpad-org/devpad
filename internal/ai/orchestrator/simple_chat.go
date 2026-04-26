package orchestrator

import (
	"context"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// SimpleChatOrchestrator is a thin wrapper for non-agent streaming chat.
type SimpleChatOrchestrator struct {
	service ChatService
}

// NewSimpleChatOrchestrator creates a simple pass-through chat orchestrator.
func NewSimpleChatOrchestrator(service ChatService) *SimpleChatOrchestrator {
	return &SimpleChatOrchestrator{service: service}
}

// Stream delegates directly to the provider-backed chat service.
func (o *SimpleChatOrchestrator) Stream(ctx context.Context, req domain.ChatRequest) (<-chan domain.ClientEvent, error) {
	stream, err := o.service.ChatStream(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan domain.ClientEvent)
	go func() {
		defer close(out)
		for {
			select {
			case event, ok := <-stream:
				if !ok {
					return
				}

				clientEvent := domain.ClientEvent{
					TextDelta:      event.TextDelta,
					ReasoningDelta: event.ReasoningDelta,
					ReasoningState: domain.CloneRawMessage(event.ReasoningState),
					ToolCalls:      append([]domain.ToolCall(nil), event.ToolCalls...),
					Done:           event.Done,
				}
				if event.Err != nil {
					clientEvent.ErrorMessage = event.Err.Error()
				}

				if !emitEvent(ctx, out, clientEvent) {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}
