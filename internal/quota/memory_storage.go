// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package quota

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryQuotaStorage implements QuotaStorage using in-memory storage.
// This is intended for testing purposes only and does not support
// distributed tracking across multiple instances.
type MemoryQuotaStorage struct {
	config  StorageConfig
	buckets map[string]*memoryBucket
	mu      sync.RWMutex
}

type memoryBucket struct {
	input     uint64
	output    uint64
	timestamp int64
}

// NewMemoryQuotaStorage creates a new in-memory quota storage.
func NewMemoryQuotaStorage(cfg StorageConfig) (*MemoryQuotaStorage, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &MemoryQuotaStorage{
		config:  cfg,
		buckets: make(map[string]*memoryBucket),
	}, nil
}

// bucketKey generates the key for a specific bucket.
func (m *MemoryQuotaStorage) bucketKey(key QuotaKey, capacity CapacityType, bucketTS int64) string {
	return fmt.Sprintf("%s:%s:%s:%d", key.Backend, key.Model, capacity, bucketTS)
}

// RecordUsage atomically increments token counters and returns window totals.
func (m *MemoryQuotaStorage) RecordUsage(_ context.Context, req RecordUsageRequest) (*UsageSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := req.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	// Clean up expired buckets
	m.cleanupExpiredLocked(now)

	// Get current bucket
	currentBucketTS := m.config.bucketTimestamp(now)
	bucketKey := m.bucketKey(req.Key, req.CapacityType, currentBucketTS)

	bucket, ok := m.buckets[bucketKey]
	if !ok {
		bucket = &memoryBucket{timestamp: currentBucketTS}
		m.buckets[bucketKey] = bucket
	}

	bucket.input += req.InputTokens
	bucket.output += req.OutputTokens

	// Aggregate all buckets in window
	bucketTimestamps := m.config.windowBucketTimestamps(now)
	var totalInput, totalOutput uint64

	for _, ts := range bucketTimestamps {
		bk := m.bucketKey(req.Key, req.CapacityType, ts)
		if b, exists := m.buckets[bk]; exists {
			totalInput += b.input
			totalOutput += b.output
		}
	}

	snapshot := &UsageSnapshot{
		Key:         req.Key,
		WindowStart: time.Unix(bucketTimestamps[0], 0),
		WindowEnd:   now,
	}

	switch req.CapacityType {
	case CapacityProvisioned:
		snapshot.ProvisionedInput = totalInput
		snapshot.ProvisionedOutput = totalOutput
	case CapacityOnDemand:
		snapshot.OnDemandInput = totalInput
		snapshot.OnDemandOutput = totalOutput
	}

	return snapshot, nil
}

// GetUsage retrieves current token usage within the sliding window.
func (m *MemoryQuotaStorage) GetUsage(_ context.Context, key QuotaKey) (*UsageSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	bucketTimestamps := m.config.windowBucketTimestamps(now)

	var provInput, provOutput, odInput, odOutput uint64

	for _, ts := range bucketTimestamps {
		// Provisioned
		provKey := m.bucketKey(key, CapacityProvisioned, ts)
		if b, exists := m.buckets[provKey]; exists {
			provInput += b.input
			provOutput += b.output
		}

		// On-demand
		odKey := m.bucketKey(key, CapacityOnDemand, ts)
		if b, exists := m.buckets[odKey]; exists {
			odInput += b.input
			odOutput += b.output
		}
	}

	return &UsageSnapshot{
		Key:               key,
		WindowStart:       time.Unix(bucketTimestamps[0], 0),
		WindowEnd:         now,
		ProvisionedInput:  provInput,
		ProvisionedOutput: provOutput,
		OnDemandInput:     odInput,
		OnDemandOutput:    odOutput,
	}, nil
}

// Close is a no-op for in-memory storage.
func (m *MemoryQuotaStorage) Close() error {
	return nil
}

// cleanupExpiredLocked removes buckets older than the window duration.
// Must be called with the lock held.
func (m *MemoryQuotaStorage) cleanupExpiredLocked(now time.Time) {
	windowStart := now.Add(-m.config.WindowDuration).Unix()

	for key, bucket := range m.buckets {
		if bucket.timestamp < windowStart {
			delete(m.buckets, key)
		}
	}
}

// Reset clears all stored data. Useful for testing.
func (m *MemoryQuotaStorage) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buckets = make(map[string]*memoryBucket)
}
