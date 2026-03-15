package proxy

import (
	"strings"
	"sync"

	"kiro-api-proxy/config"
)

var (
	cachedMappings map[string]string
	cacheMutex     sync.RWMutex
)

func rebuildCache() {
	// Double-checked locking: check if another goroutine already rebuilt
	cacheMutex.RLock()
	if cachedMappings != nil {
		cacheMutex.RUnlock()
		return
	}
	cacheMutex.RUnlock()

	// Acquire write lock to rebuild
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	// Check again in case another goroutine rebuilt while we waited for lock
	if cachedMappings != nil {
		return
	}

	// Rebuild cache
	mappings := config.GetEnabledModelMappings()
	cachedMappings = make(map[string]string)
	for _, m := range mappings {
		if m.TargetModel != "" {
			cachedMappings[strings.ToLower(m.SourceModel)] = m.TargetModel
		}
	}
}

// InvalidateMappingCache clears the cache, forcing a rebuild on next access
func InvalidateMappingCache() {
	cacheMutex.Lock()
	cachedMappings = nil
	cacheMutex.Unlock()
}

// MapModelWithCustomMapping checks if a model has a custom mapping
// Returns the mapped model and a boolean indicating if remapping occurred
func MapModelWithCustomMapping(requestedModel string) (string, bool) {
	if requestedModel == "" {
		return requestedModel, false
	}

	lowerModel := strings.ToLower(requestedModel)

	// Fast path: check cache
	cacheMutex.RLock()
	if cachedMappings != nil {
		if target, ok := cachedMappings[lowerModel]; ok {
			cacheMutex.RUnlock()
			return target, true
		}
		cacheMutex.RUnlock()
		// Cache exists but no mapping found = pass-through
		return requestedModel, false
	}
	cacheMutex.RUnlock()

	// Cache miss: rebuild
	rebuildCache()

	// Retry with cache
	cacheMutex.RLock()
	if target, ok := cachedMappings[lowerModel]; ok {
		cacheMutex.RUnlock()
		return target, true
	}
	cacheMutex.RUnlock()

	// No mapping found, return original model (pass-through)
	return requestedModel, false
}
