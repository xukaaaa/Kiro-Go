package proxy

import (
	"testing"
)

func TestParseModelAndThinkingWithCustomMapping(t *testing.T) {
	tests := []struct {
		name            string
		model           string
		thinkingSuffix  string
		expectedModel   string
		expectedThinking bool
	}{
		{
			name:            "claude-opus-4-6 maps to claude-sonnet-4.5-thinking",
			model:           "claude-opus-4-6",
			thinkingSuffix:  "-thinking",
			expectedModel:   "claude-sonnet-4.5-thinking",
			expectedThinking: false,
		},
		{
			name:            "claude-sonnet-4-6 maps to claude-sonnet-4.5",
			model:           "claude-sonnet-4-6",
			thinkingSuffix:  "-thinking",
			expectedModel:   "claude-sonnet-4.5",
			expectedThinking: false,
		},
		{
			name:            "claude-haiku-4-5-20251001 maps to claude-sonnet-4.5",
			model:           "claude-haiku-4-5-20251001",
			thinkingSuffix:  "-thinking",
			expectedModel:   "claude-sonnet-4.5",
			expectedThinking: false,
		},
		{
			name:            "claude-sonnet-4-6 with thinking suffix",
			model:           "claude-sonnet-4-6-thinking",
			thinkingSuffix:  "-thinking",
			expectedModel:   "claude-sonnet-4.5",
			expectedThinking: true,
		},
		{
			name:            "unmapped model falls back to hardcoded mapping",
			model:           "gpt-4",
			thinkingSuffix:  "-thinking",
			expectedModel:   "claude-sonnet-4.5",
			expectedThinking: false,
		},
		{
			name:            "case insensitive custom mapping",
			model:           "CLAUDE-OPUS-4-6",
			thinkingSuffix:  "-thinking",
			expectedModel:   "claude-sonnet-4.5-thinking",
			expectedThinking: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

func TestMapModel(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		expectedModel string
	}{
		{
			name:          "claude-opus-4-6 via MapModel",
			model:         "claude-opus-4-6",
			expectedModel: "claude-sonnet-4.5-thinking",
		},
		{
			name:          "claude-sonnet-4-6 via MapModel",
			model:         "claude-sonnet-4-6",
			expectedModel: "claude-sonnet-4.5",
		},
		{
			name:          "claude-haiku-4-5-20251001 via MapModel",
			model:         "claude-haiku-4-5-20251001",
			expectedModel: "claude-sonnet-4.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MapModel(tt.model)

			if got != tt.expectedModel {
				t.Errorf("MapModel() = %v, want %v", got, tt.expectedModel)
			}
		})
	}
}
