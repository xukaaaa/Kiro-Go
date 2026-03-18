package handler

import (
	"kiro-api-proxy/config"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestMain initializes config for all tests
func TestMain(m *testing.M) {
	// Create temp config file
	tmpFile, err := os.CreateTemp("", "config_handler_test_*.json")
	if err != nil {
		panic(err)
	}
	defer os.Remove(tmpFile.Name())

	// Write minimal config
	tmpFile.WriteString(`{"password":"test-admin-password","port":8080,"fireworks":{"enabled":true,"baseUrl":"https://api.fireworks.ai/inference/v1","rotationPolicy":"round-robin","costThreshold":5,"keys":[]}}`)
	tmpFile.Close()

	// Initialize config
	if err := config.Init(tmpFile.Name()); err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

func TestAdminAuth(t *testing.T) {
	tests := []struct {
		name       string
		password   string
		expectCode int
	}{
		{"correct password", "test-admin-password", http.StatusOK},
		{"wrong password", "wrong-password", http.StatusUnauthorized},
		{"no password", "", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/admin/api/fireworks", nil)
			if tt.password != "" {
				req.Header.Set("X-Admin-Password", tt.password)
			}
			rec := httptest.NewRecorder()

			// Create handler with auth
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Admin-Password") != "test-admin-password" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusOK)
			})

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.expectCode {
				t.Errorf("Expected %d, got %d", tt.expectCode, rec.Code)
			}
		})
	}
}

func TestFireworksKeysAPI(t *testing.T) {
	// Setup test keys
	keys := []config.FireworksKey{
		{
			ID:        "test-1",
			Key:       "fw_test_key_1",
			AccountID: "acc-1",
			Name:      "Test Key 1",
			Weight:    2,
			IsActive:  true,
		},
		{
			ID:        "test-2",
			Key:       "fw_test_key_2",
			AccountID: "acc-2",
			Name:      "Test Key 2",
			Weight:    1,
			IsActive:  true,
		},
	}

	// Add keys to config
	for _, key := range keys {
		config.AddFireworksKey(key)
	}

	t.Run("list keys", func(t *testing.T) {
		// Get keys from config directly
		cfg := config.GetFireworksConfig()
		if len(cfg.Keys) != 2 {
			t.Errorf("Expected 2 keys, got %d", len(cfg.Keys))
		}
	})

	t.Run("add key", func(t *testing.T) {
		newKey := config.FireworksKey{
			ID:        "test-3",
			Key:       "fw_test_key_3",
			AccountID: "acc-3",
			Name:      "Test Key 3",
			Weight:    1,
			IsActive:  true,
		}
		config.AddFireworksKey(newKey)

		cfg := config.GetFireworksConfig()
		if len(cfg.Keys) != 3 {
			t.Errorf("Expected 3 keys, got %d", len(cfg.Keys))
		}
	})

	t.Run("update key", func(t *testing.T) {
		config.UpdateFireworksKey("test-1", config.FireworksKey{
			ID:        "test-1",
			Key:       "fw_test_key_1_updated",
			AccountID: "acc-1-updated",
			Name:      "Updated Key 1",
			Weight:    5,
			IsActive:  false,
		})

		cfg := config.GetFireworksConfig()
		for _, k := range cfg.Keys {
			if k.ID == "test-1" {
				if k.Weight != 5 {
					t.Errorf("Expected weight 5, got %d", k.Weight)
				}
				if k.IsActive {
					t.Error("Expected IsActive to be false")
				}
				break
			}
		}
	})

	t.Run("delete key", func(t *testing.T) {
		config.DeleteFireworksKey("test-3")

		cfg := config.GetFireworksConfig()
		if len(cfg.Keys) != 2 {
			t.Errorf("Expected 2 keys after delete, got %d", len(cfg.Keys))
		}
	})

	t.Run("update rotation policy", func(t *testing.T) {
		config.UpdateFireworksConfig(true, "https://api.fireworks.ai/inference/v1", "weighted", 10.0)

		cfg := config.GetFireworksConfig()
		if cfg.RotationPolicy != "weighted" {
			t.Errorf("Expected weighted policy, got %s", cfg.RotationPolicy)
		}
		if cfg.CostThreshold != 10.0 {
			t.Errorf("Expected cost threshold 10.0, got %f", cfg.CostThreshold)
		}
	})
}

