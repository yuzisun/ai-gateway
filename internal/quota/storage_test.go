// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package quota

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorageConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  StorageConfig
		wantErr bool
	}{
		{
			name:    "valid default config",
			config:  DefaultStorageConfig(),
			wantErr: false,
		},
		{
			name: "valid custom config",
			config: StorageConfig{
				WindowDuration: 60 * time.Second,
				BucketDuration: 5 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "zero window duration",
			config: StorageConfig{
				WindowDuration: 0,
				BucketDuration: 1 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "zero bucket duration",
			config: StorageConfig{
				WindowDuration: 30 * time.Second,
				BucketDuration: 0,
			},
			wantErr: true,
		},
		{
			name: "window smaller than bucket",
			config: StorageConfig{
				WindowDuration: 1 * time.Second,
				BucketDuration: 5 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "window not divisible by bucket",
			config: StorageConfig{
				WindowDuration: 30 * time.Second,
				BucketDuration: 7 * time.Second,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStorageConfig_NumBuckets(t *testing.T) {
	config := StorageConfig{
		WindowDuration: 30 * time.Second,
		BucketDuration: 1 * time.Second,
	}
	assert.Equal(t, 30, config.NumBuckets())

	config = StorageConfig{
		WindowDuration: 60 * time.Second,
		BucketDuration: 5 * time.Second,
	}
	assert.Equal(t, 12, config.NumBuckets())
}

func TestMemoryQuotaStorage_RecordAndGetUsage(t *testing.T) {
	storage, err := NewMemoryQuotaStorage(StorageConfig{
		WindowDuration: 30 * time.Second,
		BucketDuration: 1 * time.Second,
	})
	require.NoError(t, err)

	ctx := context.Background()
	key := QuotaKey{Backend: "default.openai", Model: "gpt-4"}
	now := time.Now()

	// Record provisioned usage
	snapshot, err := storage.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  100,
		OutputTokens: 50,
		CapacityType: CapacityProvisioned,
		Timestamp:    now,
	})
	require.NoError(t, err)

	assert.Equal(t, uint64(100), snapshot.ProvisionedInput)
	assert.Equal(t, uint64(50), snapshot.ProvisionedOutput)
	assert.Equal(t, uint64(0), snapshot.OnDemandInput)
	assert.Equal(t, uint64(0), snapshot.OnDemandOutput)

	// Record on-demand usage
	snapshot, err = storage.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  200,
		OutputTokens: 100,
		CapacityType: CapacityOnDemand,
		Timestamp:    now,
	})
	require.NoError(t, err)

	assert.Equal(t, uint64(200), snapshot.OnDemandInput)
	assert.Equal(t, uint64(100), snapshot.OnDemandOutput)

	// Get combined usage
	snapshot, err = storage.GetUsage(ctx, key)
	require.NoError(t, err)

	assert.Equal(t, uint64(100), snapshot.ProvisionedInput)
	assert.Equal(t, uint64(50), snapshot.ProvisionedOutput)
	assert.Equal(t, uint64(200), snapshot.OnDemandInput)
	assert.Equal(t, uint64(100), snapshot.OnDemandOutput)
	assert.Equal(t, uint64(450), snapshot.TotalTokens())
}

func TestMemoryQuotaStorage_MultipleModels(t *testing.T) {
	storage, err := NewMemoryQuotaStorage(DefaultStorageConfig())
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now()

	key1 := QuotaKey{Backend: "default.openai", Model: "gpt-4"}
	key2 := QuotaKey{Backend: "default.openai", Model: "gpt-3.5"}

	// Record usage for model 1
	_, err = storage.RecordUsage(ctx, RecordUsageRequest{
		Key:          key1,
		InputTokens:  100,
		OutputTokens: 50,
		CapacityType: CapacityProvisioned,
		Timestamp:    now,
	})
	require.NoError(t, err)

	// Record usage for model 2
	_, err = storage.RecordUsage(ctx, RecordUsageRequest{
		Key:          key2,
		InputTokens:  200,
		OutputTokens: 100,
		CapacityType: CapacityProvisioned,
		Timestamp:    now,
	})
	require.NoError(t, err)

	// Check model 1 usage
	snapshot, err := storage.GetUsage(ctx, key1)
	require.NoError(t, err)
	assert.Equal(t, uint64(150), snapshot.ProvisionedTotal())

	// Check model 2 usage
	snapshot, err = storage.GetUsage(ctx, key2)
	require.NoError(t, err)
	assert.Equal(t, uint64(300), snapshot.ProvisionedTotal())
}

func TestMemoryQuotaStorage_Reset(t *testing.T) {
	storage, err := NewMemoryQuotaStorage(DefaultStorageConfig())
	require.NoError(t, err)

	ctx := context.Background()
	key := QuotaKey{Backend: "default.openai", Model: "gpt-4"}

	// Record usage
	_, err = storage.RecordUsage(ctx, RecordUsageRequest{
		Key:          key,
		InputTokens:  100,
		OutputTokens: 50,
		CapacityType: CapacityProvisioned,
		Timestamp:    time.Now(),
	})
	require.NoError(t, err)

	// Verify usage
	snapshot, err := storage.GetUsage(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, uint64(150), snapshot.ProvisionedTotal())

	// Reset
	storage.Reset()

	// Verify empty
	snapshot, err = storage.GetUsage(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), snapshot.TotalTokens())
}

func TestUsageSnapshot_Methods(t *testing.T) {
	snapshot := &UsageSnapshot{
		ProvisionedInput:  100,
		ProvisionedOutput: 50,
		OnDemandInput:     200,
		OnDemandOutput:    100,
	}

	assert.Equal(t, uint64(150), snapshot.ProvisionedTotal())
	assert.Equal(t, uint64(300), snapshot.OnDemandTotal())
	assert.Equal(t, uint64(450), snapshot.TotalTokens())
}

func TestCapacityStatus_Utilization(t *testing.T) {
	status := &CapacityStatus{
		ProvisionedUsed:  15000,
		ProvisionedLimit: 30000,
		OnDemandUsed:     7500,
		OnDemandLimit:    15000,
	}

	assert.Equal(t, 50.0, status.ProvisionedUtilization())
	assert.Equal(t, 50.0, status.OnDemandUtilization())

	// Test unlimited on-demand
	status.OnDemandLimit = 0
	assert.Equal(t, 0.0, status.OnDemandUtilization())

	// Test zero provisioned limit
	status.ProvisionedLimit = 0
	assert.Equal(t, 0.0, status.ProvisionedUtilization())
}

func TestQuotaKey_String(t *testing.T) {
	key := QuotaKey{Backend: "default.openai", Model: "gpt-4"}
	assert.Equal(t, "default.openai:gpt-4", key.String())
}
