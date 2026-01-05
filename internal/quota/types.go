// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package quota provides token quota tracking for AI model providers.
// It supports distributed tracking across multiple Envoy instances using Redis,
// with a sliding time window for usage aggregation.
package quota

import (
	"time"
)

// CapacityType distinguishes between provisioned and on-demand capacity.
type CapacityType string

const (
	// CapacityProvisioned represents reserved/guaranteed throughput capacity.
	CapacityProvisioned CapacityType = "provisioned"
	// CapacityOnDemand represents pay-per-use capacity that may be throttled.
	CapacityOnDemand CapacityType = "ondemand"
)

// QuotaKey uniquely identifies a quota scope.
type QuotaKey struct {
	// Backend is the backend identifier in "namespace.name" format.
	// Example: "default.openai-gpt4"
	Backend string
	// Model is the model name.
	// Example: "gpt-4"
	Model string
}

// String returns a string representation of the QuotaKey.
func (k QuotaKey) String() string {
	return k.Backend + ":" + k.Model
}

// QuotaConfig holds the quota limits for a backend/model.
type QuotaConfig struct {
	// ProvisionedTPM is the provisioned tokens per minute limit.
	ProvisionedTPM uint64
	// OnDemandTPM is the on-demand tokens per minute limit. 0 means unlimited.
	OnDemandTPM uint64
	// WindowDuration is the sliding window duration for quota tracking.
	WindowDuration time.Duration
}

// RecordUsageRequest represents a token usage recording request.
type RecordUsageRequest struct {
	// Key identifies the backend and model.
	Key QuotaKey
	// InputTokens is the number of input tokens consumed.
	InputTokens uint64
	// OutputTokens is the number of output tokens consumed.
	OutputTokens uint64
	// CapacityType indicates whether this usage is against provisioned or on-demand capacity.
	CapacityType CapacityType
	// Timestamp is when the usage occurred.
	Timestamp time.Time
}

// UsageSnapshot represents aggregated token usage within a time window.
type UsageSnapshot struct {
	// Key identifies the backend and model.
	Key QuotaKey
	// WindowStart is the start of the aggregation window.
	WindowStart time.Time
	// WindowEnd is the end of the aggregation window.
	WindowEnd time.Time

	// ProvisionedInput is the total input tokens used from provisioned capacity.
	ProvisionedInput uint64
	// ProvisionedOutput is the total output tokens used from provisioned capacity.
	ProvisionedOutput uint64

	// OnDemandInput is the total input tokens used from on-demand capacity.
	OnDemandInput uint64
	// OnDemandOutput is the total output tokens used from on-demand capacity.
	OnDemandOutput uint64
}

// TotalTokens returns total tokens across all capacity types.
func (s *UsageSnapshot) TotalTokens() uint64 {
	return s.ProvisionedInput + s.ProvisionedOutput +
		s.OnDemandInput + s.OnDemandOutput
}

// ProvisionedTotal returns total provisioned tokens (input + output).
func (s *UsageSnapshot) ProvisionedTotal() uint64 {
	return s.ProvisionedInput + s.ProvisionedOutput
}

// OnDemandTotal returns total on-demand tokens (input + output).
func (s *UsageSnapshot) OnDemandTotal() uint64 {
	return s.OnDemandInput + s.OnDemandOutput
}

// CapacityStatus represents current capacity utilization.
type CapacityStatus struct {
	// Key identifies the backend and model.
	Key QuotaKey
	// ProvisionedUsed is the tokens used from provisioned capacity in the current window.
	ProvisionedUsed uint64
	// ProvisionedLimit is the prorated provisioned token limit for the window.
	ProvisionedLimit uint64
	// ProvisionedRemaining is the remaining provisioned capacity.
	ProvisionedRemaining uint64
	// OnDemandUsed is the tokens used from on-demand capacity in the current window.
	OnDemandUsed uint64
	// OnDemandLimit is the prorated on-demand token limit for the window. 0 means unlimited.
	OnDemandLimit uint64
	// OnDemandRemaining is the remaining on-demand capacity. 0 with OnDemandLimit=0 means unlimited.
	OnDemandRemaining uint64
}

// ProvisionedUtilization returns the provisioned capacity utilization as a percentage (0-100+).
func (s *CapacityStatus) ProvisionedUtilization() float64 {
	if s.ProvisionedLimit == 0 {
		return 0
	}
	return float64(s.ProvisionedUsed) / float64(s.ProvisionedLimit) * 100
}

// OnDemandUtilization returns the on-demand capacity utilization as a percentage (0-100+).
// Returns 0 if on-demand is unlimited.
func (s *CapacityStatus) OnDemandUtilization() float64 {
	if s.OnDemandLimit == 0 {
		return 0
	}
	return float64(s.OnDemandUsed) / float64(s.OnDemandLimit) * 100
}
