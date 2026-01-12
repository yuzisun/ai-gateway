# Quota-Aware Routing Proposal

## Overview

This proposal describes a quota-aware routing system for AI Gateway that enables intelligent traffic distribution between Provisioned Throughput (PT) and On-Demand capacity endpoints based on real-time quota consumption. The system leverages the existing `AIGatewayRoute` backendRefs and routing rules to define endpoint pools, with quota enforcement applied at the upstream ext_proc filter level.

## Goals

1. **Soft Quota Enforcement**: Implement soft quota limits per AIServiceBackend where requests exceeding the quota trigger fallback routing instead of rejection
2. **Capacity-Aware Routing**: Route requests to PT backends when quota is available, automatically fallback to on-demand backends when PT quota is exhausted
3. **Priority-Based Fallback**: When quota is exhausted for a backend, skip it and try the next backend in priority order
4. **Reuse Existing Primitives**: Leverage existing `backendRefs` and routing rules to define PT and on-demand endpoint pools across multiple regions/providers

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Request Flow                                   │
└─────────────────────────────────────────────────────────────────────────────┘

                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Router-Level AI Gateway ExtProc Filter                   │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  1. Parse request, extract model                                    │    │
│  │  2. Resolve backend based on AIGatewayRoute rules                   │    │
│  │  3. Set headers for upstream routing (PT/OD endpoint pool)          │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Envoy Router (Route Selection)                      │
│  ┌─────────────────────────────────────────────────────────────────────-┐   │
│  │  Select cluster based on route matching                              │   │
│  └─────────────────────────────────────────────────────────────────────-┘   │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────-┐
│              Upstream AI Gateway ExtProc Filter (per-cluster)                │
│  ┌─────────────────────────────────────────────────────────────────────-┐    │
│  │  1. Check quota for current backend (rate limit in quota mode)       │    │
│  │  2. If quota available → Proceed to backend                          │    │
│  │  3. If quota exceeded (soft limit) →                                 │    │
│  │     a. Set dynamic metadata: quota_exceeded = true                   │    │
│  │     b. Return "try next backend" signal                              │    │
│  │     c. Skip this backend, fallback to next priority backend          │    │
│  │  4. Transform request to backend schema                              │    │
│  └─────────────────────────────────────────────────────────────────────-┘    │
│                                                                              │
│  Rate Limit Check (Quota Mode):                                              │
│  - Calls rate limit service for backend quota                                │
│  - Returns quota status in dynamic metadata                                  │
│  - Does NOT reject request, allows fallback routing                          │
└─────────────────────────────────────────────────────────────────────────────-┘
                                    │
                    ┌───────────────┼──────────────--─┐
                    │               │                 │
            ┌───────▼───────┐ ┌─────▼─────-┐  ┌───────▼───────┐
            │ Backend 1 (PT)│ │ Backend 2  │  │ Backend 3 (OD)│
            │ Priority: 1   │ │ Priority: 1│  │ Priority: 2   │
            │ AWS us-east-1 │ │ GCP central│  │ Anthropic API │
            └───────────────┘ └───────────-┘  └───────────────┘
```

## Key Design Decisions

### 0. Quota Check at Route Rate Limit Filter

The rate limit check happens at the **router rate limit filter** (per-cluster) based on available tenant token quota:

- Enforce max token usage for a given tenant and reject the request when the max limit is over
- Soft limit check for a given tenant and select the burst or on-demand endpoint pool when soft min limit is over


### 1. Quota Check at Upstream Rate Limit Filter

The rate limit check happens at the **upstream rate limit filter** (per-cluster) for backend selection based on available model provider quota:

- The upstream filter knows which specific backend was selected
- It can make backend-specific quota decisions
- It can signal fallback to the routing layer when quota is exceeded
- The rate limit service can be called with backend-specific descriptors

### 2. Reuse Existing BackendRefs for Endpoint Pools

Instead of defining endpoint pools in `AIServiceBackend`, we use the existing `AIGatewayRoute.backendRefs` with priority ordering:

```yaml
backendRefs:
  - name: aws-claude-pt-us-east-1      # PT, Priority 1
  - name: aws-claude-pt-us-west-2      # PT, Priority 1
  - name: gcp-claude-pt-us-central1    # PT, Priority 1
  - name: anthropic-claude-ondemand    # On-demand, Priority 2 (fallback)
