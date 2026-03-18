// Package pool Fireworks key pool management
// Implements key rotation, cooldown, error tracking, and usage monitoring
package pool

import (
	"kiro-api-proxy/config"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Fireworks model pricing (price per 1M tokens)
// Only supports 3 models: kimi-k2p5, minimax-m2p5, glm-5
var modelPricing = map[string]struct {
	inputPrice       float64
	cachedInputPrice float64
	outputPrice      float64
}{
	"kimi-k2p5":     {inputPrice: 0.60, cachedInputPrice: 0.10, outputPrice: 3.00},
	"minimax-m2p5":  {inputPrice: 0.30, cachedInputPrice: 0.03, outputPrice: 1.20},
	"glm-5":         {inputPrice: 1.00, cachedInputPrice: 0.20, outputPrice: 3.20},
}

// getPricing returns pricing for a given model, returns Kimi K2.5 as default
func getPricing(model string) (inputPrice, cachedInputPrice, outputPrice float64) {
	lower := strings.ToLower(model)

	// Try exact match first
	if p, ok := modelPricing[lower]; ok {
		return p.inputPrice, p.cachedInputPrice, p.outputPrice
	}

	// Try matching by model family
	for name, p := range modelPricing {
		if strings.Contains(lower, name) || strings.Contains(name, lower) {
			return p.inputPrice, p.cachedInputPrice, p.outputPrice
		}
	}

	// Default to Kimi K2.5
	return 0.60, 0.10, 3.00
}

// KeyStatus represents the current status of a Fireworks key
type KeyStatus int

const (
	StatusActive KeyStatus = iota
	StatusCooldown
	StatusCostLimited
	StatusDisabled
)

// String returns the string representation of the status
func (s KeyStatus) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusCooldown:
		return "cooldown"
	case StatusCostLimited:
		return "cost_limited"
	case StatusDisabled:
		return "disabled"
	default:
		return "unknown"
	}
}

// FireworksKeyPool manages multiple Fireworks API keys with rotation and health tracking
type FireworksKeyPool struct {
	mu            sync.RWMutex
	keys          []config.FireworksKey
	weightedKeys  []string // For weighted: repeated key IDs
	currentIndex  uint64   // Atomic counter for round-robin
	rotationPolicy string   // "round-robin" or "weighted"
	costThreshold float64  // Auto-stop threshold in USD
}

var (
	fwPool     *FireworksKeyPool
	fwPoolOnce sync.Once
)

// GetFireworksKeyPool returns the global Fireworks key pool singleton
func GetFireworksKeyPool() *FireworksKeyPool {
	fwPoolOnce.Do(func() {
		fwPool = &FireworksKeyPool{
			rotationPolicy: "round-robin",
			costThreshold:  5.0,
		}
		fwPool.Reload()
	})
	return fwPool
}

// Reload reloads keys from configuration and rebuilds weighted list
func (p *FireworksKeyPool) Reload() {
	p.mu.Lock()
	defer p.mu.Unlock()

	cfg := config.GetFireworksConfig()
	p.rotationPolicy = cfg.RotationPolicy
	if p.rotationPolicy == "" {
		p.rotationPolicy = "round-robin"
	}
	p.costThreshold = cfg.CostThreshold
	if p.costThreshold == 0 {
		p.costThreshold = 5.0
	}

	p.keys = make([]config.FireworksKey, len(cfg.Keys))
	copy(p.keys, cfg.Keys)
	p.rebuildWeightedList()
}

// rebuildWeightedList builds the weighted key list for weighted rotation
func (p *FireworksKeyPool) rebuildWeightedList() {
	p.weightedKeys = nil
	for _, key := range p.keys {
		if !key.IsActive {
			continue
		}
		weight := key.Weight
		if weight < 1 {
			weight = 1
		}
		if weight > 10 {
			weight = 10 // Cap at 10 to prevent excessive repetition
		}
		for i := 0; i < weight; i++ {
			p.weightedKeys = append(p.weightedKeys, key.ID)
		}
	}
}

// GetRotationPolicy returns the current rotation policy
func (p *FireworksKeyPool) GetRotationPolicy() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.rotationPolicy
}

// SetRotationPolicy updates the rotation policy
func (p *FireworksKeyPool) SetRotationPolicy(policy string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rotationPolicy = policy
	p.rebuildWeightedList()
}

// GetCostThreshold returns the auto-stop cost threshold
func (p *FireworksKeyPool) GetCostThreshold() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.costThreshold
}

// SetCostThreshold updates the auto-stop cost threshold
func (p *FireworksKeyPool) SetCostThreshold(threshold float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.costThreshold = threshold
}

