package pool

import (
	"kiro-api-proxy/config"
	"os"
	"sync"
	"testing"
	"time"
)

// TestMain initializes config for all tests
func TestMain(m *testing.M) {
	// Create temp config file
	tmpFile, err := os.CreateTemp("", "config_test_*.json")
	if err != nil {
		panic(err)
	}
	defer os.Remove(tmpFile.Name())

	// Write minimal config
	tmpFile.WriteString(`{"password":"test","port":8080}`)
	tmpFile.Close()

	// Initialize config
	if err := config.Init(tmpFile.Name()); err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

func TestFireworksKeyPool_BasicOperations(t *testing.T) {
	// Reset pool for clean test
	fwPoolOnce = sync.Once{}
	fwPool = nil

	// Create test keys
	keys := []config.FireworksKey{
		{
			ID:        "test-key-1",
			Key:       "fw_test_key_1",
			AccountID: "account-1",
			Weight:    3,
			IsActive:  true,
		},
		{
			ID:        "test-key-2",
			Key:       "fw_test_key_2",
			AccountID: "account-2",
			Weight:    1,
			IsActive:  true,
		},
		{
			ID:        "test-key-3",
			Key:       "fw_test_key_3",
			AccountID: "account-3",
			Weight:    1,
			IsActive:  false, // Disabled
		},
	}

	// Set rotation policy to weighted
	config.UpdateFireworksConfig(true, "https://api.fireworks.ai/inference/v1", "weighted", 5.0)

	// Add keys to config
	for _, key := range keys {
		config.AddFireworksKey(key)
	}

	// Get pool and reload
	pool := GetFireworksKeyPool()
	pool.Reload()

	// Test GetNext - should return active keys only
	key1 := pool.GetNext()
	if key1 == nil {
		t.Fatal("Expected key, got nil")
	}
	if key1.ID != "test-key-1" && key1.ID != "test-key-2" {
		t.Errorf("Expected key-1 or key-2, got %s", key1.ID)
	}

	// Test weighted rotation - key-1 should appear more often
	counts := make(map[string]int)
	for i := 0; i < 100; i++ {
		key := pool.GetNext()
		if key != nil {
			counts[key.ID]++
		}
	}

	// Key-1 (weight 3) should appear ~3x more than key-2 (weight 1)
	if counts["test-key-1"] < counts["test-key-2"]*2 {
		t.Errorf("Expected key-1 to appear more often. Got key-1: %d, key-2: %d", counts["test-key-1"], counts["test-key-2"])
	}

	// Test cooldown
	pool.RecordError("test-key-1", 429)
	key := pool.GetNext()
	if key != nil && key.ID == "test-key-1" {
		t.Error("Key-1 should be in cooldown, should not be returned")
	}

	// Test cost limit
	pool.mu.Lock()
	for i := range pool.keys {
		if pool.keys[i].ID == "test-key-2" {
			pool.keys[i].EstimatedCost = 10.0 // Over $5 threshold
		}
	}
	pool.mu.Unlock()

	// key-1 is in cooldown, key-2 is cost-limited
	// Pool returns fallback key (key-1 with shortest cooldown) or nil
	key = pool.GetNext()
	// Acceptable: nil (no available keys) or fallback to key-1 (shortest cooldown)
	if key != nil && key.ID == "test-key-2" {
		t.Error("Key-2 should be cost-limited, should not be returned as active")
	}
}

func TestFireworksKeyPool_Status(t *testing.T) {
	pool := &FireworksKeyPool{
		costThreshold: 5.0,
	}

	tests := []struct {
		name     string
		key      config.FireworksKey
		expected KeyStatus
	}{
		{
			name: "active key",
			key: config.FireworksKey{
				IsActive:      true,
				EstimatedCost: 3.0,
				CooldownUntil: 0,
			},
			expected: StatusActive,
		},
		{
			name: "disabled key",
			key: config.FireworksKey{
				IsActive: false,
			},
			expected: StatusDisabled,
		},
		{
			name: "cost limited key",
			key: config.FireworksKey{
				IsActive:      true,
				EstimatedCost: 6.0, // Over $5 threshold
			},
			expected: StatusCostLimited,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := pool.GetKeyStatus(tt.key)
			if status != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, status)
			}
		})
	}
}

func TestFireworksKeyPool_CooldownDuration(t *testing.T) {
	pool := GetFireworksKeyPool()

	tests := []struct {
		statusCode     int
		expectedMinDur int64
		expectedMaxDur int64
	}{
		{401, 3500, 3700}, // ~1 hour (3600s)
		{429, 800, 1000},  // ~15 minutes (900s)
		{500, 200, 400},   // ~5 minutes (300s)
	}

	for _, tt := range tests {
		// Reset
		pool.mu.Lock()
		pool.keys = []config.FireworksKey{{ID: "cooldown-test", IsActive: true}}
		pool.mu.Unlock()

		now := time.Now().Unix()

		pool.RecordError("cooldown-test", tt.statusCode)

		// Check cooldown was set
		pool.mu.RLock()
		for _, key := range pool.keys {
			if key.ID == "cooldown-test" {
				duration := key.CooldownUntil - now
				if duration < tt.expectedMinDur || duration > tt.expectedMaxDur {
					t.Errorf("Status %d: expected duration ~%ds, got %ds", tt.statusCode, tt.expectedMinDur, duration)
				}
			}
		}
		pool.mu.RUnlock()
	}
}