```

### 3. Fallback via Backend Retry with Quota Skip

When a backend's quota is exceeded:
1. The upstream rate limit filter marks the backend as "quota exceeded" in dynamic metadata
2. The request is retried to the next priority backend
3. Backends with exceeded quota are skipped in the retry chain

## API Design

### QuotaPolicy in AIServiceBackend

```yaml
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: aws-claude-pt-us-east-1
  namespace: ai-gateway
spec:
  backendRef:
    name: bedrock-us-east-1
    port: 443
  # Backend quota policy configuration
  backendQuotaRef:
    name: aws-bedrock-model-quota
---
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: QuotaPolicy
metadata:
  name: aws-bedrock-model-quota
  namespace: ai-gateway
spec:
  perModelQuota:
    - modelName: claude-4-sonnet
      costExpression: input_tokens + 3 * output_tokens + 0.1 * cached_input_tokens + 1.25 * cache_creation_input_tokens
      rules:
        - clientSelectors:
          - headers:
              - name: service_tier
                value: reserved
          quotaValue:
            limit: 1M
            duration: 30s
        - clientSelectors:
            - headers:
              - name: service_tier
                value: default
          quotaValue:
            limit: 2M
            duration: 60s
```

### AIGatewayRoute with Priority-Based BackendRefs

```yaml
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: claude-route
  namespace: ai-gateway
spec:
  parentRefs:
    - name: ai-gateway

  rules:
    - matches:
        - headers:
            - name: x-ai-model
              value: claude-4-sonnet

      # Backend refs in priority order (first = highest priority)
      # When quota exceeded, fallback to next in list
      backendRefs:
        # Priority 1: AWS PT us-east-1
        - name: aws-claude-pt-us-east-1
          priority: 0

        # Priority 1: AWS PT us-west-2 (regional failover)
        - name: aws-claude-pt-us-west-2
          priority: 0  # Only used as fallback

        # Priority 1: GCP PT us-central1 (cross-cloud failover)
        - name: gcp-claude-pt-us-central1
          priority: 0

        # Priority 4: On-demand fallback (always available)
        - name: anthropic-claude-ondemand
          priority: 1

      # Quota-based routing configuration
      quotaRouting:
        enabled: true
        # Retry to next backend when quota exceeded
        fallbackOnQuotaExceeded: true
        # Maximum backends to try before failing
        maxFallbackAttempts: 3
```

## Upstream ExtProc Filter Flow

### Quota Check Implementation

```go
// ProcessRequestHeaders in upstream ext_proc filter
func (p *UpstreamProcessor) ProcessRequestHeaders(ctx context.Context, headers *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
    // 1. Get backend info from cluster metadata
    backend := p.getBackendFromClusterMetadata()

    // 2. Check quota for this backend
    quotaStatus, err := p.checkQuota(ctx, backend)
    if err != nil {
        p.logger.Error("quota check failed", "error", err, "backend", backend.Name)
        if p.failOpen {
            // Continue without quota check
            return p.continueProcessing()
        }
        return p.rejectRequest(err)
    }

    // 3. If quota exceeded (soft limit), signal fallback
    if quotaStatus.SoftLimitExceeded {
        p.logger.Info("quota exceeded, signaling fallback",
            "backend", backend.Name,
            "used", quotaStatus.Used,
            "limit", quotaStatus.SoftLimit)

        return p.signalFallbackToNextBackend(backend, quotaStatus)
    }

    // 4. Quota available, proceed with request
    return p.continueProcessing()
}

