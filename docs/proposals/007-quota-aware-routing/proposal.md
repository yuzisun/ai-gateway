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
// Note: ClearRouteCache does NOT work in upstream filters because the route has already
// been selected by the Router. Instead, we return a retriable status code (529) to trigger
// Envoy's retry mechanism which will select a different backend.
func (p *UpstreamProcessor) signalFallbackToNextBackend(backend *Backend, status *QuotaStatus) (*extprocv3.ProcessingResponse, error) {
    // Return immediate response with retriable status code
    // This triggers Envoy's retry policy to try the next backend
    return &extprocv3.ProcessingResponse{
        Response: &extprocv3.ProcessingResponse_ImmediateResponse{
            ImmediateResponse: &extprocv3.ImmediateResponse{
                Status: &typev3.HttpStatus{
                    Code: typev3.StatusCode_ServiceUnavailable, // 429 or custom 529
                },
                Headers: &extprocv3.HeaderMutation{
                    SetHeaders: []*corev3.HeaderValueOption{
                        {
                            Header: &corev3.HeaderValue{
                                Key:   "x-envoy-retriable-header-names",
                                Value: "x-quota-exceeded",
                            },
                        },
                        {
                            Header: &corev3.HeaderValue{
                                Key:   "x-quota-exceeded",
                                Value: "true",
                            },
                        },
                        {
                            Header: &corev3.HeaderValue{
                                Key:   "x-exceeded-backend",
                                Value: backend.Name,
                            },
                        },
                    },
                },
                Body: []byte(fmt.Sprintf(`{"error": "quota_exceeded", "backend": "%s", "used": %d, "limit": %d}`,
                    backend.Name, status.Used, status.SoftLimit)),
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

> **Note:** `ClearRouteCache` does NOT work in upstream filters because by the time
> the request reaches the upstream filter, the Router has already selected the route
> and cluster. The route decision is committed before upstream filters execute.
> Therefore, we use Envoy's **priority-based routing with retry** to achieve fallback.

### Priority-Based Backend Configuration

Envoy's load balancer supports **priority levels** for endpoints. When backends at a higher
priority level fail (or return retriable errors), Envoy can failover to lower priority backends.
This maps naturally to PT (priority 0) vs On-Demand (priority 1) routing.

#### How Priority-Based Load Balancing Works

1. **Priority 0 (highest)**: PT backends with quota limits
2. **Priority 1 (fallback)**: On-demand backends (always available)

Envoy selects backends from priority 0 first. When:
- All priority 0 backends return retriable errors (quota exceeded → 429)
- The `previous_priorities` retry predicate marks priority 0 as exhausted

Envoy automatically fails over to priority 1 backends.

### Envoy Cluster Configuration with Priority Levels

The cluster endpoints are configured with explicit priority levels. Each backend maps to
a locality within a priority level:

```yaml
# Generated Envoy cluster configuration
cluster:
  name: "ai-gateway-backends"
  load_balancing_policy:
    policies:
      - typed_extension_config:
          name: envoy.load_balancing_policies.least_request
          typed_config:
            "@type": type.googleapis.com/envoy.extensions.load_balancing_policies.least_request.v3.LeastRequest
            locality_lb_config:
              locality_weighted_lb_config: {}

  load_assignment:
    cluster_name: "ai-gateway-backends"
    endpoints:
      # Priority 0: PT backends (try first)
      - priority: 0
        locality:
          region: "aws-claude-pt-us-east-1"
        lb_endpoints:
          - endpoint:
              address:
                socket_address:
                  address: "bedrock.us-east-1.amazonaws.com"
                  port_value: 443
            metadata:
              filter_metadata:
                aigateway.envoy.io:
                  backend_name: "aws-claude-pt-us-east-1"
                  capacity_type: "provisioned"
            load_balancing_weight: 1
        load_balancing_weight: 1

      - priority: 0
        locality:
          region: "aws-claude-pt-us-west-2"
        lb_endpoints:
          - endpoint:
              address:
                socket_address:
                  address: "bedrock.us-west-2.amazonaws.com"
                  port_value: 443
            metadata:
              filter_metadata:
                aigateway.envoy.io:
                  backend_name: "aws-claude-pt-us-west-2"
                  capacity_type: "provisioned"
            load_balancing_weight: 1
        load_balancing_weight: 1

      # Priority 1: On-demand backends (fallback)
      - priority: 1
        locality:
          region: "anthropic-claude-ondemand"
        lb_endpoints:
          - endpoint:
              address:
                socket_address:
                  address: "api.anthropic.com"
                  port_value: 443
            metadata:
              filter_metadata:
                aigateway.envoy.io:
                  backend_name: "anthropic-claude-ondemand"
                  capacity_type: "on-demand"
            load_balancing_weight: 1
        load_balancing_weight: 1
```

### Retry Policy with Priority Failover

Configure the retry policy to use `previous_priorities` predicate, which tracks failed
priority levels and skips them on retry:

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
      - "5xx"
    retriableStatusCodes:
      - 503
      - 429 # Returned by ext_proc when quota exceeded
    perTryTimeout: 30s

    # Skip hosts that were already attempted
    retryHostPredicate:
      - name: envoy.retry_host_predicates.previous_hosts

    # Skip priority levels where all hosts failed
    retryPriority:
      name: envoy.retry_priorities.previous_priorities
      typedConfig:
        "@type": type.googleapis.com/envoy.extensions.retry.priority.previous_priorities.v3.PreviousPrioritiesConfig
        updateFrequency: 2  # Update priority load after every 2 retries

    # Allow multiple host selection attempts within each retry
    hostSelectionRetryMaxAttempts: 5
```

### How Priority Failover Works

1. **Initial Request**: LB selects host from priority 0 (e.g., `aws-claude-pt-us-east-1`)
2. **Quota Check**: Upstream ext_proc checks quota → exceeded
3. **429 Response**: Ext_proc returns immediate 429 response
4. **First Retry**:
   - `previous_hosts` predicate rejects `aws-claude-pt-us-east-1`
   - LB selects another priority 0 host (e.g., `aws-claude-pt-us-west-2`)
5. **Quota Check Again**: Also exceeded → 429
6. **Second Retry**:
   - `previous_hosts` rejects both attempted hosts
   - No more hosts in priority 0 available
   - `previous_priorities` marks priority 0 as exhausted
   - LB fails over to priority 1
7. **Priority 1 Success**: Request goes to `anthropic-claude-ondemand`

### Key Benefits of Priority-Based Approach

1. **Explicit Failover Order**: PT backends always tried before on-demand
2. **Efficient Skip**: Once a priority is exhausted, entire level is skipped
3. **Native Envoy Support**: Uses built-in retry predicates, no custom code
4. **Works with Locality LB**: Compatible with existing locality-weighted config
5. **Configurable**: Can adjust `updateFrequency` to control failover sensitivity

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

### Priority-Based Failover Flow

```
┌──────┐     ┌─────────────┐     ┌────────┐     ┌──────────────┐     ┌──────────────┐     ┌─────────────┐
│Client│     │Router ExtProc│    │Router/LB│    │Upstream ExtProc│   │RateLimit Svc │     │   Backend   │
└──┬───┘     └──────┬──────┘     └───┬────┘     └───────┬──────┘     └──────┬───────┘     └──────┬──────┘
   │                │                │                   │                   │                   │
   │ POST /chat     │                │                   │                   │                   │
   │───────────────>│                │                   │                   │                   │
   │                │                │                   │                   │                   │
   │                │ Set model hdr  │                   │                   │                   │
   │                │───────────────>│                   │                   │                   │
   │                │                │                   │                   │                   │
   │                │                │ Select Priority 0 │                   │                   │
   │                │                │ Host: PT-east-1   │                   │                   │
   │                │                │──────────────────>│                   │                   │
   │                │                │                   │                   │                   │
   │                │                │                   │ Check quota       │                   │
   │                │                │                   │ (PT-east-1)       │                   │
   │                │                │                   │──────────────────>│                   │
   │                │                │                   │                   │                   │
   │                │                │                   │ OVER_LIMIT        │                   │
   │                │                │                   │<──────────────────│                   │
   │                │                │                   │                   │                   │
   │                │                │ 429 Response      │                   │                   │
   │                │                │<──────────────────│                   │                   │
   │                │                │                   │                   │                   │
   │                │                │ Retry #1:         │                   │                   │
   │                │                │ previous_hosts    │                   │                   │
   │                │                │ skips PT-east-1   │                   │                   │
   │                │                │ Select PT-west-2  │                   │                   │
   │                │                │──────────────────>│                   │                   │
   │                │                │                   │                   │                   │
   │                │                │                   │ Check quota       │                   │
   │                │                │                   │ (PT-west-2)       │                   │
   │                │                │                   │──────────────────>│                   │
   │                │                │                   │                   │                   │
   │                │                │                   │ OVER_LIMIT        │                   │
   │                │                │                   │<──────────────────│                   │
   │                │                │                   │                   │                   │
   │                │                │ 429 Response      │                   │                   │
   │                │                │<──────────────────│                   │                   │
   │                │                │                   │                   │                   │
   │                │                │ Retry #2:         │                   │                   │
   │                │                │ previous_priorities│                  │                   │
   │                │                │ marks P0 exhausted│                   │                   │
   │                │                │ Failover to P1    │                   │                   │
   │                │                │ Select: On-demand │                   │                   │
   │                │                │──────────────────>│                   │                   │
   │                │                │                   │                   │                   │
   │                │                │                   │ Check quota       │                   │
   │                │                │                   │ (On-demand)       │                   │
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

### Legend

- **Priority 0 (P0)**: Provisioned Throughput backends (PT-east-1, PT-west-2)
- **Priority 1 (P1)**: On-demand backends (fallback)
- **previous_hosts**: Retry predicate that skips already-attempted hosts
- **previous_priorities**: Retry predicate that skips exhausted priority levels

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

## Implementation Items

### 1: Quota Check in Upstream ExtProc
- Parse quota mode dynamic metadata set by the rate limit filter (soft limit exceeded vs. hard limit)
- Return immediate 429 response when quota exceeded to trigger retry
- Record quota check metrics per backend

### 2: Retry Policy Configuration
- Configure `previous_hosts` retry predicate to skip already-attempted hosts
- Configure `previous_priorities` retry predicate to failover to lower priority levels
- Set appropriate `hostSelectionRetryMaxAttempts` for host selection within retry
- Set `numRetries` based on number of backends across all priorities
- Configure `retriableStatusCodes` to include 429

### 3: Token-Based Quota Tracking
- Integrate with quota tracker for token-based limits
- Support weighted token calculation (input, output, cached tokens)
- Record actual usage post-response to rate limit service
- Implement quota reconciliation for streaming responses

## Open Questions

1. How to handle the latency overhead of quota check on each request?
   - Option: Cache quota status with short TTL
   - Option: Async quota check with optimistic routing

2. Should fallback be transparent to the client or return a header indicating fallback occurred?

3. How to handle streaming requests where token count is unknown upfront?
   - Option: Estimate based on input tokens
   - Option: Reserve capacity and reconcile after response

4. Should there be a "sticky" preference to avoid flip-flopping between backends near quota boundary?
