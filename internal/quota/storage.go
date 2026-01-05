// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package quota

import (
	"context"
	"fmt"
	"time"
)

// QuotaStorage defines the interface for distributed quota storage.
// Implementations must be safe for concurrent use.
type QuotaStorage interface {
	// RecordUsage atomically increments token counters for the current time bucket.
	// Returns the updated totals within the window after the increment.
	RecordUsage(ctx context.Context, req RecordUsageRequest) (*UsageSnapshot, error)

	// GetUsage retrieves current token usage within the sliding window.
	GetUsage(ctx context.Context, key QuotaKey) (*UsageSnapshot, error)

	// Close releases storage resources.
	Close() error
}

// StorageConfig holds common configuration for quota storage implementations.
type StorageConfig struct {
	// WindowDuration is the quota tracking window (default: 30s).
	WindowDuration time.Duration
	// BucketDuration is the granularity of time buckets (default: 1s).
	BucketDuration time.Duration
}

// DefaultStorageConfig returns the default storage configuration.
func DefaultStorageConfig() StorageConfig {
	return StorageConfig{
		WindowDuration: 30 * time.Second,
		BucketDuration: 1 * time.Second,
	}
}

// Validate validates the storage configuration.
func (c *StorageConfig) Validate() error {
	if c.WindowDuration <= 0 {
		return fmt.Errorf("window duration must be positive, got %v", c.WindowDuration)
	}
	if c.BucketDuration <= 0 {
		return fmt.Errorf("bucket duration must be positive, got %v", c.BucketDuration)
	}
	if c.WindowDuration < c.BucketDuration {
		return fmt.Errorf("window duration (%v) must be >= bucket duration (%v)",
			c.WindowDuration, c.BucketDuration)
	}
	if c.WindowDuration%c.BucketDuration != 0 {
		return fmt.Errorf("window duration (%v) must be divisible by bucket duration (%v)",
			c.WindowDuration, c.BucketDuration)
	}
	return nil
}

// NumBuckets returns the number of buckets in the window.
func (c *StorageConfig) NumBuckets() int {
	return int(c.WindowDuration / c.BucketDuration)
}

// bucketTimestamp returns the bucket timestamp for a given time.
// Buckets are aligned to bucket duration boundaries.
func (c *StorageConfig) bucketTimestamp(t time.Time) int64 {
	return t.Unix() / int64(c.BucketDuration.Seconds()) * int64(c.BucketDuration.Seconds())
}

// windowBucketTimestamps returns all bucket timestamps in the current window.
func (c *StorageConfig) windowBucketTimestamps(now time.Time) []int64 {
	currentBucket := c.bucketTimestamp(now)
	numBuckets := c.NumBuckets()
	bucketSeconds := int64(c.BucketDuration.Seconds())

	timestamps := make([]int64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		timestamps[i] = currentBucket - int64(numBuckets-1-i)*bucketSeconds
	}
	return timestamps
}
