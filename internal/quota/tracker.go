// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package quota

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// TrackerConfig configures the QuotaTracker.
type TrackerConfig struct {
	// Storage is the quota storage backend.
	Storage QuotaStorage
	// FailOpen determines behavior when storage is unavailable.
	// If true, requests are allowed to proceed (fail-open).
	// If false, requests are rejected (fail-closed).
	FailOpen bool
	// Logger for logging quota events.
	Logger *slog.Logger
}

// QuotaTracker provides the main API for token quota tracking.
// It is safe for concurrent use.
type QuotaTracker struct {
	storage  QuotaStorage
	configs  map[QuotaKey]*QuotaConfig
	mu       sync.RWMutex
	failOpen bool
	logger   *slog.Logger
}

// NewQuotaTracker creates a new quota tracker.
func NewQuotaTracker(cfg TrackerConfig) *QuotaTracker {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &QuotaTracker{
		storage:  cfg.Storage,
		configs:  make(map[QuotaKey]*QuotaConfig),
		failOpen: cfg.FailOpen,
		logger:   logger,
	}
}

// UpdateConfig updates quota configuration for a backend/model.
// Called by the controller when AIServiceBackend changes.
// Pass nil config to remove the configuration.
func (t *QuotaTracker) UpdateConfig(key QuotaKey, config *QuotaConfig) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if config == nil {
		delete(t.configs, key)
		t.logger.Info("removed quota config",
			"backend", key.Backend,
			"model", key.Model)
	} else {
		t.configs[key] = config
		t.logger.Info("updated quota config",
			"backend", key.Backend,
			"model", key.Model,
			"provisionedTPM", config.ProvisionedTPM,
			"onDemandTPM", config.OnDemandTPM,
			"windowDuration", config.WindowDuration)
	}
}

// GetConfig returns the quota configuration for a backend/model.
// Returns nil if no configuration exists.
func (t *QuotaTracker) GetConfig(key QuotaKey) *QuotaConfig {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.configs[key]
}

// HasConfig returns true if a quota configuration exists for the given key.
func (t *QuotaTracker) HasConfig(key QuotaKey) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.configs[key]
	return ok
}

// RecordUsage records token usage after a response completes.
// This is called from the ExtProc processor.
//
// If storage is unavailable and failOpen is true, returns nil without error.
// If storage is unavailable and failOpen is false, returns an error.
func (t *QuotaTracker) RecordUsage(ctx context.Context, req RecordUsageRequest) (*UsageSnapshot, error) {
	if t.storage == nil {
		if t.failOpen {
			return nil, nil
		}
		return nil, fmt.Errorf("quota storage not configured")
	}

	snapshot, err := t.storage.RecordUsage(ctx, req)
	if err != nil {
		if t.failOpen {
			t.logger.Warn("quota storage error, failing open",
				"error", err,
				"backend", req.Key.Backend,
				"model", req.Key.Model)
			return nil, nil // Fail open: continue without tracking
		}
		return nil, fmt.Errorf("failed to record usage: %w", err)
	}

	t.logger.Debug("recorded quota usage",
		"backend", req.Key.Backend,
		"model", req.Key.Model,
		"capacityType", req.CapacityType,
		"inputTokens", req.InputTokens,
		"outputTokens", req.OutputTokens)

	return snapshot, nil
}

