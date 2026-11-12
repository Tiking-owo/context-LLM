package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"context-LLM/internal/config"
)

const instructions = "You are being evaluated for long-context reliability. Read all material supplied below.\n" +
	"Follow the authority order stated in the corpus. Answer every question, using only the corpus.\n" +
	"Return JSON only, with exactly one object for every question id, each containing answer and evidence.\n" +
	"For information not present, use answer INSUFFICIENT. Do not add commentary.\n\n"

type Result struct {
	Raw                      map[string]any
	Text                     string
	HTTPStatus               int
	TransportError           string
	Retryable                bool
	RetryAfter               time.Duration
	RetryClass               string
	StructuredOutput         bool
	StructuredOutputFallback bool
}

func Send(cfg config.Config, material, questions string) Result {
	userInput := "CORPUS START\n" + material + "\nCORPUS END\nQUESTIONS\n" + questions
	return sendWithStructuredOutput(cfg, userInput, cfg.StructuredOutput)
}

func sendWithStructuredOutput(cfg config.Config, userInput string, structured bool) Result {
	endpoint, payload := buildPayload(cfg, userInput, structured)
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{Raw: map[string]any{"transport_error": "MarshalError", "error": err.Error()}, TransportError: "MarshalError", RetryClass: "marshal_error"}
	}
	request, err := http.NewRequest(http.MethodPost, cfg.BaseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{Raw: map[string]any{"transport_error": "RequestError", "error": err.Error()}, TransportError: "RequestError", RetryClass: "request_error"}
	}
	request.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: cfg.Timeout}
	httpResult, err := client.Do(request)
	if err != nil {
		return Result{Raw: map[string]any{"transport_error": "TransportError", "error": err.Error()}, TransportError: "TransportError", Retryable: true, RetryClass: "transport"}
	}
	defer httpResult.Body.Close()
	data, err := io.ReadAll(httpResult.Body)
	if err != nil {
		return Result{Raw: map[string]any{"transport_error": "ReadError", "error": err.Error()}, TransportError: "ReadError", Retryable: true, RetryClass: "transport"}
	}
	if httpResult.StatusCode >= 400 {
		body := string(data)
		if structured && structuredOutputUnsupported(httpResult.StatusCode, body) {
			fallback := sendWithStructuredOutput(cfg, userInput, false)
			fallback.StructuredOutput = false
			fallback.StructuredOutputFallback = true
			if fallback.Raw == nil {
				fallback.Raw = map[string]any{}
			}
			fallback.Raw["_structured_output"] = false
			fallback.Raw["_structured_output_fallback"] = true
			return fallback
		}
		result := httpErrorResult(httpResult.StatusCode, body, httpResult.Header.Get("Retry-After"))
		result.StructuredOutput = structured
		return result
	}
	raw := map[string]any{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Result{Raw: map[string]any{"transport_error": "DecodeError", "error": err.Error()}, TransportError: "DecodeError", RetryClass: "decode_error"}
	}
	return Result{Raw: raw, Text: responseText(raw), HTTPStatus: httpResult.StatusCode, StructuredOutput: structured}
}

func buildPayload(cfg config.Config, userInput string, structured bool) (string, map[string]any) {
	endpoint := "/chat/completions"
	payload := map[string]any{}
	if cfg.APIMode == "chat-completions" {
		payload = map[string]any{
			"model": cfg.Model,
			"messages": []map[string]string{
				{"role": "developer", "content": instructions},
				{"role": "user", "content": userInput},
			},
			"max_tokens": cfg.MaxOutputTokens,
			"stream":     false,
		}
		if structured {
			payload["response_format"] = map[string]any{"type": "json_object"}
		}
		if cfg.ReasoningEffort != "" && cfg.ReasoningEffort != "default" {
			payload["reasoning_effort"] = cfg.ReasoningEffort
		}
	} else {
		endpoint = "/responses"
		payload = map[string]any{
			"model":             cfg.Model,
			"input":             instructions + userInput,
			"max_output_tokens": cfg.MaxOutputTokens,
			"store":             false,
		}
		if structured {
			payload["text"] = map[string]any{"format": map[string]any{"type": "json_object"}}
		}
		reasoning := map[string]any{}
		if cfg.ReasoningEffort != "" && cfg.ReasoningEffort != "default" {
			reasoning["effort"] = cfg.ReasoningEffort
		}
		if cfg.ReasoningSummary != "" && cfg.ReasoningSummary != "default" {
			reasoning["summary"] = cfg.ReasoningSummary
		}
		if len(reasoning) > 0 {
			payload["reasoning"] = reasoning
		}
	}
	return endpoint, payload
}

