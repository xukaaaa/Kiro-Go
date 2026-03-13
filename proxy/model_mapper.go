package proxy

import "strings"

// modelMappings defines hardcoded model mappings
// Request model (key) -> Target model (value)
var modelMappings = map[string]string{
	"claude-opus-4-6":           "claude-sonnet-4.5-thinking",
	"claude-sonnet-4-6":         "claude-sonnet-4.5",
	"claude-haiku-4-5-20251001": "claude-sonnet-4.5",
}

// MapModelWithCustomMapping checks if a model has a custom mapping
// Returns the mapped model and a boolean indicating if remapping occurred
func MapModelWithCustomMapping(requestedModel string) (string, bool) {
	if requestedModel == "" {
		return requestedModel, false
	}

	// Case-insensitive lookup
	lowerModel := strings.ToLower(requestedModel)
	for key, value := range modelMappings {
		if strings.ToLower(key) == lowerModel {
			return value, true
		}
	}

	// No mapping found, return original model (pass-through)
	return requestedModel, false
}
