package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelMappings(t *testing.T) {
	// Setup temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test_config.json")

	// Initialize config
	if err := Init(configPath); err != nil {
		t.Fatalf("Failed to initialize config: %v", err)
	}

	t.Run("GetModelMappings returns empty when not set", func(t *testing.T) {
		mappings := GetModelMappings()
		if mappings == nil {
			mappings = make(map[string]string)
		}
		if len(mappings) != 0 {
			t.Errorf("Expected empty mappings, got %d", len(mappings))
		}
	})

	t.Run("SetModelMapping adds new mapping", func(t *testing.T) {
		err := SetModelMapping("gpt-4", "claude-sonnet-4.5")
		if err != nil {
			t.Errorf("Failed to set mapping: %v", err)
		}

		target, ok := GetModelMapping("gpt-4")
		if !ok {
			t.Error("Mapping not found after setting")
		}
		if target != "claude-sonnet-4.5" {
			t.Errorf("Expected target 'claude-sonnet-4.5', got '%s'", target)
		}
	})

	t.Run("SetModelMapping updates existing mapping", func(t *testing.T) {
		// Update the existing mapping
		err := SetModelMapping("gpt-4", "claude-opus-4.5")
		if err != nil {
			t.Errorf("Failed to update mapping: %v", err)
		}

		target, ok := GetModelMapping("gpt-4")
		if !ok {
			t.Error("Mapping not found after update")
		}
		if target != "claude-opus-4.5" {
			t.Errorf("Expected target 'claude-opus-4.5', got '%s'", target)
		}
	})

	t.Run("DeleteModelMapping removes mapping", func(t *testing.T) {
		deleted, err := DeleteModelMapping("gpt-4")
		if err != nil {
			t.Errorf("Failed to delete mapping: %v", err)
		}
		if !deleted {
			t.Error("Expected mapping to be deleted")
		}

		_, ok := GetModelMapping("gpt-4")
		if ok {
			t.Error("Mapping still exists after deletion")
		}
	})

	t.Run("DeleteModelMapping returns false for non-existent mapping", func(t *testing.T) {
		deleted, err := DeleteModelMapping("non-existent-model")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if deleted {
			t.Error("Expected false for non-existent mapping")
		}
	})

	t.Run("GetModelMappingsList returns list format", func(t *testing.T) {
		// Add some mappings
		SetModelMapping("model-1", "target-1")
		SetModelMapping("model-2", "target-2")

		list := GetModelMappingsList()
		if len(list) != 2 {
			t.Errorf("Expected 2 mappings, got %d", len(list))
		}

		// Verify mappings are present
		found := make(map[string]bool)
		for _, entry := range list {
			found[entry.Source] = true
			if entry.Source == "model-1" && entry.Target != "target-1" {
				t.Errorf("model-1 should map to target-1, got %s", entry.Target)
			}
			if entry.Source == "model-2" && entry.Target != "target-2" {
				t.Errorf("model-2 should map to target-2, got %s", entry.Target)
			}
		}

		if !found["model-1"] || !found["model-2"] {
			t.Error("Not all mappings found in list")
		}
	})

	t.Run("Cross-provider mapping (Fireworks routing)", func(t *testing.T) {
		err := SetModelMapping("claude-opus", "accounts/fireworks/models/llama-v3")
		if err != nil {
			t.Errorf("Failed to set cross-provider mapping: %v", err)
		}

		target, ok := GetModelMapping("claude-opus")
		if !ok {
			t.Error("Cross-provider mapping not found")
		}
		if target != "accounts/fireworks/models/llama-v3" {
			t.Errorf("Expected Fireworks model path, got '%s'", target)
		}
	})
}

func TestModelMappingPersistence(t *testing.T) {
	// Setup temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "persist_config.json")

	// Initialize config
	if err := Init(configPath); err != nil {
		t.Fatalf("Failed to initialize config: %v", err)
	}

	// Set a mapping
	if err := SetModelMapping("test-model", "test-target"); err != nil {
		t.Fatalf("Failed to set mapping: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("Config file was not created")
	}

	// Reload config
	if err := Load(); err != nil {
		t.Fatalf("Failed to reload config: %v", err)
	}

	// Verify mapping persisted
	target, ok := GetModelMapping("test-model")
	if !ok {
		t.Error("Mapping not persisted after reload")
	}
	if target != "test-target" {
		t.Errorf("Expected 'test-target', got '%s'", target)
	}
}