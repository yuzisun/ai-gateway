// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package quota

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuotaTracker_RecordUsage(t *testing.T) {
	storage, err := NewMemoryQuotaStorage(DefaultStorageConfig())
	require.NoError(t, err)

	tracker := NewQuotaTracker(TrackerConfig{
		Storage:  storage,
		FailOpen: true,
		Logger:   slog.Default(),
	})

	ctx := context.Background()
	key := QuotaKey{Backend: "default.openai", Model: "gpt-4"}

	// Record some usage
	snapshot, err := tracker.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  100,
		OutputTokens: 50,
		CapacityType: CapacityProvisioned,
		Timestamp:    time.Now(),
	})
	require.NoError(t, err)
	require.NotNil(t, snapshot)

	assert.Equal(t, uint64(100), snapshot.ProvisionedInput)
	assert.Equal(t, uint64(50), snapshot.ProvisionedOutput)
	assert.Equal(t, uint64(150), snapshot.ProvisionedTotal())

	// Record more usage
	snapshot, err = tracker.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  200,
		OutputTokens: 100,
		CapacityType: CapacityProvisioned,
		Timestamp:    time.Now(),
	})
	require.NoError(t, err)
	require.NotNil(t, snapshot)

	assert.Equal(t, uint64(300), snapshot.ProvisionedInput)
	assert.Equal(t, uint64(150), snapshot.ProvisionedOutput)
	assert.Equal(t, uint64(450), snapshot.ProvisionedTotal())
}

func TestQuotaTracker_GetUsage(t *testing.T) {
	storage, err := NewMemoryQuotaStorage(DefaultStorageConfig())
	require.NoError(t, err)

	tracker := NewQuotaTracker(TrackerConfig{
		Storage:  storage,
		FailOpen: true,
		Logger:   slog.Default(),
	})

	ctx := context.Background()
	key := QuotaKey{Backend: "default.anthropic", Model: "claude-3"}

	// Record provisioned usage
	_, err = tracker.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  100,
		OutputTokens: 50,
		CapacityType: CapacityProvisioned,
		Timestamp:    time.Now(),
	})
	require.NoError(t, err)

	// Record on-demand usage
	_, err = tracker.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  200,
		OutputTokens: 100,
		CapacityType: CapacityOnDemand,
		Timestamp:    time.Now(),
	})
	require.NoError(t, err)

	// Get combined usage
	snapshot, err := tracker.GetUsage(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, snapshot)

	assert.Equal(t, uint64(100), snapshot.ProvisionedInput)
	assert.Equal(t, uint64(50), snapshot.ProvisionedOutput)
	assert.Equal(t, uint64(200), snapshot.OnDemandInput)
	assert.Equal(t, uint64(100), snapshot.OnDemandOutput)
	assert.Equal(t, uint64(450), snapshot.TotalTokens())
}

func TestQuotaTracker_GetCapacityStatus(t *testing.T) {
	storage, err := NewMemoryQuotaStorage(StorageConfig{
		WindowDuration: 30 * time.Second,
		BucketDuration: 1 * time.Second,
	})
	require.NoError(t, err)

	tracker := NewQuotaTracker(TrackerConfig{
		Storage:  storage,
		FailOpen: true,
		Logger:   slog.Default(),
	})

	ctx := context.Background()
	key := QuotaKey{Backend: "default.openai", Model: "gpt-4"}

	// Configure quota: 60000 TPM provisioned, 30000 TPM on-demand
	// With 30s window, effective limits are 30000 and 15000 tokens
	tracker.UpdateConfig(key, &QuotaConfig{
		ProvisionedTPM: 60000,
		OnDemandTPM:    30000,
		WindowDuration: 30 * time.Second,
	})

	// Record some usage
	_, err = tracker.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  10000,
		OutputTokens: 5000,
		CapacityType: CapacityProvisioned,
		Timestamp:    time.Now(),
	})
	require.NoError(t, err)

	// Get capacity status
	status, err := tracker.GetCapacityStatus(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, status)

	assert.Equal(t, uint64(15000), status.ProvisionedUsed)
	assert.Equal(t, uint64(30000), status.ProvisionedLimit)
	assert.Equal(t, uint64(15000), status.ProvisionedRemaining)
	assert.Equal(t, float64(50), status.ProvisionedUtilization())
}

