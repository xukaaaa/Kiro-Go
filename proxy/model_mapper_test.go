package proxy

import "testing"

func TestMapModelWithCustomMapping(t *testing.T) {
	tests := []struct {
		name           string
		requestedModel string
		expectedModel  string
		expectedRemap  bool
	}{
		{
			name:           "claude-opus-4-6 maps to claude-sonnet-4.5-thinking",
			requestedModel: "claude-opus-4-6",
			expectedModel:  "claude-sonnet-4.5-thinking",
			expectedRemap:  true,
		},
		{
			name:           "claude-sonnet-4-6 maps to claude-sonnet-4.5",
			requestedModel: "claude-sonnet-4-6",
			expectedModel:  "claude-sonnet-4.5",
			expectedRemap:  true,
		},
		{
			name:           "claude-haiku-4-5-20251001 maps to claude-sonnet-4.5",
			requestedModel: "claude-haiku-4-5-20251001",
			expectedModel:  "claude-sonnet-4.5",
			expectedRemap:  true,
		},
		{
			name:           "unmapped model returns original (pass-through)",
			requestedModel: "gpt-4",
			expectedModel:  "gpt-4",
			expectedRemap:  false,
		},
		{
			name:           "unknown model returns original",
			requestedModel: "unknown-model",
			expectedModel:  "unknown-model",
			expectedRemap:  false,
		},
		{
			name:           "case insensitive - CLAUDE-OPUS-4-6 uppercase",
			requestedModel: "CLAUDE-OPUS-4-6",
			expectedModel:  "claude-sonnet-4.5-thinking",
			expectedRemap:  true,
		},
		{
			name:           "case insensitive - Claude-Sonnet-4-6 mixed case",
			requestedModel: "Claude-Sonnet-4-6",
			expectedModel:  "claude-sonnet-4.5",
			expectedRemap:  true,
		},
		{
			name:           "empty string returns empty",
			requestedModel: "",
			expectedModel:  "",
			expectedRemap:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotModel, gotRemap := MapModelWithCustomMapping(tt.requestedModel)

			if gotModel != tt.expectedModel {
				t.Errorf("MapModelWithCustomMapping() model = %v, want %v", gotModel, tt.expectedModel)
			}

			if gotRemap != tt.expectedRemap {
				t.Errorf("MapModelWithCustomMapping() remap = %v, want %v", gotRemap, tt.expectedRemap)
			}
		})
	}
}