// GetUsage returns current usage within the time window.
//
// If storage is unavailable and failOpen is true, returns an empty snapshot.
// If storage is unavailable and failOpen is false, returns an error.
func (t *QuotaTracker) GetUsage(ctx context.Context, key QuotaKey) (*UsageSnapshot, error) {
	if t.storage == nil {
		if t.failOpen {
			return &UsageSnapshot{Key: key}, nil
		}
		return nil, fmt.Errorf("quota storage not configured")
	}

	snapshot, err := t.storage.GetUsage(ctx, key)
	if err != nil {
		if t.failOpen {
			t.logger.Warn("quota storage error, failing open",
				"error", err,
				"backend", key.Backend,
				"model", key.Model)
			return &UsageSnapshot{Key: key}, nil // Return empty snapshot
		}
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	return snapshot, nil
}

// GetCapacityStatus returns the current capacity status including utilization.
// Returns an error if no quota configuration exists for the given key.
func (t *QuotaTracker) GetCapacityStatus(ctx context.Context, key QuotaKey) (*CapacityStatus, error) {
	t.mu.RLock()
	config, ok := t.configs[key]
	t.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no quota config for %s", key.String())
	}

	snapshot, err := t.GetUsage(ctx, key)
	if err != nil {
		return nil, err
	}

	// Prorate limits to window duration
	// TPM (tokens per minute) -> tokens per window
	windowMinutes := config.WindowDuration.Minutes()
	provisionedLimit := uint64(float64(config.ProvisionedTPM) * windowMinutes)
	onDemandLimit := uint64(float64(config.OnDemandTPM) * windowMinutes)

	return &CapacityStatus{
		Key:                  key,
		ProvisionedUsed:      snapshot.ProvisionedTotal(),
		ProvisionedLimit:     provisionedLimit,
		ProvisionedRemaining: saturatingSub(provisionedLimit, snapshot.ProvisionedTotal()),
		OnDemandUsed:         snapshot.OnDemandTotal(),
		OnDemandLimit:        onDemandLimit,
		OnDemandRemaining:    saturatingSub(onDemandLimit, snapshot.OnDemandTotal()),
	}, nil
}

// CheckQuota checks if a request can proceed given estimated token usage.
// Returns the recommended capacity type and whether the request should be allowed.
//
// The logic is:
// 1. If provisioned capacity has room, use provisioned
// 2. Else if on-demand capacity has room (or is unlimited), use on-demand
// 3. Else reject the request
//
// If no quota config exists, the request is always allowed with CapacityOnDemand.
func (t *QuotaTracker) CheckQuota(ctx context.Context, key QuotaKey, estimatedTokens uint64) (*QuotaDecision, error) {
	t.mu.RLock()
	config, ok := t.configs[key]
	t.mu.RUnlock()

	if !ok {
		// No config means no limits
		return &QuotaDecision{
			Allowed:      true,
			CapacityType: CapacityOnDemand,
			Reason:       "no quota configured",
		}, nil
	}

	status, err := t.GetCapacityStatus(ctx, key)
	if err != nil {
		if t.failOpen {
			return &QuotaDecision{
				Allowed:      true,
				CapacityType: CapacityOnDemand,
				Reason:       "storage unavailable, failing open",
			}, nil
		}
		return nil, err
	}

	// Check provisioned capacity first
	if status.ProvisionedRemaining >= estimatedTokens {
		return &QuotaDecision{
			Allowed:      true,
			CapacityType: CapacityProvisioned,
			Reason:       "within provisioned capacity",
		}, nil
	}

	// Check on-demand capacity
	// OnDemandLimit == 0 means unlimited
	if config.OnDemandTPM == 0 || status.OnDemandRemaining >= estimatedTokens {
		return &QuotaDecision{
			Allowed:      true,
			CapacityType: CapacityOnDemand,
			Reason:       "within on-demand capacity",
		}, nil
	}

	// Both capacities exceeded
	return &QuotaDecision{
		Allowed:      false,
		CapacityType: CapacityOnDemand,
		Reason: fmt.Sprintf("quota exceeded: provisioned %d/%d, on-demand %d/%d",
			status.ProvisionedUsed, status.ProvisionedLimit,
			status.OnDemandUsed, status.OnDemandLimit),
	}, nil
}

// Close releases resources.
func (t *QuotaTracker) Close() error {
	if t.storage != nil {
		return t.storage.Close()
	}
	return nil
}

// QuotaDecision represents the result of a quota check.
type QuotaDecision struct {
	// Allowed indicates whether the request should be allowed.
	Allowed bool
	// CapacityType indicates which capacity type should be used.
	CapacityType CapacityType
	// Reason provides a human-readable explanation.
	Reason string
}

// saturatingSub returns a - b, or 0 if b > a.
func saturatingSub(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}