func TestQuotaTracker_CheckQuota(t *testing.T) {
	storage, err := NewMemoryQuotaStorage(StorageConfig{
		WindowDuration: 30 * time.Second,
		BucketDuration: 1 * time.Second,
	})
	require.NoError(t, err)

	tracker := NewQuotaTracker(TrackerConfig{
		Storage:  storage,
		FailOpen: true,
		Logger:   slog.Default(),
	})

	ctx := context.Background()
	key := QuotaKey{Backend: "default.openai", Model: "gpt-4"}

	// Configure quota: 60000 TPM provisioned, 30000 TPM on-demand
	// With 30s window, effective limits are 30000 and 15000 tokens
	tracker.UpdateConfig(key, &QuotaConfig{
		ProvisionedTPM: 60000,
		OnDemandTPM:    30000,
		WindowDuration: 30 * time.Second,
	})

	// Check quota when empty - should use provisioned
	decision, err := tracker.CheckQuota(ctx, key, 1000)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.Equal(t, CapacityProvisioned, decision.CapacityType)

	// Use up provisioned capacity
	_, err = tracker.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  30000,
		OutputTokens: 0,
		CapacityType: CapacityProvisioned,
		Timestamp:    time.Now(),
	})
	require.NoError(t, err)

	// Check quota - should fall back to on-demand
	decision, err = tracker.CheckQuota(ctx, key, 1000)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.Equal(t, CapacityOnDemand, decision.CapacityType)

	// Use up on-demand capacity
	_, err = tracker.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  15000,
		OutputTokens: 0,
		CapacityType: CapacityOnDemand,
		Timestamp:    time.Now(),
	})
	require.NoError(t, err)

	// Check quota - should be rejected
	decision, err = tracker.CheckQuota(ctx, key, 1000)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
}

func TestQuotaTracker_CheckQuota_NoConfig(t *testing.T) {
	storage, err := NewMemoryQuotaStorage(DefaultStorageConfig())
	require.NoError(t, err)

	tracker := NewQuotaTracker(TrackerConfig{
		Storage:  storage,
		FailOpen: true,
		Logger:   slog.Default(),
	})

	ctx := context.Background()
	key := QuotaKey{Backend: "default.unknown", Model: "unknown-model"}

	// Check quota with no config - should allow with on-demand
	decision, err := tracker.CheckQuota(ctx, key, 1000000)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.Equal(t, CapacityOnDemand, decision.CapacityType)
	assert.Contains(t, decision.Reason, "no quota configured")
}

func TestQuotaTracker_FailOpen(t *testing.T) {
	// Create a failing storage
	failingStorage := &failingQuotaStorage{}

	tracker := NewQuotaTracker(TrackerConfig{
		Storage:  failingStorage,
		FailOpen: true,
		Logger:   slog.Default(),
	})

	ctx := context.Background()
	key := QuotaKey{Backend: "default.openai", Model: "gpt-4"}

	// RecordUsage should return nil without error (fail-open)
	snapshot, err := tracker.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  100,
		OutputTokens: 50,
		CapacityType: CapacityProvisioned,
		Timestamp:    time.Now(),
	})
	require.NoError(t, err)
	assert.Nil(t, snapshot)

	// GetUsage should return empty snapshot without error (fail-open)
	snapshot, err = tracker.GetUsage(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, snapshot)
	assert.Equal(t, uint64(0), snapshot.TotalTokens())
}

func TestQuotaTracker_FailClosed(t *testing.T) {
	// Create a failing storage
	failingStorage := &failingQuotaStorage{}

	tracker := NewQuotaTracker(TrackerConfig{
		Storage:  failingStorage,
		FailOpen: false, // Fail closed
		Logger:   slog.Default(),
	})

	ctx := context.Background()
	key := QuotaKey{Backend: "default.openai", Model: "gpt-4"}

	// RecordUsage should return error (fail-closed)
	_, err := tracker.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  100,
		OutputTokens: 50,
		CapacityType: CapacityProvisioned,
		Timestamp:    time.Now(),
	})
	require.Error(t, err)

	// GetUsage should return error (fail-closed)
	_, err = tracker.GetUsage(ctx, key)
	require.Error(t, err)
}

func TestQuotaTracker_UpdateConfig(t *testing.T) {
	storage, err := NewMemoryQuotaStorage(DefaultStorageConfig())
	require.NoError(t, err)

	tracker := NewQuotaTracker(TrackerConfig{
		Storage:  storage,
		FailOpen: true,
		Logger:   slog.Default(),
	})

	key := QuotaKey{Backend: "default.openai", Model: "gpt-4"}

	// Initially no config
	assert.False(t, tracker.HasConfig(key))
	assert.Nil(t, tracker.GetConfig(key))

	// Add config
	config := &QuotaConfig{
		ProvisionedTPM: 100000,
		OnDemandTPM:    50000,
		WindowDuration: 30 * time.Second,
	}
	tracker.UpdateConfig(key, config)

	assert.True(t, tracker.HasConfig(key))
	assert.Equal(t, config, tracker.GetConfig(key))

	// Remove config
	tracker.UpdateConfig(key, nil)
	assert.False(t, tracker.HasConfig(key))
	assert.Nil(t, tracker.GetConfig(key))
}

// failingQuotaStorage is a mock storage that always fails.
type failingQuotaStorage struct{}

func (f *failingQuotaStorage) RecordUsage(_ context.Context, _ RecordUsageRequest) (*UsageSnapshot, error) {
	return nil, errors.New("storage unavailable")
}

func (f *failingQuotaStorage) GetUsage(_ context.Context, _ QuotaKey) (*UsageSnapshot, error) {
	return nil, errors.New("storage unavailable")
}

func (f *failingQuotaStorage) Close() error {
	return nil
}
