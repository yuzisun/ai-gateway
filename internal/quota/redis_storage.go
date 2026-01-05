// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package quota

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConfig holds Redis connection configuration.
type RedisConfig struct {
	// Addr is the Redis server address (host:port).
	Addr string
	// Password for Redis authentication.
	Password string
	// DB is the Redis database number.
	DB int
	// PoolSize is the maximum number of connections.
	PoolSize int

	// StorageConfig holds the window and bucket configuration.
	StorageConfig
}

// DefaultRedisConfig returns the default Redis configuration.
func DefaultRedisConfig() RedisConfig {
	return RedisConfig{
		Addr:          "localhost:6379",
		DB:            0,
		PoolSize:      10,
		StorageConfig: DefaultStorageConfig(),
	}
}

// RedisQuotaStorage implements QuotaStorage using Redis.
type RedisQuotaStorage struct {
	client redis.UniversalClient
	config RedisConfig

	// Lua scripts for atomic operations
	recordUsageScript *redis.Script
	getUsageScript    *redis.Script
}

// recordUsageLua atomically increments tokens and returns window totals.
// KEYS[1] = current bucket key
// ARGV[1] = input tokens to add
// ARGV[2] = output tokens to add
// ARGV[3] = TTL in seconds
// ARGV[4..N] = all bucket keys in current window (for aggregation)
const recordUsageLua = `
-- Increment current bucket
redis.call('HINCRBY', KEYS[1], 'input', ARGV[1])
redis.call('HINCRBY', KEYS[1], 'output', ARGV[2])

-- Set TTL if not already set (TTL returns -1 for no expiry, -2 for missing key)
local ttl = redis.call('TTL', KEYS[1])
if ttl == -1 or ttl == -2 then
    redis.call('EXPIRE', KEYS[1], ARGV[3])
end

-- Aggregate all buckets in window
local total_input = 0
local total_output = 0

for i = 4, #ARGV do
    local vals = redis.call('HMGET', ARGV[i], 'input', 'output')
    if vals[1] then total_input = total_input + tonumber(vals[1]) end
    if vals[2] then total_output = total_output + tonumber(vals[2]) end
end

return {total_input, total_output}
`

// getUsageLua retrieves aggregated usage across all buckets in window.
// ARGV[1..N/2] = provisioned bucket keys
// ARGV[N/2+1..N] = on-demand bucket keys
const getUsageLua = `
local prov_input, prov_output = 0, 0
local od_input, od_output = 0, 0

-- First half: provisioned keys
local half = #ARGV / 2
for i = 1, half do
    local vals = redis.call('HMGET', ARGV[i], 'input', 'output')
    if vals[1] then prov_input = prov_input + tonumber(vals[1]) end
    if vals[2] then prov_output = prov_output + tonumber(vals[2]) end
end

-- Second half: on-demand keys
for i = half + 1, #ARGV do
    local vals = redis.call('HMGET', ARGV[i], 'input', 'output')
    if vals[1] then od_input = od_input + tonumber(vals[1]) end
    if vals[2] then od_output = od_output + tonumber(vals[2]) end
end

return {prov_input, prov_output, od_input, od_output}
`

// NewRedisQuotaStorage creates a new Redis-backed quota storage.
func NewRedisQuotaStorage(cfg RedisConfig) (*RedisQuotaStorage, error) {
	if err := cfg.StorageConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid storage config: %w", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: cfg.PoolSize,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connection failed: %w", err)
	}

	return &RedisQuotaStorage{
		client:            client,
		config:            cfg,
		recordUsageScript: redis.NewScript(recordUsageLua),
		getUsageScript:    redis.NewScript(getUsageLua),
	}, nil
}

// NewRedisQuotaStorageFromClient creates a Redis quota storage from an existing client.
// This is useful for testing or when sharing a Redis connection.
func NewRedisQuotaStorageFromClient(client redis.UniversalClient, cfg StorageConfig) (*RedisQuotaStorage, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid storage config: %w", err)
	}

	return &RedisQuotaStorage{
		client: client,
		config: RedisConfig{
			StorageConfig: cfg,
		},
		recordUsageScript: redis.NewScript(recordUsageLua),
		getUsageScript:    redis.NewScript(getUsageLua),
	}, nil
}