// GetKeyStatus returns the current status of a key
func (p *FireworksKeyPool) GetKeyStatus(key config.FireworksKey) KeyStatus {
	if !key.IsActive {
		return StatusDisabled
	}

	// Check cost threshold
	if key.EstimatedCost >= p.costThreshold {
		return StatusCostLimited
	}

	// Check cooldown
	if key.CooldownUntil > 0 && time.Now().Unix() < key.CooldownUntil {
		return StatusCooldown
	}

	return StatusActive
}

// GetNext returns the next available key based on rotation policy
func (p *FireworksKeyPool) GetNext() *config.FireworksKey {
	p.mu.RLock()
	defer p.mu.RUnlock()

	now := time.Now().Unix()
	seen := make(map[string]bool)

	switch p.rotationPolicy {
	case "weighted":
		return p.getNextWeighted(now, seen)
	default: // "round-robin"
		return p.getNextRoundRobin(now, seen)
	}
}

// getNextRoundRobin implements round-robin rotation
func (p *FireworksKeyPool) getNextRoundRobin(now int64, seen map[string]bool) *config.FireworksKey {
	n := len(p.keys)
	if n == 0 {
		return nil
	}

	// Track available keys
	var cooldownKey *config.FireworksKey
	var earliestCooldown int64 = 0

	for i := 0; i < n; i++ {
		idx := int(atomic.AddUint64(&p.currentIndex, 1) % uint64(n))
		key := &p.keys[idx]

		if seen[key.ID] {
			continue
		}
		seen[key.ID] = true

		status := p.checkKeyStatus(key, now)
		if status == StatusActive {
			return key
		}
		// Track cooldown key (not cost_limited or disabled)
		if status == StatusCooldown && (earliestCooldown == 0 || key.CooldownUntil < earliestCooldown) {
			earliestCooldown = key.CooldownUntil
			cooldownKey = key
		}
	}

	// No active keys - only fallback to cooldown keys, NOT cost_limited
	if cooldownKey != nil {
		return cooldownKey
	}

	// No available keys at all - return nil to trigger error
	return nil
}

// getNextWeighted implements weighted rotation
func (p *FireworksKeyPool) getNextWeighted(now int64, seen map[string]bool) *config.FireworksKey {
	n := len(p.weightedKeys)
	if n == 0 {
		// Fallback to round-robin on all keys
		return p.getNextRoundRobin(now, seen)
	}

	keyMap := make(map[string]*config.FireworksKey)
	for i := range p.keys {
		keyMap[p.keys[i].ID] = &p.keys[i]
	}

	// Track cooldown key
	var cooldownKey *config.FireworksKey
	var earliestCooldown int64 = 0

	for i := 0; i < n; i++ {
		idx := int(atomic.AddUint64(&p.currentIndex, 1) % uint64(n))
		keyID := p.weightedKeys[idx]

		if seen[keyID] {
			continue
		}

		key, exists := keyMap[keyID]
		if !exists {
			continue
		}
		seen[keyID] = true

		status := p.checkKeyStatus(key, now)
		if status == StatusActive {
			return key
		}
		// Track cooldown key (not cost_limited or disabled)
		if status == StatusCooldown && (earliestCooldown == 0 || key.CooldownUntil < earliestCooldown) {
			earliestCooldown = key.CooldownUntil
			cooldownKey = key
		}
	}

	// No active keys - only fallback to cooldown keys, NOT cost_limited
	if cooldownKey != nil {
		return cooldownKey
	}

	// No available keys at all - return nil to trigger error
	return nil
}

// checkKeyStatus checks if a key is available for use
func (p *FireworksKeyPool) checkKeyStatus(key *config.FireworksKey, now int64) KeyStatus {
	if !key.IsActive {
		return StatusDisabled
	}

	// Check cost threshold
	if key.EstimatedCost >= p.costThreshold {
		return StatusCostLimited
	}

	// Check cooldown
	if key.CooldownUntil > now {
		return StatusCooldown
	}

	return StatusActive
}

// getLeastCooldownKey returns the key with shortest remaining cooldown
func (p *FireworksKeyPool) getLeastCooldownKey(now int64) *config.FireworksKey {
	var bestKey *config.FireworksKey
	var earliestCooldown int64 = 0

	for i := range p.keys {
		key := &p.keys[i]
		if key.CooldownUntil > now {
			if earliestCooldown == 0 || key.CooldownUntil < earliestCooldown {
				earliestCooldown = key.CooldownUntil
				bestKey = key
			}
		}
	}

	return bestKey
}

