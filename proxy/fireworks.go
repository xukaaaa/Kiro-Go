package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"kiro-api-proxy/config"
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
	OnComplete func(inputTokens, outputTokens, cacheReadTokens int64, keyID string) error
	OnError    func(err error, statusCode int, keyID string)
}

type FireworksBillingResponse struct {
	LineItems []struct {
		Category   string `json:"category"`
		TotalCost  struct {
			Units string `json:"units"`
			Nanos int32  `json:"nanos"`
		} `json:"totalCost"`
	} `json:"lineItems"`
}

func convertToUSD(units string, nanos int32) float64 {
	var unitsInt int64
	fmt.Sscanf(units, "%d", &unitsInt)
	return float64(unitsInt) + float64(nanos)/1e9
}

func CallFireworksAPI(key *config.FireworksKey, baseURL string, reqBody []byte, callback *FireworksCallback) error {
	log.Printf("[Fireworks] Starting API call to %s with key %s", baseURL, key.ID)

	apiKey := key.Key

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

	// Always force streaming mode
	originalStream := req["stream"]
	req["stream"] = true
	log.Printf("[Fireworks] Forcing stream=true (original: %v)", originalStream)

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
		if callback.OnError != nil {
			callback.OnError(fmt.Errorf("fireworks API error %d: %s", resp.StatusCode, string(body)), resp.StatusCode, key.ID)
		}
		return fmt.Errorf("fireworks API error %d: %s", resp.StatusCode, string(body))
	}

	// Always use streaming mode
	log.Printf("[Fireworks] Processing streaming response for key %s", key.ID)
	return parseAnthropicSSE(resp.Body, callback, key.ID)
}

func parseAnthropicSSE(body io.Reader, callback *FireworksCallback, keyID string) error {
	log.Printf("[Fireworks] Starting SSE parsing for key %s", keyID)

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 50*1024*1024) // 50MB max buffer

	var inputTokens, outputTokens, cacheReadTokens int64
	eventCount := 0
	var sseBuffer strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		// Forward raw line to callback
		if callback.OnChunk != nil {
			sseBuffer.WriteString(line)
			sseBuffer.WriteString("\n")

			// On empty line, flush accumulated SSE event
			if strings.TrimSpace(line) == "" && sseBuffer.Len() > 1 {
				if err := callback.OnChunk(sseBuffer.String()); err != nil {
					log.Printf("[Fireworks] OnChunk callback error: %v", err)
					return err
				}
				sseBuffer.Reset()
			}
		}

		// Parse for token tracking (keep existing logic)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data:") {
			eventCount++
			dataJSON := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))

			var event map[string]interface{}
			if err := json.Unmarshal([]byte(dataJSON), &event); err == nil {
				eventType := event["type"]
				if eventCount <= 5 || eventType == "message_delta" || eventType == "message_stop" {
					log.Printf("[Fireworks] SSE event #%d type: %v", eventCount, eventType)
				}

				// Extract token usage - ONLY from message_delta (has actual values)
				if event["type"] == "message_delta" {
					if usage, ok := event["usage"].(map[string]interface{}); ok {
						// Log full usage object for debugging
						usageJSON, _ := json.Marshal(usage)
						log.Printf("[Fireworks] Full usage from message_delta: %s", string(usageJSON))

						// Get input_tokens (actual value, not placeholder)
						if it, ok := usage["input_tokens"].(float64); ok && it > 0 {
							inputTokens = int64(it)
							log.Printf("[Fireworks] Got input_tokens: %d", inputTokens)
						}

						// Get output_tokens
						if ot, ok := usage["output_tokens"].(float64); ok {
							outputTokens = int64(ot)
							log.Printf("[Fireworks] Got output_tokens: %d", outputTokens)
						}

						// Get cache_read_input_tokens
						if crt, ok := usage["cache_read_input_tokens"].(float64); ok {
							cacheReadTokens = int64(crt)
							log.Printf("[Fireworks] Got cache_read_input_tokens: %d", cacheReadTokens)
						}
					}
				}
				// message_start only has placeholder (0 tokens), skip it
				// All actual usage comes from message_delta
			}
		}
	}

	log.Printf("[Fireworks] SSE parsing complete for key %s. Total events: %d, input_tokens: %d, output_tokens: %d, cache_read_tokens: %d", keyID, eventCount, inputTokens, outputTokens, cacheReadTokens)

	if err := scanner.Err(); err != nil {
		log.Printf("[Fireworks] Scanner error: %v", err)
		if callback.OnError != nil {
			callback.OnError(err, 0, keyID)
		}
		return err
	}

	if callback.OnComplete != nil {
		log.Printf("[Fireworks] Calling OnComplete with final usage - input: %d, output: %d, cache_read: %d", inputTokens, outputTokens, cacheReadTokens)
		callback.OnComplete(inputTokens, outputTokens, cacheReadTokens, keyID)
	}

	return nil
}

// FetchFireworksUsage fetches current month usage from Fireworks billing API
func FetchFireworksUsage(apiKey, accountID string) (float64, error) {
	now := time.Now().UTC()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	startOfNextMonth := startOfMonth.AddDate(0, 1, 0)

	url := fmt.Sprintf("https://api.fireworks.ai/v1/accounts/%s/billing/summary?startTime=%s&endTime=%s",
		accountID,
		startOfMonth.Format("2006-01-02T15:04:05Z"),
		startOfNextMonth.Format("2006-01-02T15:04:05Z"))

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := fireworksHttpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var billing FireworksBillingResponse
	if err := json.NewDecoder(resp.Body).Decode(&billing); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	var totalCost float64
	for _, item := range billing.LineItems {
		totalCost += convertToUSD(item.TotalCost.Units, item.TotalCost.Nanos)
	}

	return totalCost, nil
}
