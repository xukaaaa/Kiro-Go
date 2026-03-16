package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

type FireworksStreamCallback struct {
	OnChunk    func(chunk string) error
	OnComplete func(usage map[string]interface{}) error
	OnError    func(err error)
}

func CallFireworksAPI(apiKey, baseURL string, req *OpenAIRequest, callback *FireworksStreamCallback) error {
	requestID := fmt.Sprintf("fw_%d", time.Now().UnixNano())
	fmt.Printf("[Fireworks API][%s] Starting request for model: %s, stream: %v\n", requestID, req.Model, req.Stream)

	reqBody, err := json.Marshal(req)
	if err != nil {
		fmt.Printf("[Fireworks API][%s] ERROR marshaling request: %v\n", requestID, err)
		return fmt.Errorf("marshal request: %w", err)
	}
	fmt.Printf("[Fireworks API][%s] Request body size: %d bytes, messages: %d\n", requestID, len(reqBody), len(req.Messages))

	endpoint := baseURL + "/chat/completions"
	fmt.Printf("[Fireworks API][%s] Endpoint: %s\n", requestID, endpoint)
	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(reqBody))
	if err != nil {
		fmt.Printf("[Fireworks API][%s] ERROR creating HTTP request: %v\n", requestID, err)
		return fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	// Mask API key for security (show first 8 and last 4 chars)
	maskedKey := apiKey
	if len(apiKey) > 12 {
		maskedKey = apiKey[:8] + "..." + apiKey[len(apiKey)-4:]
	}
	fmt.Printf("[Fireworks API][%s] Using API key: %s (length: %d)\n", requestID, maskedKey, len(apiKey))
	fmt.Printf("[Fireworks API][%s] Sending HTTP request...\n", requestID)

	startTime := time.Now()
	resp, err := fireworksHttpClient.Do(httpReq)
	elapsed := time.Since(startTime)
	if err != nil {
		fmt.Printf("[Fireworks API][%s] ERROR sending HTTP request (took %v): %v\n", requestID, elapsed, err)
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("[Fireworks API][%s] Response received: status=%d, took=%v, content-type=%s\n",
		requestID, resp.StatusCode, elapsed, resp.Header.Get("Content-Type"))

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("[Fireworks API][%s] ERROR response (status %d): %s\n", requestID, resp.StatusCode, string(body))
		return fmt.Errorf("fireworks API error %d: %s", resp.StatusCode, string(body))
	}

	if req.Stream {
		fmt.Printf("[Fireworks API][%s] Processing streaming response...\n", requestID)
		return parseFireworksSSE(resp.Body, callback, requestID)
	}

	fmt.Printf("[Fireworks API][%s] Processing non-streaming response...\n", requestID)
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("[Fireworks API][%s] ERROR decoding response: %v\n", requestID, err)
		return fmt.Errorf("decode response: %w", err)
	}

	resultJSON, _ := json.Marshal(result)
	fmt.Printf("[Fireworks API][%s] Response decoded: size=%d bytes\n", requestID, len(resultJSON))

	if callback.OnChunk != nil {
		callback.OnChunk(string(resultJSON))
	}
	if callback.OnComplete != nil {
		if usage, ok := result["usage"].(map[string]interface{}); ok {
			fmt.Printf("[Fireworks API][%s] Usage: prompt=%v, completion=%v, total=%v\n",
				requestID, usage["prompt_tokens"], usage["completion_tokens"], usage["total_tokens"])
			callback.OnComplete(usage)
		}
	}
	fmt.Printf("[Fireworks API][%s] Request completed successfully\n", requestID)
	return nil
}

func parseFireworksSSE(body io.Reader, callback *FireworksStreamCallback, requestID string) error {
	fmt.Printf("[Fireworks SSE][%s] Starting to parse SSE stream...\n", requestID)
	scanner := bufio.NewScanner(body)
	var lastUsage map[string]interface{}
	chunkCount := 0
	startTime := time.Now()

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			fmt.Printf("[Fireworks SSE][%s] Received [DONE] marker after %d chunks\n", requestID, chunkCount)
			break
		}

		chunkCount++
		// Parse chunk to extract usage if present
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err == nil {
			if usage, ok := chunk["usage"].(map[string]interface{}); ok && usage != nil {
				lastUsage = usage
				fmt.Printf("[Fireworks SSE][%s] Chunk #%d with usage: prompt=%v, completion=%v\n",
					requestID, chunkCount, usage["prompt_tokens"], usage["completion_tokens"])
			}
		}

		if callback.OnChunk != nil {
			if err := callback.OnChunk(data); err != nil {
				fmt.Printf("[Fireworks SSE][%s] ERROR in OnChunk callback (chunk #%d): %v\n", requestID, chunkCount, err)
				return err
			}
		}
	}

	elapsed := time.Since(startTime)
	fmt.Printf("[Fireworks SSE][%s] Stream completed: chunks=%d, took=%v\n", requestID, chunkCount, elapsed)

	if err := scanner.Err(); err != nil {
		fmt.Printf("[Fireworks SSE][%s] ERROR scanning stream: %v\n", requestID, err)
		if callback.OnError != nil {
			callback.OnError(err)
		}
		return err
	}

	// Call OnComplete with final usage stats
	if callback.OnComplete != nil && lastUsage != nil {
		fmt.Printf("[Fireworks SSE][%s] Final usage: prompt=%v, completion=%v, total=%v\n",
			requestID, lastUsage["prompt_tokens"], lastUsage["completion_tokens"], lastUsage["total_tokens"])
		callback.OnComplete(lastUsage)
	}

	return nil
}