// RecordError records an error for a key and sets cooldown
func (p *FireworksKeyPool) RecordError(keyID string, statusCode int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now().Unix()
	var cooldownDuration int64

	// Determine cooldown duration based on error type
	switch statusCode {
	case 401: // Unauthorized - API key invalid, long cooldown
		cooldownDuration = 3600 // 1 hour
	case 429: // Rate limited - short cooldown
		cooldownDuration = 900  // 15 minutes
	default: // Other errors (5xx, etc.)
		cooldownDuration = 300  // 5 minutes
	}

	// Find and update the key
	for i := range p.keys {
		if p.keys[i].ID == keyID {
			p.keys[i].ErrorCount++
			p.keys[i].CooldownUntil = now + cooldownDuration

			// Also update in config
			config.UpdateFireworksKeyStatus(keyID, p.keys[i].IsActive, p.keys[i].CooldownUntil, p.keys[i].ErrorCount, p.keys[i].LastUsed)
			break
		}
	}
}

// RecordSuccess marks a key as successfully used
func (p *FireworksKeyPool) RecordSuccess(keyID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now().Unix()

	for i := range p.keys {
		if p.keys[i].ID == keyID {
			p.keys[i].ErrorCount = 0
			p.keys[i].LastUsed = now
			// Don't clear cooldown - it will expire naturally
			break
		}
	}
}

// UpdateEstimatedUsage updates the estimated usage for a key after a request
// Uses hardcoded pricing based on model
func (p *FireworksKeyPool) UpdateEstimatedUsage(keyID string, inputTokens, outputTokens, cacheReadTokens int64, model string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Get hardcoded pricing
	inputPrice, cachedInputPrice, outputPrice := getPricing(model)

	// Calculate cost in USD: (input * inputPrice + cache * cachedPrice + output * outputPrice) / 1M
	cost := (float64(inputTokens)*inputPrice + float64(cacheReadTokens)*cachedInputPrice + float64(outputTokens)*outputPrice) / 1_000_000

	for i := range p.keys {
		if p.keys[i].ID == keyID {
			p.keys[i].EstimatedInputTokens += inputTokens
			p.keys[i].EstimatedCacheReadTokens += cacheReadTokens
			p.keys[i].EstimatedOutputTokens += outputTokens
			p.keys[i].EstimatedCost += cost
			p.keys[i].LastUsed = time.Now().Unix()

			// Update in config
			config.UpdateFireworksKeyUsage(keyID, p.keys[i].EstimatedInputTokens, p.keys[i].EstimatedCacheReadTokens, p.keys[i].EstimatedOutputTokens, p.keys[i].EstimatedCost)
			break
		}
	}
}

// UpdateActualUsage updates the actual usage from billing API sync
func (p *FireworksKeyPool) UpdateActualUsage(keyID string, actualCost float64, timestamp int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := range p.keys {
		if p.keys[i].ID == keyID {
			p.keys[i].ActualUsageCost = actualCost
			p.keys[i].LastBillingCheck = timestamp
			break
		}
	}
}

// GetAllKeys returns all keys with their current status
func (p *FireworksKeyPool) GetAllKeys() []KeyWithStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]KeyWithStatus, len(p.keys))
	for i, key := range p.keys {
		result[i] = KeyWithStatus{
			Key:    key,
			Status: p.GetKeyStatus(key),
		}
	}
	return result
}

// KeyWithStatus combines a key with its current status
type KeyWithStatus struct {
	Key    config.FireworksKey
	Status KeyStatus
}

// GetStats returns pool statistics
func (p *FireworksKeyPool) GetStats() PoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := PoolStats{
		TotalKeys:    len(p.keys),
		ActiveKeys:   0,
		CooldownKeys: 0,
		LimitedKeys:  0,
		DisabledKeys: 0,
	}

	now := time.Now().Unix()
	for _, key := range p.keys {
		switch p.checkKeyStatus(&key, now) {
		case StatusActive:
			stats.ActiveKeys++
			stats.TotalEstimatedCost += key.EstimatedCost
			stats.TotalActualCost += key.ActualUsageCost
		case StatusCooldown:
			stats.CooldownKeys++
		case StatusCostLimited:
			stats.LimitedKeys++
			stats.TotalEstimatedCost += key.EstimatedCost
			stats.TotalActualCost += key.ActualUsageCost
		case StatusDisabled:
			stats.DisabledKeys++
		}
	}

	return stats
}

// PoolStats contains aggregated statistics for the key pool
type PoolStats struct {
	TotalKeys        int
	ActiveKeys       int
	CooldownKeys     int
	LimitedKeys      int
	DisabledKeys     int
	TotalEstimatedCost float64
	TotalActualCost  float64
}
