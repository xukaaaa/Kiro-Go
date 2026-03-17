package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

var fireworksHttpClient = &http.Client{
	Timeout: 5 * time.Minute,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
	},
}

type FireworksCallback struct {
	OnChunk    func(chunk string) error
	OnComplete func(inputTokens, outputTokens int64) error
	OnError    func(err error)
}

func CallFireworksAPI(apiKey, baseURL string, reqBody []byte, callback *FireworksCallback) error {
	log.Printf("[Fireworks] Starting API call to %s", baseURL)

	// Filter unsupported parameters
	var req map[string]interface{}
	if err := json.Unmarshal(reqBody, &req); err != nil {
		log.Printf("[Fireworks] Failed to unmarshal request: %v", err)
		return fmt.Errorf("unmarshal request: %w", err)
	}

	log.Printf("[Fireworks] Original request model: %v, stream: %v, max_tokens: %v", req["model"], req["stream"], req["max_tokens"])

	// Filter unsupported parameters
	delete(req, "disable_parallel_tool_use")
	delete(req, "stop_sequences")
	delete(req, "context_management")
	delete(req, "thinking")
	delete(req, "metadata")
	delete(req, "output_config")

	// Handle tool_choice: "none"
	if tc, ok := req["tool_choice"].(map[string]interface{}); ok {
		if tc["type"] == "none" {
			log.Printf("[Fireworks] Removing tools due to tool_choice:none")
			delete(req, "tools")
			delete(req, "tool_choice")
		}
	}

	// Fireworks constraint: max_tokens > 4096 requires stream=true
	maxTokens := 0
	if mt, ok := req["max_tokens"].(float64); ok {
		maxTokens = int(mt)
	}
	isStream := req["stream"] == true

	if maxTokens > 4096 && !isStream {
		log.Printf("[Fireworks] max_tokens=%d > 4096 but stream=false, forcing stream=true", maxTokens)
		req["stream"] = true
	}

	filteredBody, _ := json.Marshal(req)
	log.Printf("[Fireworks] Filtered request body length: %d bytes", len(filteredBody))

	// Log filtered request keys to verify filtering worked
	filteredKeys := make([]string, 0, len(req))
	for k := range req {
		filteredKeys = append(filteredKeys, k)
	}
	log.Printf("[Fireworks] Filtered request keys: %v", filteredKeys)

	// Log first 1000 chars of filtered body
	if len(filteredBody) > 1000 {
		log.Printf("[Fireworks] Filtered body preview: %s...", string(filteredBody[:1000]))
	} else {
		log.Printf("[Fireworks] Filtered body: %s", string(filteredBody))
	}

	endpoint := baseURL + "/messages"
	log.Printf("[Fireworks] Calling endpoint: %s", endpoint)

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(filteredBody))
	if err != nil {
		log.Printf("[Fireworks] Failed to create HTTP request: %v", err)
		return fmt.Errorf("create request: %w", err)
	}

	// Log API key (masked)
	maskedKey := apiKey
	if len(apiKey) > 20 {
		maskedKey = apiKey[:10] + "..." + apiKey[len(apiKey)-4:]
	} else if len(apiKey) > 10 {
		maskedKey = apiKey[:10] + "..."
	}
	log.Printf("[Fireworks] Using API key: %s (length: %d)", maskedKey, len(apiKey))

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := fireworksHttpClient.Do(httpReq)
	if err != nil {
		log.Printf("[Fireworks] HTTP request failed: %v", err)
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("[Fireworks] Response status: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[Fireworks] API error response: %s", string(body))
		return fmt.Errorf("fireworks API error %d: %s", resp.StatusCode, string(body))
	}

	// Check final stream value (may have been forced to true)
	finalStream := req["stream"] == true
	log.Printf("[Fireworks] Stream mode: %v", finalStream)

	if finalStream {
		return parseAnthropicSSE(resp.Body, callback)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[Fireworks] Failed to decode response: %v", err)
		return fmt.Errorf("decode response: %w", err)
	}

	log.Printf("[Fireworks] Non-streaming response received, type: %v", result["type"])

	resultJSON, _ := json.Marshal(result)
	if callback.OnChunk != nil {
		callback.OnChunk(string(resultJSON))
	}

	if callback.OnComplete != nil {
		if usage, ok := result["usage"].(map[string]interface{}); ok {
			inputTokens := int64(usage["input_tokens"].(float64))
			outputTokens := int64(usage["output_tokens"].(float64))
			log.Printf("[Fireworks] Token usage - input: %d, output: %d", inputTokens, outputTokens)
			callback.OnComplete(inputTokens, outputTokens)
		}
	}
	return nil
}