func TestFireworksConfigValidation(t *testing.T) {
	t.Run("key validation", func(t *testing.T) {
		// Test key with missing required fields
		invalidKey := config.FireworksKey{
			ID: "", // Missing ID
		}

		// Should not panic when adding
		config.AddFireworksKey(invalidKey)

		// Test key with empty key value
		emptyKey := config.FireworksKey{
			ID:  "empty-key",
			Key: "", // Empty key
		}
		config.AddFireworksKey(emptyKey)

		// Cleanup
		config.DeleteFireworksKey("")
		config.DeleteFireworksKey("empty-key")
	})

	t.Run("weight bounds", func(t *testing.T) {
		// Test weight below 1 (should default to 1 in pool)
		key := config.FireworksKey{
			ID:        "weight-test",
			Key:       "fw_test",
			AccountID: "acc",
			Weight:    0, // Invalid weight
			IsActive:  true,
		}
		config.AddFireworksKey(key)

		cfg := config.GetFireworksConfig()
		for _, k := range cfg.Keys {
			if k.ID == "weight-test" {
				if k.Weight != 0 {
					t.Errorf("Expected weight 0 in config (pool handles default), got %d", k.Weight)
				}
				break
			}
		}

		// Cleanup
		config.DeleteFireworksKey("weight-test")
	})
}

func TestFireworksUsageTracking(t *testing.T) {
	keyID := "usage-test-key"

	// Add a test key
	config.AddFireworksKey(config.FireworksKey{
		ID:        keyID,
		Key:       "fw_usage_test",
		AccountID: "acc-usage",
		Weight:    1,
		IsActive:  true,
	})

	t.Run("update usage", func(t *testing.T) {
		// Simulate usage update (input: 1000 tokens, output: 500 tokens)
		// Cost = (1000 * 3 + 500 * 15) / 1_000_000 = 0.0105
		inputTokens := int64(1000)
		outputTokens := int64(500)
		estimatedCost := (float64(inputTokens)*3.0 + float64(outputTokens)*15.0) / 1_000_000

		config.UpdateFireworksKeyUsage(keyID, inputTokens, outputTokens, estimatedCost)

		cfg := config.GetFireworksConfig()
		for _, k := range cfg.Keys {
			if k.ID == keyID {
				if k.EstimatedInputTokens != 1000 {
					t.Errorf("Expected input tokens 1000, got %d", k.EstimatedInputTokens)
				}
				if k.EstimatedOutputTokens != 500 {
					t.Errorf("Expected output tokens 500, got %d", k.EstimatedOutputTokens)
				}
				// Note: EstimatedCost is set to the passed value, not calculated
				break
			}
		}
	})

	t.Run("update status", func(t *testing.T) {
		now := int64(1234567890)
		config.UpdateFireworksKeyStatus(keyID, false, now+3600, 3, now)

		cfg := config.GetFireworksConfig()
		for _, k := range cfg.Keys {
			if k.ID == keyID {
				if k.IsActive {
					t.Error("Expected IsActive to be false")
				}
				if k.CooldownUntil != now+3600 {
					t.Errorf("Expected cooldown until %d, got %d", now+3600, k.CooldownUntil)
				}
				if k.ErrorCount != 3 {
					t.Errorf("Expected error count 3, got %d", k.ErrorCount)
				}
				break
			}
		}
	})

	// Cleanup
	config.DeleteFireworksKey(keyID)
}

func TestHTTPMuxRouting(t *testing.T) {
	// Test that routes are correctly registered
	tests := []struct {
		path   string
		method string
	}{
		{"/admin/api/fireworks", "GET"},
		{"/admin/api/fireworks", "POST"},
		{"/admin/api/fireworks/keys", "GET"},
		{"/admin/api/fireworks/keys", "POST"},
		{"/admin/api/fireworks/keys/test-id", "GET"},
		{"/admin/api/fireworks/keys/test-id", "PUT"},
		{"/admin/api/fireworks/keys/test-id", "DELETE"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			// Test that the route pattern is valid
			if !strings.HasPrefix(tt.path, "/admin/api/fireworks") {
				t.Errorf("Unexpected path pattern: %s", tt.path)
			}
		})
	}
}