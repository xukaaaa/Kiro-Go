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
	reqBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := fireworksHttpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fireworks API error %d: %s", resp.StatusCode, string(body))
	}

	if req.Stream {
		return parseFireworksSSE(resp.Body, callback)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	resultJSON, _ := json.Marshal(result)
	if callback.OnChunk != nil {
		callback.OnChunk(string(resultJSON))
	}
	if callback.OnComplete != nil {
		if usage, ok := result["usage"].(map[string]interface{}); ok {
			callback.OnComplete(usage)
		}
	}
	return nil
}

func parseFireworksSSE(body io.Reader, callback *FireworksStreamCallback) error {
	scanner := bufio.NewScanner(body)
	var lastUsage map[string]interface{}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		// Parse chunk to extract usage if present
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err == nil {
			if usage, ok := chunk["usage"].(map[string]interface{}); ok && usage != nil {
				lastUsage = usage
			}
		}

		if callback.OnChunk != nil {
			if err := callback.OnChunk(data); err != nil {
				return err
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if callback.OnError != nil {
			callback.OnError(err)
		}
		return err
	}

	// Call OnComplete with final usage stats
	if callback.OnComplete != nil && lastUsage != nil {
		callback.OnComplete(lastUsage)
	}

	return nil
}
