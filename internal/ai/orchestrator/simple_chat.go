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

// Stream delegates directly to the provider-backed chat service, pricing
// each request's usage when the model's prices are known. recordUsage, which
// may be nil, is told what each request was billed for.
func (o *SimpleChatOrchestrator) Stream(ctx context.Context, req domain.ChatRequest, recordUsage func(domain.Usage)) (<-chan domain.ClientEvent, error) {
	model, err := o.service.FindModel(req.Model)
	if err != nil {
		return nil, err
	}
	stream, err := o.service.ChatStream(ctx, req)
	if err != nil {
		return nil, err
	}
	meter := usageMeter{model: model, record: recordUsage}

	out := make(chan domain.ClientEvent)
	go func() {
		defer close(out)
		for {
			select {
			case event, ok := <-stream:
				if !ok {
					return
				}

				var usage *domain.Usage
				if event.Usage != nil {
					usage = meter.meter(*event.Usage)
				}
				clientEvent := domain.ClientEvent{
					TextDelta:      event.TextDelta,
					ReasoningDelta: event.ReasoningDelta,
					ReasoningState: domain.CloneRawMessage(event.ReasoningState),
					ToolCalls:      append([]domain.ToolCall(nil), event.ToolCalls...),
					Usage:          usage,
					Done:           event.Done,
				}
				if event.Err != nil {
					clientEvent.ErrorMessage = event.Err.Error()
				}

				if !emitEvent(ctx, out, clientEvent) {
					meter.drain(stream)
					return
				}
			case <-ctx.Done():
				meter.drain(stream)
				return
			}
		}
	}()

	return out, nil
}