// signalFallbackToNextBackend returns a response that triggers retry to next backend
func (p *UpstreamProcessor) signalFallbackToNextBackend(backend *Backend, status *QuotaStatus) (*extprocv3.ProcessingResponse, error) {
    // Set dynamic metadata to indicate quota exceeded
    // This metadata is used by retry policy to skip this backend
    return &extprocv3.ProcessingResponse{
        Response: &extprocv3.ProcessingResponse_RequestHeaders{
            RequestHeaders: &extprocv3.HeadersResponse{
                Response: &extprocv3.CommonResponse{
                    // Clear route cache to allow re-routing
                    ClearRouteCache: true,
                },
            },
        },
        DynamicMetadata: &structpb.Struct{
            Fields: map[string]*structpb.Value{
                "io.envoy.ai_gateway": structpb.NewStructValue(&structpb.Struct{
                    Fields: map[string]*structpb.Value{
                        "quota_exceeded":       structpb.NewBoolValue(true),
                        "exceeded_backend":     structpb.NewStringValue(backend.Name),
                        "quota_used":           structpb.NewNumberValue(float64(status.Used)),
                        "quota_limit":          structpb.NewNumberValue(float64(status.SoftLimit)),
                        "fallback_required":    structpb.NewBoolValue(true),
                    },
                }),
            },
        },
        ModeOverride: &extprocv3.ProcessingMode{
            // Skip body processing for fallback
            RequestBodyMode: extprocv3.ProcessingMode_NONE,
        },
    }, nil
}
```

### Quota Check with Rate Limit Service

```go
// checkQuota calls the rate limit service in quota mode
func (p *UpstreamProcessor) checkQuota(ctx context.Context, backend *Backend) (*QuotaStatus, error) {
    // Build rate limit request for this backend
    request := &ratelimitv3.RateLimitRequest{
        Domain: "ai-gateway-quota",
        Descriptors: []*ratelimitv3.RateLimitDescriptor{
            {
                Entries: []*ratelimitv3.RateLimitDescriptor_Entry{
                    {Key: "backend", Value: backend.Name},
                    {Key: "model", Value: p.model},
                },
            },
        },
        HitsAddend: 1, // Pre-check with 1 token, actual usage recorded post-response
    }

    // Call rate limit service
    response, err := p.rateLimitClient.ShouldRateLimit(ctx, request)
    if err != nil {
        return nil, fmt.Errorf("rate limit service error: %w", err)
    }

    // Parse quota status from response
    status := &QuotaStatus{
        Used:              p.parseUsedFromMetadata(response.DynamicMetadata),
        SoftLimit:         backend.QuotaPolicy.SoftLimit.TokensPerMinute,
        HardLimit:         backend.QuotaPolicy.HardLimit.TokensPerMinute,
        SoftLimitExceeded: response.OverallCode == ratelimitv3.RateLimitResponse_OVER_LIMIT,
    }

    // In quota mode, OVER_LIMIT means soft limit exceeded but request is not rejected
    if response.DynamicMetadata != nil {
        if quotaMode := response.DynamicMetadata.Fields["quotaModeEnabled"]; quotaMode != nil && quotaMode.GetBoolValue() {
            status.QuotaModeActive = true
        }
    }

    return status, nil
}
```

## Fallback Routing Implementation

### Option 1: ExtProc-Driven Fallback with Clear Route Cache

```go
// When quota exceeded, clear route cache and set header for next backend selection
func (p *UpstreamProcessor) triggerFallback(exceededBackend string, priority int) (*extprocv3.ProcessingResponse, error) {
    return &extprocv3.ProcessingResponse{
        Response: &extprocv3.ProcessingResponse_RequestHeaders{
            RequestHeaders: &extprocv3.HeadersResponse{
                Response: &extprocv3.CommonResponse{
                    // Clear route cache forces re-evaluation
                    ClearRouteCache: true,
                    HeaderMutation: &extprocv3.HeaderMutation{
                        SetHeaders: []*corev3.HeaderValueOption{
                            {
                                Header: &corev3.HeaderValue{
                                    Key:   "x-ai-skip-backends",
                                    Value: exceededBackend,
                                },
                                AppendAction: corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD,
                            },
                            {
                                Header: &corev3.HeaderValue{
                                    Key:   "x-ai-fallback-priority",
                                    Value: strconv.Itoa(priority + 1),
                                },
                                AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
                            },
                        },
                    },
                },
            },
        },
    }, nil
}
```

### Option 2: Envoy Retry Policy with Quota-Based Retry Predicate

Configure Envoy retry policy to retry on quota exceeded:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: quota-retry-policy
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: claude-route

  retry:
    numRetries: 3
    retryOn:
      - "retriable-status-codes"
    retriableStatusCodes:
      - 529  # Custom status for "quota exceeded, try next backend"
    perTryTimeout: 30s

    # Retry to different backend (not same one)
    retryHostPredicate:
      - name: envoy.retry_host_predicates.previous_hosts
```

## Rate Limit Service Configuration

### Per-Backend Quota Descriptors

```yaml
domain: ai-gateway-quota
descriptors:
  # AWS Claude PT us-east-1
  - key: backend
    value: aws-claude-pt-us-east-1
    descriptors:
      - key: model
        value: claude-3-sonnet
        rate_limit:
          unit: minute
          requests_per_unit: 1000
        quota_mode: true  # Don't reject, just track

  # AWS Claude PT us-west-2
  - key: backend
    value: aws-claude-pt-us-west-2
    descriptors:
      - key: model
        value: claude-3-sonnet
        rate_limit:
          unit: minute
          requests_per_unit: 800
        quota_mode: true

  # On-demand (high limit or unlimited)
  - key: backend
    value: anthropic-claude-ondemand
    descriptors:
      - key: model
        value: claude-3-sonnet
        rate_limit:
          unit: minute
          requests_per_unit: 100000  # Very high for on-demand
        quota_mode: true
```

