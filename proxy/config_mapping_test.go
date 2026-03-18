package proxy

import (
	"kiro-api-proxy/config"
	"path/filepath"
	"testing"
)

// TestParseModelAndThinkingWithConfigMapping tests that config mappings
// have the highest priority and override hardcoded mappings
func TestParseModelAndThinkingWithConfigMapping(t *testing.T) {
	// Setup temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test_config.json")

	// Initialize config
	if err := config.Init(configPath); err != nil {
		t.Fatalf("Failed to initialize config: %v", err)
	}

	tests := []struct {
		name             string
		model            string
		thinkingSuffix   string
		configMapping    map[string]string // Set before test
		expectedModel    string
		expectedThinking bool
	}{
		{
			name:             "Config mapping overrides hardcoded mapping",
			model:            "claude-opus-4-6",
			thinkingSuffix:   "-thinking",
			configMapping:    map[string]string{"claude-opus-4-6": "claude-opus-4.5"},
			expectedModel:    "claude-opus-4.5",
			expectedThinking: false,
		},
		{
			name:             "Config mapping for unmapped model",
			model:            "custom-model-x",
			thinkingSuffix:   "-thinking",
			configMapping:    map[string]string{"custom-model-x": "claude-sonnet-4.5"},
			expectedModel:    "claude-sonnet-4.5",
			expectedThinking: false,
		},
		{
			name:             "Config cross-provider mapping to Fireworks",
			model:            "claude-opus",
			thinkingSuffix:   "-thinking",
			configMapping:    map[string]string{"claude-opus": "accounts/fireworks/models/llama-v3"},
			expectedModel:    "accounts/fireworks/models/llama-v3",
			expectedThinking: false,
		},
		{
			name:             "Config mapping with thinking suffix",
			model:            "my-model-thinking",
			thinkingSuffix:   "-thinking",
			configMapping:    map[string]string{"my-model": "claude-sonnet-4.5"},
			expectedModel:    "claude-sonnet-4.5",
			expectedThinking: true,
		},
		{
			name:             "Chain mapping (A -> B -> C)",
			model:            "model-a",
			thinkingSuffix:   "-thinking",
			configMapping:    map[string]string{"model-a": "model-b", "model-b": "claude-opus-4.5"},
			expectedModel:    "claude-opus-4.5",
			expectedThinking: false,
		},
		{
			name:             "No config mapping, fallback to hardcoded",
			model:            "claude-sonnet-4-6",
			thinkingSuffix:   "-thinking",
			configMapping:    nil,
			expectedModel:    "claude-sonnet-4.5", // From hardcoded mapping
			expectedThinking: false,
		},
		{
			name:             "No config mapping, fallback to ordered mapping",
			model:            "gpt-4o",
			thinkingSuffix:   "-thinking",
			configMapping:    nil,
			expectedModel:    "claude-sonnet-4.5", // From ordered mapping
			expectedThinking: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear any existing mappings first
			for k := range config.GetModelMappings() {
				config.DeleteModelMapping(k)
			}

			// Set up config mappings for this test
			if tt.configMapping != nil {
				for k, v := range tt.configMapping {
					if err := config.SetModelMapping(k, v); err != nil {
						t.Fatalf("Failed to set config mapping: %v", err)
					}
				}
			}

			gotModel, gotThinking := ParseModelAndThinking(tt.model, tt.thinkingSuffix)

			if gotModel != tt.expectedModel {
				t.Errorf("ParseModelAndThinking() model = %v, want %v", gotModel, tt.expectedModel)
			}

			if gotThinking != tt.expectedThinking {
				t.Errorf("ParseModelAndThinking() thinking = %v, want %v", gotThinking, tt.expectedThinking)
			}
		})
	}
}

// TestChainMappingDepthLimit tests that chain mappings don't cause infinite loops
func TestChainMappingDepthLimit(t *testing.T) {
	// Setup temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test_config.json")

	// Initialize config
	if err := config.Init(configPath); err != nil {
		t.Fatalf("Failed to initialize config: %v", err)
	}

	// Clear existing mappings
	for k := range config.GetModelMappings() {
		config.DeleteModelMapping(k)
	}

	// Create a chain mapping (A -> B -> C -> D -> E -> F -> G -> H -> I -> J -> K)
	// This should hit the depth limit and return the last model before limit
	chain := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}
	for i := 0; i < len(chain)-1; i++ {
		config.SetModelMapping(chain[i], chain[i+1])
	}

	// This should not hang and should return the last mapped model or the model at depth limit
	gotModel, _ := ParseModelAndThinking("a", "-thinking")

	// The model should be somewhere in the chain (due to depth limit)
	// or the default fallback if no match
	t.Logf("Chain mapping result: %s", gotModel)

	// Important: the test should complete without hanging (verifying no infinite loop)
	// The exact result depends on depth limit implementation
	if gotModel == "" {
		t.Error("Model should not be empty")
	}
}