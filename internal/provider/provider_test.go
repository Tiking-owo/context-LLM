package provider

import (
	"net/http"
	"testing"
	"time"

	"context-LLM/internal/config"
)

func TestResponseTextForChatCompletions(t *testing.T) {
	response := map[string]any{
		"choices": []any{
			map[string]any{"message": map[string]any{"content": "answer"}},
		},
	}
	if got := responseText(response); got != "answer" {
		t.Fatalf("got %q, want answer", got)
	}
}

func TestContextTooLargeDetection(t *testing.T) {
	result := Result{Raw: map[string]any{"error": "Your input exceeds the context window of this model."}}
	if !result.ContextTooLarge() {
		t.Fatal("expected context-window error to be detected")
	}
}

func TestRetryableStatusClassification(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 599} {
		if !isRetryableStatus(status) {
			t.Fatalf("status %d should be retryable", status)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusNotImplemented} {
		if isRetryableStatus(status) {
			t.Fatalf("status %d should not be retryable", status)
		}
	}
}

func TestHTTPErrorHonorsRetryAfter(t *testing.T) {
	result := httpErrorResult(http.StatusTooManyRequests, `{"error":{"code":"rate_limited"}}`, "2")
	if result.HTTPStatus != http.StatusTooManyRequests || !result.Retryable || result.RetryAfter != 2*time.Second || result.RetryClass != "rate_limit" {
		t.Fatalf("unexpected retry metadata: %#v", result)
	}
}

func TestHTTPErrorClassifiesCooldownAndUsageLimit(t *testing.T) {
	cooldown := httpErrorResult(http.StatusTooManyRequests, `{"error":{"code":"model_cooldown","message":"cooling down"}}`, "")
	if !cooldown.Retryable || cooldown.RetryClass != "model_cooldown" {
		t.Fatalf("cooldown was not classified correctly: %#v", cooldown)
	}
	usage := httpErrorResult(http.StatusTooManyRequests, `{"error":{"code":"usage_limit_reached","message":"usage limit reached"}}`, "")
	if usage.Retryable || usage.RetryClass != "usage_limit" {
		t.Fatalf("usage limit was not classified correctly: %#v", usage)
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	when := time.Now().Add(3 * time.Second).UTC().Truncate(time.Second)
	delay := parseRetryAfter(when.Format(http.TimeFormat))
	if delay <= 0 || delay > 4*time.Second {
		t.Fatalf("unexpected HTTP-date delay: %s", delay)
	}
}

func TestBuildPayloadAddsStructuredOutput(t *testing.T) {
	chatEndpoint, chatPayload := buildPayload(config.Config{APIMode: "chat-completions"}, "input", true)
	if chatEndpoint != "/chat/completions" {
		t.Fatalf("unexpected chat endpoint: %s", chatEndpoint)
	}
	if _, ok := chatPayload["response_format"]; !ok {
		t.Fatal("chat payload is missing response_format")
	}

	responsesEndpoint, responsesPayload := buildPayload(config.Config{APIMode: "responses"}, "input", true)
	if responsesEndpoint != "/responses" {
		t.Fatalf("unexpected responses endpoint: %s", responsesEndpoint)
	}
	text, ok := responsesPayload["text"].(map[string]any)
	if !ok {
		t.Fatalf("responses payload is missing text format: %#v", responsesPayload)
	}
	format, ok := text["format"].(map[string]any)
	if !ok || format["type"] != "json_object" {
		t.Fatalf("unexpected responses format: %#v", text)
	}
}

func TestBuildPayloadAddsReasoningParameters(t *testing.T) {
	defaultChat, defaultPayload := buildPayload(config.Config{APIMode: "chat-completions", ReasoningEffort: "default"}, "input", false)
	if defaultChat != "/chat/completions" {
		t.Fatalf("unexpected default endpoint: %s", defaultChat)
	}
	if _, exists := defaultPayload["reasoning_effort"]; exists {
		t.Fatal("default reasoning effort must not modify the chat payload")
	}

	_, chatPayload := buildPayload(config.Config{APIMode: "chat-completions", ReasoningEffort: "high", ReasoningSummary: "detailed"}, "input", false)
	if chatPayload["reasoning_effort"] != "high" {
		t.Fatalf("chat reasoning effort missing: %#v", chatPayload)
	}
	if _, exists := chatPayload["reasoning"]; exists {
		t.Fatal("chat completions must not receive reasoning.summary")
	}

	_, responsesPayload := buildPayload(config.Config{APIMode: "responses", ReasoningEffort: "high", ReasoningSummary: "detailed"}, "input", false)
	reasoning, ok := responsesPayload["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" || reasoning["summary"] != "detailed" {
		t.Fatalf("unexpected responses reasoning payload: %#v", responsesPayload)
	}
}

func TestStructuredOutputFallbackDetection(t *testing.T) {
	if !structuredOutputUnsupported(http.StatusBadRequest, `{"error":"response_format is unsupported"}`) {
		t.Fatal("expected response_format error to trigger fallback")
	}
	if structuredOutputUnsupported(http.StatusBadRequest, `{"error":"invalid model"}`) {
		t.Fatal("unrelated 400 error should not trigger fallback")
	}
}

func TestFinishReasonForTruncatedChatResponse(t *testing.T) {
	result := Result{Raw: map[string]any{
		"choices": []any{map[string]any{"finish_reason": "length"}},
	}}
	if result.FinishReason() != "length" {
		t.Fatalf("unexpected finish reason: %q", result.FinishReason())
	}
}

func TestMetadataPreservesResponseFieldsButStripsContent(t *testing.T) {
	raw := map[string]any{
		"id":              "resp_123",
		"model":           "model-a",
		"usage":           map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		"_extracted_text": "answer text",
		"choices": []any{
			map[string]any{
				"finish_reason": "stop",
				"message":       map[string]any{"content": "answer text"},
			},
		},
	}
	metadata := Metadata(raw)
	if metadata["id"] != "resp_123" || metadata["model"] != "model-a" {
		t.Fatalf("metadata fields were not preserved: %#v", metadata)
	}
	if _, exists := metadata["_extracted_text"]; exists {
		t.Fatal("extracted text should not be stored as metadata")
	}
	choice := metadata["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "stop" {
		t.Fatalf("finish reason was not preserved: %#v", choice)
	}
	if _, exists := choice["message"].(map[string]any)["content"]; exists {
		t.Fatal("message content should be stripped from metadata")
	}
}