## Sequence Diagram

```
┌──────┐     ┌─────────────┐     ┌────────┐     ┌──────────────┐     ┌──────────────┐     ┌─────────────┐
│Client│     │Router ExtProc│    │ Router │     │Upstream ExtProc│   │RateLimit Svc │     │   Backend   │
└──┬───┘     └──────┬──────┘     └───┬────┘     └───────┬──────┘     └──────┬───────┘     └──────┬──────┘
   │                │                │                   │                   │                   │
   │ POST /chat     │                │                   │                   │                   │
   │───────────────>│                │                   │                   │                   │
   │                │                │                   │                   │                   │
   │                │ Set model hdr  │                   │                   │                   │
   │                │───────────────>│                   │                   │                   │
   │                │                │                   │                   │                   │
   │                │                │ Route to          │                   │                   │
   │                │                │ Backend-1 cluster │                   │                   │
   │                │                │──────────────────>│                   │                   │
   │                │                │                   │                   │                   │
   │                │                │                   │ Check quota       │                   │
   │                │                │                   │ (backend-1)       │                   │
   │                │                │                   │──────────────────>│                   │
   │                │                │                   │                   │                   │
   │                │                │                   │ OVER_LIMIT        │                   │
   │                │                │                   │ (quota mode)      │                   │
   │                │                │                   │<──────────────────│                   │
   │                │                │                   │                   │                   │
   │                │                │ ClearRouteCache   │                   │                   │
   │                │                │ + skip backend-1  │                   │                   │
   │                │                │<──────────────────│                   │                   │
   │                │                │                   │                   │                   │
   │                │                │ Route to          │                   │                   │
   │                │                │ Backend-2 cluster │                   │                   │
   │                │                │──────────────────>│                   │                   │
   │                │                │                   │                   │                   │
   │                │                │                   │ Check quota       │                   │
   │                │                │                   │ (backend-2)       │                   │
   │                │                │                   │──────────────────>│                   │
   │                │                │                   │                   │                   │
   │                │                │                   │ OK (under limit)  │                   │
   │                │                │                   │<──────────────────│                   │
   │                │                │                   │                   │                   │
   │                │                │                   │ Forward request   │                   │
   │                │                │                   │──────────────────────────────────────>│
   │                │                │                   │                   │                   │
   │                │                │                   │                   │      Response     │
   │                │                │                   │<──────────────────────────────────────│
   │                │                │                   │                   │                   │
   │                │                │                   │ Record usage      │                   │
   │                │                │                   │──────────────────>│                   │
   │                │                │                   │                   │                   │
   │<───────────────────────────────────────────────────────────────────────────────────────────│
   │  Response                       │                   │                   │                   │
```

## Metrics and Observability

```go
var (
    quotaCheckTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "ai_gateway_quota_checks_total",
            Help: "Total quota checks per backend",
        },
        []string{"backend", "result"},  // result: "allowed", "exceeded", "error"
    )

    quotaFallbackTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "ai_gateway_quota_fallbacks_total",
            Help: "Total fallbacks due to quota exceeded",
        },
        []string{"from_backend", "to_backend"},
    )

    quotaUtilization = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "ai_gateway_quota_utilization_ratio",
            Help: "Current quota utilization (0.0-1.0+)",
        },
        []string{"backend", "capacity_type"},
    )
)
```

## Implementation Phases

### Phase 1: Quota Check in Upstream ExtProc
- Add quota check call to rate limit service in upstream filter
- Parse quota mode response
- Record quota metrics

### Phase 2: Fallback Routing
- Implement `ClearRouteCache` with skip header
- Add backend skip logic to router-level ext_proc
- Track fallback metrics

### Phase 3: Priority-Based Backend Selection
- Support priority ordering in `backendRefs`
- Implement max fallback attempts
- Add circuit breaker for repeatedly failing backends

### Phase 4: Token-Based Quota Tracking
- Integrate with quota tracker for token-based limits
- Support weighted token calculation
- Record actual usage post-response

## Open Questions

1. How to handle the latency overhead of quota check on each request?
   - Option: Cache quota status with short TTL
   - Option: Async quota check with optimistic routing

2. Should fallback be transparent to the client or return a header indicating fallback occurred?

3. How to handle streaming requests where token count is unknown upfront?
   - Option: Estimate based on input tokens
   - Option: Reserve capacity and reconcile after response

4. Should there be a "sticky" preference to avoid flip-flopping between backends near quota boundary?