func parseAnthropicSSE(body io.Reader, callback *FireworksCallback) error {
	log.Printf("[Fireworks] Starting SSE parsing")

	// Read all body first to log it
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		log.Printf("[Fireworks] Failed to read SSE body: %v", err)
		return err
	}

	log.Printf("[Fireworks] SSE body length: %d bytes", len(bodyBytes))
	if len(bodyBytes) > 0 {
		if len(bodyBytes) > 2000 {
			log.Printf("[Fireworks] SSE body preview: %s...", string(bodyBytes[:2000]))
		} else {
			log.Printf("[Fireworks] SSE body: %s", string(bodyBytes))
		}
	} else {
		log.Printf("[Fireworks] SSE body is EMPTY!")
	}

	scanner := bufio.NewScanner(bytes.NewReader(bodyBytes))
	var inputTokens, outputTokens int64
	eventCount := 0
	var currentEventType string
	var currentData string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines
		if line == "" {
			// Process accumulated event
			if currentData != "" {
				eventCount++
				var event map[string]interface{}
				if err := json.Unmarshal([]byte(currentData), &event); err == nil {
					eventType := event["type"]
					if eventCount <= 5 || eventType == "message_delta" || eventType == "message_stop" {
						log.Printf("[Fireworks] SSE event #%d type: %v (event: %s)", eventCount, eventType, currentEventType)
					}

					// Extract token usage
					if event["type"] == "message_delta" {
						if usage, ok := event["usage"].(map[string]interface{}); ok {
							if ot, ok := usage["output_tokens"].(float64); ok {
								outputTokens = int64(ot)
								log.Printf("[Fireworks] Updated output_tokens: %d", outputTokens)
							}
						}
					} else if event["type"] == "message_start" {
						if msg, ok := event["message"].(map[string]interface{}); ok {
							if usage, ok := msg["usage"].(map[string]interface{}); ok {
								if it, ok := usage["input_tokens"].(float64); ok {
									inputTokens = int64(it)
									log.Printf("[Fireworks] Got input_tokens: %d", inputTokens)
								}
							}
						}
					}

					// Send to callback
					if callback.OnChunk != nil {
						dataJSON, _ := json.Marshal(event)
						if err := callback.OnChunk(string(dataJSON)); err != nil {
							log.Printf("[Fireworks] OnChunk callback error: %v", err)
							return err
						}
					}
				}
				currentData = ""
				currentEventType = ""
			}
			continue
		}

		// Parse event type line
		if strings.HasPrefix(line, "event:") {
			currentEventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}

		// Parse data line
		if strings.HasPrefix(line, "data:") {
			currentData = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			continue
		}
	}

	log.Printf("[Fireworks] SSE parsing complete. Total events: %d, input_tokens: %d, output_tokens: %d", eventCount, inputTokens, outputTokens)

	if err := scanner.Err(); err != nil {
		log.Printf("[Fireworks] Scanner error: %v", err)
		if callback.OnError != nil {
			callback.OnError(err)
		}
		return err
	}

	if callback.OnComplete != nil {
		callback.OnComplete(inputTokens, outputTokens)
	}

	return nil
}