func structuredOutputUnsupported(status int, body string) bool {
	if status != http.StatusBadRequest {
		return false
	}
	detail := strings.ToLower(body)
	for _, marker := range []string{"response_format", "json_object", "json_schema", "text.format", "structured output"} {
		if strings.Contains(detail, marker) {
			return true
		}
	}
	return false
}

func (result Result) PromptTokens() *int {
	return usageToken(result.Raw, "prompt_tokens", "input_tokens")
}

func (result Result) CompletionTokens() *int {
	return usageToken(result.Raw, "completion_tokens", "output_tokens")
}

func (result Result) FinishReason() string {
	if choices, ok := result.Raw["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if reason, ok := choice["finish_reason"].(string); ok {
				return strings.ToLower(reason)
			}
		}
	}
	if status, ok := result.Raw["status"].(string); ok && strings.EqualFold(status, "incomplete") {
		return "length"
	}
	return ""
}

func Metadata(raw map[string]any) map[string]any {
	if raw == nil {
		return map[string]any{}
	}
	clone := make(map[string]any, len(raw))
	for key, value := range raw {
		if key == "_extracted_text" {
			continue
		}
		clone[key] = sanitizeMetadata(value)
	}
	return clone
}

func sanitizeMetadata(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		clone := make(map[string]any, len(typed))
		for key, child := range typed {
			if isContentKey(key) {
				continue
			}
			clone[key] = sanitizeMetadata(child)
		}
		return clone
	case []any:
		clone := make([]any, len(typed))
		for index, child := range typed {
			clone[index] = sanitizeMetadata(child)
		}
		return clone
	default:
		return value
	}
}

func isContentKey(key string) bool {
	switch key {
	case "content", "text", "input_text", "output_text", "reasoning_content":
		return true
	default:
		return false
	}
}

func (result Result) ContextTooLarge() bool {
	detail := strings.ToLower(stringValue(result.Raw["error"]))
	return strings.Contains(detail, "context_too_large") ||
		strings.Contains(detail, "exceeds the context window") ||
		strings.Contains(detail, "context length")
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return status >= 500 && status <= 599 && status != http.StatusNotImplemented
}

func httpErrorResult(status int, body, retryAfter string) Result {
	retryClass, retryable := classifyHTTPError(status, body)
	return Result{
		Raw:        map[string]any{"http_status": status, "error": body},
		HTTPStatus: status,
		Retryable:  retryable,
		RetryAfter: parseRetryAfter(retryAfter),
		RetryClass: retryClass,
	}
}

func classifyHTTPError(status int, body string) (string, bool) {
	detail := strings.ToLower(body)
	if status == http.StatusTooManyRequests {
		if strings.Contains(detail, "model_cooldown") || strings.Contains(detail, "cooling down") {
			return "model_cooldown", true
		}
		if strings.Contains(detail, "usage_limit") || strings.Contains(detail, "usage limit") {
			return "usage_limit", false
		}
		return "rate_limit", true
	}
	if status == http.StatusRequestTimeout {
		return "timeout", true
	}
	if status >= 500 && status <= 599 && status != http.StatusNotImplemented {
		return "server_error", true
	}
	if strings.Contains(detail, "context_too_large") || strings.Contains(detail, "exceeds the context window") {
		return "context_too_large", false
	}
	if strings.Contains(detail, "invalid api key") || strings.Contains(detail, "authentication") || strings.Contains(detail, "unauthorized") {
		return "authentication_error", false
	}
	if strings.Contains(detail, "invalid request") || strings.Contains(detail, "invalid_parameter") {
		return "invalid_request_error", false
	}
	return fmt.Sprintf("http_%d", status), isRetryableStatus(status)
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

func responseText(response map[string]any) string {
	if choices, ok := response["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				return contentText(message["content"])
			}
		}
	}
	parts := []string{}
	if output, ok := response["output"].([]any); ok {
		for _, item := range output {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			content, ok := itemMap["content"].([]any)
			if !ok {
				continue
			}
			for _, block := range content {
				blockMap, ok := block.(map[string]any)
				if !ok {
					continue
				}
				if blockMap["type"] == "output_text" {
					parts = append(parts, stringValue(blockMap["text"]))
				}
			}
		}
	}
	return strings.Join(parts, "")
}

func contentText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	parts := []string{}
	if blocks, ok := content.([]any); ok {
		for _, block := range blocks {
			if item, ok := block.(map[string]any); ok {
				parts = append(parts, stringValue(item["text"]))
			}
		}
	}
	return strings.Join(parts, "")
}

func usageToken(response map[string]any, names ...string) *int {
	usage, ok := response["usage"].(map[string]any)
	if !ok {
		return nil
	}
	for _, name := range names {
		if value := intValue(usage[name]); value > 0 {
			return &value
		}
	}
	return nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func intValue(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	case json.Number:
		result, _ := number.Int64()
		return int(result)
	default:
		return 0
	}
}
