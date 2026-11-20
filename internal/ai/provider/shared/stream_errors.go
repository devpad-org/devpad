package shared

import (
	"encoding/json"
	"fmt"
	"log"
)

// Stream decoders return these errors rather than passing a damaged, cut-off,
// or rejected response off as a finished one. Their messages are shown to the
// user, so they name the provider and say what happened in plain terms.

const maxLoggedEventBytes = 500

// MalformedEventError reports a stream event that is not valid JSON. The raw
// event is logged so a change in the provider's format can be diagnosed.
func MalformedEventError(provider, data string, err error) error {
	if len(data) > maxLoggedEventBytes {
		data = data[:maxLoggedEventBytes] + "…"
	}
	log.Printf("unreadable %s stream event %q: %v", provider, data, err)
	return fmt.Errorf("%s sent a response event that could not be read", provider)
}

// ReadStreamError reports a failure reading the response body, such as a
// dropped connection.
func ReadStreamError(provider string, err error) error {
	log.Printf("error reading %s response stream: %v", provider, err)
	return fmt.Errorf("error reading %s response stream", provider)
}

// IncompleteStreamError reports a stream that ended without the provider
// marking the response as finished.
func IncompleteStreamError(provider string) error {
	log.Printf("%s response stream ended before the response finished", provider)
	return fmt.Errorf("%s response stream ended before the response finished", provider)
}

// ProviderStreamError reports an error the provider sent inside the stream.
func ProviderStreamError(provider, message string) error {
	if message == "" {
		return fmt.Errorf("%s returned an error while streaming the response", provider)
	}
	return fmt.Errorf("%s returned an error: %s", provider, message)
}

// FinishReasonError returns the error for an OpenAI-compatible finish_reason
// that means the response did not finish normally, or nil when it did.
func FinishReasonError(provider, reason string) error {
	switch reason {
	case "stop", "tool_calls", "function_call":
		return nil
	case "length":
		return fmt.Errorf("%s stopped the response because it reached the output token limit", provider)
	case "model_length":
		return fmt.Errorf("%s stopped the response because the conversation reached the model's context limit", provider)
	case "content_filter":
		return fmt.Errorf("%s stopped the response with its content filter", provider)
	case "error":
		return fmt.Errorf("%s stopped the response because of an error", provider)
	default:
		// An unfamiliar reason may well be harmless, so it is logged for
		// follow-up rather than failing a response that may be complete.
		log.Printf("%s finished a response with unrecognized reason %q", provider, reason)
		return nil
	}
}

// ChatStreamErrorMessage extracts the error from an OpenAI-compatible stream
// chunk that carries one instead of choices. Providers send either
// {"error": {"message": ...}}, {"error": "..."}, or Mistral's
// {"object": "error", "message": ...}.
func ChatStreamErrorMessage(data []byte) (string, bool) {
	var payload struct {
		Object  string          `json:"object"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false
	}
	if payload.Object == "error" {
		return payload.Message, true
	}
	if len(payload.Error) == 0 || string(payload.Error) == "null" {
		return "", false
	}

	var message string
	if err := json.Unmarshal(payload.Error, &message); err == nil {
		return message, true
	}
	var detail struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload.Error, &detail); err == nil {
		return detail.Message, true
	}
	return "", true
}