// bucketKey generates the Redis key for a specific bucket.
// Format: quota:usage:{backend}:{model}:{capacity}:{bucket_ts}
func (r *RedisQuotaStorage) bucketKey(key QuotaKey, capacity CapacityType, bucketTS int64) string {
	return fmt.Sprintf("quota:usage:%s:%s:%s:%d", key.Backend, key.Model, capacity, bucketTS)
}

// RecordUsage atomically increments token counters and returns window totals.
func (r *RedisQuotaStorage) RecordUsage(ctx context.Context, req RecordUsageRequest) (*UsageSnapshot, error) {
	now := req.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	currentBucketTS := r.config.bucketTimestamp(now)
	currentKey := r.bucketKey(req.Key, req.CapacityType, currentBucketTS)

	// Get all bucket keys for the current window
	bucketTimestamps := r.config.windowBucketTimestamps(now)
	allKeys := make([]interface{}, len(bucketTimestamps))
	for i, ts := range bucketTimestamps {
		allKeys[i] = r.bucketKey(req.Key, req.CapacityType, ts)
	}

	// TTL is window duration + 1 bucket for safety
	ttlSeconds := int(r.config.WindowDuration.Seconds()) + int(r.config.BucketDuration.Seconds())

	// Build script arguments: inputTokens, outputTokens, TTL, then all bucket keys
	args := make([]interface{}, 0, 3+len(allKeys))
	args = append(args, req.InputTokens, req.OutputTokens, ttlSeconds)
	args = append(args, allKeys...)

	// Execute the Lua script
	result, err := r.recordUsageScript.Run(ctx, r.client, []string{currentKey}, args...).Slice()
	if err != nil {
		return nil, fmt.Errorf("failed to record usage: %w", err)
	}

	if len(result) != 2 {
		return nil, fmt.Errorf("unexpected result length: %d", len(result))
	}

	totalInput, err := toUint64(result[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse total input: %w", err)
	}
	totalOutput, err := toUint64(result[1])
	if err != nil {
		return nil, fmt.Errorf("failed to parse total output: %w", err)
	}

	snapshot := &UsageSnapshot{
		Key:         req.Key,
		WindowStart: time.Unix(bucketTimestamps[0], 0),
		WindowEnd:   now,
	}

	// Set the appropriate fields based on capacity type
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
func (r *RedisQuotaStorage) GetUsage(ctx context.Context, key QuotaKey) (*UsageSnapshot, error) {
	now := time.Now()
	bucketTimestamps := r.config.windowBucketTimestamps(now)

	// Build keys for both capacity types
	provisionedKeys := make([]interface{}, len(bucketTimestamps))
	onDemandKeys := make([]interface{}, len(bucketTimestamps))

	for i, ts := range bucketTimestamps {
		provisionedKeys[i] = r.bucketKey(key, CapacityProvisioned, ts)
		onDemandKeys[i] = r.bucketKey(key, CapacityOnDemand, ts)
	}

	// Combine all keys for the Lua script
	allKeys := append(provisionedKeys, onDemandKeys...)

	// Execute the Lua script
	result, err := r.getUsageScript.Run(ctx, r.client, nil, allKeys...).Slice()
	if err != nil {
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	if len(result) != 4 {
		return nil, fmt.Errorf("unexpected result length: %d", len(result))
	}

	provInput, err := toUint64(result[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse provisioned input: %w", err)
	}
	provOutput, err := toUint64(result[1])
	if err != nil {
		return nil, fmt.Errorf("failed to parse provisioned output: %w", err)
	}
	odInput, err := toUint64(result[2])
	if err != nil {
		return nil, fmt.Errorf("failed to parse on-demand input: %w", err)
	}
	odOutput, err := toUint64(result[3])
	if err != nil {
		return nil, fmt.Errorf("failed to parse on-demand output: %w", err)
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

// Close releases Redis resources.
func (r *RedisQuotaStorage) Close() error {
	return r.client.Close()
}

// toUint64 converts a Redis result to uint64.
func toUint64(v interface{}) (uint64, error) {
	switch val := v.(type) {
	case int64:
		if val < 0 {
			return 0, nil
		}
		return uint64(val), nil
	case string:
		return strconv.ParseUint(val, 10, 64)
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("unexpected type %T", v)
	}
}
