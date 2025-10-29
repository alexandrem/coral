# Coral SDK Integration Guide

Coral works in **two modes**:

1. **Passive mode** (no SDK): Basic observability - process monitoring, connection mapping, AI debugging
2. **SDK-integrated mode**: Full operations control - feature flags, traffic inspection, profiling, rollbacks + enhanced observability

This guide covers SDK integration for full control capabilities.

---

## Quick Start

**5-minute integration for full control:**

```go
import coral "github.com/coral-io/coral-go"

func main() {
    // Initialize Coral SDK
    coral.Initialize(coral.Config{
        ServiceName: "api",
        Version:     "2.1.0",
    })
    defer coral.Shutdown()

    // Feature flags
    if coral.IsEnabled("new-checkout") {
        useNewCheckout()
    }

    // Enable traffic sampling
    coral.EnableTrafficSampling(coral.TrafficOptions{
        SampleRate: 0.1, // 10% of requests
    })

    // Enable remote profiling
    coral.EnableProfiling()

    // Your app runs normally
    http.ListenAndServe(":8080", handler)
}
```

---

## SDK Philosophy: Two-Tier Operations Model

### Passive Mode (Tier 0): No SDK Required

**What you get without SDK:**
- Process monitoring (CPU, memory, connections)
- Network topology discovery (via netstat/ss)
- HTTP health check polling
- AI-powered debugging from observed metrics
- Auto-discovered service dependencies

**Use case:** Quick setup, no code changes needed

### SDK-Integrated Mode (Tier 1): Full Operations Control

**Primary value: Control capabilities**
- ✅ **Feature flags**: Toggle features remotely across services
- ✅ **Traffic inspection**: Sample and inspect live requests
- ✅ **Profiling**: Trigger profilers remotely (CPU, heap, goroutine)
- ✅ **Rollbacks**: One-command rollback coordination

**Bonus value: Enhanced observability**
- ✅ Accurate version tracking (git commit, build time)
- ✅ Component-level health (database, cache, etc.)
- ✅ Build metadata and deployment events
- ✅ Endpoint discovery for standards (Prometheus, OTEL)

**Use case:** Full unified operations control

---

## Control Capabilities (Primary Value)

### 1. Feature Flags

**Enable runtime feature toggling without redeployment:**

```go
// In your application code
import coral "github.com/coral-io/coral-go"

func checkoutHandler(w http.ResponseWriter, r *http.Request) {
    // Check feature flag
    if coral.IsEnabled("new-checkout-flow") {
        handleNewCheckout(w, r)
    } else {
        handleLegacyCheckout(w, r)
    }
}

// Advanced: gradual rollout with user targeting
func paymentHandler(w http.ResponseWriter, r *http.Request) {
    user := getUserFromRequest(r)

    if coral.IsEnabledForUser("stripe-v2", user.ID) {
        useStripeV2()
    } else {
        useStripeV1()
    }
}
```

**Control from CLI:**

```bash
# Enable feature globally
coral flags enable new-checkout-flow

# Gradual rollout (10% of traffic)
coral flags enable new-checkout-flow --gradual 10%

# Target specific users/segments
coral flags enable stripe-v2 --segment beta-users

# Disable feature
coral flags disable new-checkout-flow

# List all flags
coral flags list --service api
```

**Feature flag configuration:**

```go
coral.RegisterFeatureFlag("new-checkout-flow", coral.FlagConfig{
    DefaultEnabled: false,
    Description:    "New checkout flow with optimized UX",
    Variants: map[string]interface{}{
        "control":    "legacy-checkout",
        "treatment":  "new-checkout",
    },
})
```

**SDK Implementation:**

```protobuf
// Feature Flags API
service FeatureFlags {
    // Check if flag is enabled
    rpc IsEnabled(FlagRequest) returns (FlagResponse);

    // List all flags
    rpc ListFlags(ListFlagsRequest) returns (ListFlagsResponse);

    // Update flag state (called by colony)
    rpc UpdateFlag(UpdateFlagRequest) returns (UpdateFlagResponse);
}

message FlagRequest {
    string flag_name = 1;
    string user_id = 2;      // Optional: for user-targeted flags
    string segment = 3;       // Optional: for segment-targeted flags
    map<string, string> context = 4;
}

message FlagResponse {
    bool enabled = 1;
    string variant = 2;
    string reason = 3;  // "global", "user-targeted", "segment", "gradual-10%"
}
```

### 2. Traffic Inspection

**Sample and inspect live HTTP requests without SSH:**

```go
// Enable traffic sampling in your app
coral.EnableTrafficSampling(coral.TrafficOptions{
    SampleRate: 0.1,  // Sample 10% of requests
    MaxBodySize: 10 * 1024,  // Capture up to 10KB of body
    IncludeHeaders: true,
    IncludeBodies: true,
    Filters: coral.TrafficFilters{
        Paths: []string{"/api/*", "/checkout"},  // Only these paths
        Methods: []string{"POST", "PUT"},         // Only mutations
        StatusCodes: []int{500, 502, 503},       // Only errors
    },
})

// Or use HTTP middleware for automatic instrumentation
http.Handle("/", coral.TrafficMiddleware(yourHandler))
```

**Inspect from CLI:**

```bash
# Start sampling 10% of API traffic for 5 minutes
coral traffic sample api --rate 0.1 --duration 5m

# Inspect live traffic with filters
coral traffic inspect api --filter "path=/checkout"
coral traffic inspect api --filter "status>=500"
coral traffic inspect api --filter "method=POST"

# View captured request details
coral traffic show <request-id>

# Export traffic samples for analysis
coral traffic export api --format json --output traffic.json
```

**Example output:**

```
Capturing traffic from api (10% sample rate)...

[14:23:15] POST /api/checkout → 500 (234ms)
  Request:
    User-Agent: Mozilla/5.0
    Content-Type: application/json
    Body: {"cart_id": "abc123", "payment": {...}}

  Response:
    Status: 500 Internal Server Error
    Body: {"error": "Payment gateway timeout"}

  Trace: Connected to payment-gateway → timeout after 30s

[14:23:42] POST /api/checkout → 200 (89ms)
  Request: {...}
  Response: {...}
  ✓ Success

Press Ctrl+C to stop. 15 requests captured (3 errors).
```

**SDK Implementation:**

```protobuf
service TrafficInspection {
    // Enable/configure traffic sampling
    rpc ConfigureSampling(SamplingConfig) returns (SamplingResponse);

    // Stream captured traffic samples
    rpc StreamSamples(StreamRequest) returns (stream TrafficSample);

    // Get specific captured request
    rpc GetSample(GetSampleRequest) returns (TrafficSample);
}

message TrafficSample {
    string request_id = 1;
    google.protobuf.Timestamp timestamp = 2;

    // Request
    string method = 3;
    string path = 4;
    map<string, string> request_headers = 5;
    bytes request_body = 6;

    // Response
    int32 status_code = 7;
    map<string, string> response_headers = 8;
    bytes response_body = 9;

    // Timing
    google.protobuf.Duration duration = 10;

    // Context
    string trace_id = 11;
    string user_id = 12;
}
```

### 3. Remote Profiling

**Trigger profilers remotely without SSH:**

```go
// Enable profiling endpoints in your app
coral.EnableProfiling(coral.ProfilingOptions{
    Types: []string{"cpu", "heap", "goroutine", "mutex"},
    Port: 6060,  // Default pprof port
})

// Or use standard Go pprof
import _ "net/http/pprof"
```

**Control from CLI:**

```bash
# Start CPU profile for 60 seconds
coral profile start api --type cpu --duration 60s

# Start heap profile
coral profile start api --type heap

# Start goroutine profile
coral profile start api --type goroutine

# Stop active profile
coral profile stop api

# Download profile for analysis
coral profile download api --output api-cpu.pprof

# Analyze with pprof
go tool pprof api-cpu.pprof
```

**AI-assisted profiling:**

```bash
# Ask Coral to profile automatically
$ coral ask "Why is API slow?"

Coral: "API response time degraded from 50ms → 200ms over last hour.
       Memory usage normal. CPU spiked to 80%.

       Recommend CPU profiling to identify bottleneck.

       Start CPU profile for 60s? [y/N]"

You: "y"

Coral: "Profiling API for 60s...

       Analysis:
       • 65% time in JSON serialization
       • 20% time in database queries
       • 15% other

       Bottleneck: JSON encoding (likely large response bodies)
       Recommendation: Enable response compression or reduce payload size"
```

**SDK Implementation:**

```protobuf
service Profiling {
    // Start profiling
    rpc StartProfile(ProfileRequest) returns (ProfileResponse);

    // Stop profiling
    rpc StopProfile(StopProfileRequest) returns (StopProfileResponse);

    // Get profile data
    rpc GetProfile(GetProfileRequest) returns (ProfileData);

    // List available profile types
    rpc ListProfileTypes(ListProfileTypesRequest) returns (ListProfileTypesResponse);
}

message ProfileRequest {
    enum ProfileType {
        CPU = 0;
        HEAP = 1;
        GOROUTINE = 2;
        MUTEX = 3;
        BLOCK = 4;
    }

    ProfileType type = 1;
    google.protobuf.Duration duration = 2;  // For CPU profiling
}
```

### 4. Deployment Rollbacks

**Coordinate rollbacks through Coral:**

```go
// Register rollback handler in your app
coral.OnRollback(func(ctx context.Context, target coral.RollbackTarget) error {
    log.Printf("Rolling back to version %s", target.Version)

    // Custom rollback logic
    // Example: Update symlink, restart process, etc.
    if err := deployVersion(target.Version); err != nil {
        return err
    }

    log.Printf("Rollback complete")
    return nil
})

// Or for Kubernetes apps, SDK coordinates with kubectl
coral.EnableKubernetesRollback(coral.K8sConfig{
    Namespace: "production",
    Deployment: "api",
})
```

**Control from CLI:**

```bash
# Rollback to previous version
coral rollback api

# Rollback to specific version
coral rollback api --to-version v2.2.5

# Dry-run rollback
coral rollback api --dry-run

# Rollback with confirmation
coral rollback api --confirm
```

**AI-assisted rollback:**

```bash
$ coral ask "What's wrong with API?"

Coral: "API crashed with OOM 3 minutes ago (v2.3.0 deployed 10 min ago).

       Root cause: Memory leak in v2.3.0 (95% confidence)
       Evidence: Memory grew 250MB→512MB linearly over 10 minutes

       Recommendation: Rollback to v2.2.5 (stable for 3 days)

       Execute rollback? [y/N]"

You: "y"

Coral: "Rolling back api to v2.2.5...
       ✓ Deployment updated
       ✓ Pods restarting (3/3)
       ✓ Health checks passing
       ✓ Memory stable at 240MB

       Rollback complete. API healthy."
```

**SDK Implementation:**

```protobuf
service Rollback {
    // Execute rollback
    rpc Rollback(RollbackRequest) returns (RollbackResponse);

    // Get rollback status
    rpc GetRollbackStatus(GetRollbackStatusRequest) returns (RollbackStatus);

    // List available rollback targets
    rpc ListRollbackTargets(ListRollbackTargetsRequest) returns (ListRollbackTargetsResponse);
}

message RollbackRequest {
    string target_version = 1;  // Empty = previous version
    bool dry_run = 2;
    google.protobuf.Duration timeout = 3;
}

message RollbackResponse {
    bool success = 1;
    string message = 2;
    RollbackStatus status = 3;
}
```

---

## Enhanced Observability (Bonus Value)

Beyond control capabilities, the SDK provides enhanced observability:

### Version & Build Metadata

```go
coral.Initialize(coral.Config{
    ServiceName: "api",
    Version:     "2.1.0",
    GitCommit:   "abc123def",
    GitBranch:   "main",
    BuildTime:   time.Now(),
    GoVersion:   runtime.Version(),
})
```

### Component-Level Health

```go
// Register custom health checks
coral.RegisterHealthCheck("database", func(ctx context.Context) coral.Health {
    if err := db.Ping(); err != nil {
        return coral.Unhealthy(err.Error())
    }
    return coral.Healthy()
})

coral.RegisterHealthCheck("cache", func(ctx context.Context) coral.Health {
    latency := cache.Ping()
    if latency > 100*time.Millisecond {
        return coral.Degraded(fmt.Sprintf("slow: %v", latency))
    }
    return coral.Healthy()
})
```

### Endpoint Discovery

```go
// Tell agent where to find standard endpoints
coral.Configure(coral.Config{
    Endpoints: coral.Endpoints{
        Metrics: "http://localhost:8080/metrics",      // Prometheus
        Traces:  "http://localhost:9411/traces",       // OpenTelemetry
        Pprof:   "http://localhost:6060/debug/pprof",  // Go profiling
        Health:  "http://localhost:8080/healthz",      // HTTP health
    },
})
```

---

## Feature Comparison: Passive vs SDK-Integrated

| Feature | Passive (No SDK) | SDK-Integrated |
|---------|------------------|----------------|
| **Feature Flags** | ❌ Not available | ✅ Toggle remotely, gradual rollouts |
| **Traffic Inspection** | ❌ Not available | ✅ Sample & inspect live requests |
| **Profiling** | ❌ Manual (SSH required) | ✅ Remote triggers (CPU, heap, goroutine) |
| **Rollbacks** | ❌ Manual (kubectl/SSH) | ✅ One-command coordinated rollback |
| **Version Detection** | ⚠️ Best-effort (env vars, labels) | ✅ Accurate (from binary, git commit) |
| **Health Status** | ⚠️ Binary (up/down) | ✅ Component-level (DB, cache, degraded states) |
| **Metrics Discovery** | ⚠️ Guessing/probing | ✅ Explicit configuration |
| **Build Metadata** | ❌ Not available | ✅ Git commit, build time, Go version |
| **Deployment Events** | ⚠️ Inferred from restarts | ✅ Explicit version changes |

---

## Integration Examples

### Go (Full SDK)

```go
package main

import (
    "context"
    "log"
    "net/http"
    coral "github.com/coral-io/coral-go"
)

func main() {
    // Initialize Coral
    if err := coral.Initialize(coral.Config{
        ServiceName: "api",
        Version:     "2.1.0",
        GitCommit:   "abc123",
        Environment: "production",
    }); err != nil {
        log.Fatal(err)
    }
    defer coral.Shutdown()

    // Enable control capabilities
    coral.EnableTrafficSampling(coral.TrafficOptions{SampleRate: 0.1})
    coral.EnableProfiling()
    coral.OnRollback(handleRollback)

    // Register health checks
    coral.RegisterHealthCheck("database", checkDatabase)
    coral.RegisterHealthCheck("cache", checkCache)

    // Feature flags
    http.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {
        if coral.IsEnabled("new-checkout") {
            newCheckoutHandler(w, r)
        } else {
            legacyCheckoutHandler(w, r)
        }
    })

    // Traffic middleware
    http.ListenAndServe(":8080", coral.TrafficMiddleware(http.DefaultServeMux))
}

func handleRollback(ctx context.Context, target coral.RollbackTarget) error {
    log.Printf("Rolling back to %s", target.Version)
    return deployVersion(target.Version)
}

func checkDatabase(ctx context.Context) coral.Health {
    if err := db.Ping(); err != nil {
        return coral.Unhealthy(err.Error())
    }
    return coral.Healthy()
}
```

### Python (Planned)

```python
import coral

# Initialize
coral.initialize(
    service_name="api",
    version="2.1.0"
)

# Feature flags
@app.route("/checkout")
def checkout():
    if coral.is_enabled("new-checkout"):
        return new_checkout()
    else:
        return legacy_checkout()

# Traffic sampling
app.wsgi_app = coral.TrafficMiddleware(app.wsgi_app, sample_rate=0.1)

# Health checks
@coral.health_check("database")
def check_db():
    db.ping()
    return coral.Healthy()
```

### Java (Planned)

```java
import io.coral.sdk.Coral;
import io.coral.sdk.FeatureFlags;

public class Application {
    public static void main(String[] args) {
        // Initialize
        Coral.initialize(Config.builder()
            .serviceName("api")
            .version("2.1.0")
            .build());

        // Feature flags
        if (FeatureFlags.isEnabled("new-checkout")) {
            useNewCheckout();
        } else {
            useLegacyCheckout();
        }

        // Start app
        SpringApplication.run(Application.class, args);
    }
}
```

---

## gRPC Interface Specification

The SDK implements the following gRPC services:

```protobuf
syntax = "proto3";
package coral.sdk.v1;

// Main Coral SDK service
service CoralSDK {
    // Core observability (always available)
    rpc GetHealth(HealthRequest) returns (HealthResponse);
    rpc GetInfo(InfoRequest) returns (InfoResponse);
    rpc GetConfig(ConfigRequest) returns (ConfigResponse);

    // Control capabilities (SDK-integrated mode)
    rpc GetFeatureFlags(FlagRequest) returns (FlagResponse);
    rpc UpdateFeatureFlag(UpdateFlagRequest) returns (UpdateFlagResponse);

    rpc ConfigureTrafficSampling(SamplingConfig) returns (SamplingResponse);
    rpc StreamTrafficSamples(StreamRequest) returns (stream TrafficSample);

    rpc StartProfile(ProfileRequest) returns (ProfileResponse);
    rpc StopProfile(StopProfileRequest) returns (StopProfileResponse);
    rpc GetProfile(GetProfileRequest) returns (ProfileData);

    rpc ExecuteRollback(RollbackRequest) returns (RollbackResponse);
    rpc GetRollbackStatus(GetRollbackStatusRequest) returns (RollbackStatus);
}
```

Full protobuf definitions: [See protobuf/](../protobuf/)

---

## Language Support

**Official SDKs:**
- **Go** (v1.0) - Full support (all control capabilities)
- **Python** (planned) - Full support
- **Java** (planned) - Full support

**Community SDKs:**
- Node.js, Rust, Ruby, C# - Community-maintained
- Any language can implement gRPC interface directly

---

## Migration Path

### Phase 1: Start Passive (Day 1)
- Deploy Coral agents
- Apps work without changes
- Get observability + AI debugging immediately

### Phase 2: Identify Control Needs (Week 1)
- Which apps need feature flags?
- Which apps have difficult rollbacks?
- Which apps need production debugging?

### Phase 3: Integrate SDK (Week 2+)
- Add SDK to apps that need control (5-30 min per app)
- Enable feature flags for gradual rollouts
- Enable traffic inspection for debugging
- Enable remote profiling for performance issues

### Phase 4: Unified Operations (Month 2+)
- Control all operations from Coral CLI
- AI-assisted debugging and remediation
- One interface for observe → debug → control

---

## Best Practices

### Feature Flags
- Start with simple boolean flags
- Use gradual rollouts for risky features
- Always provide fallback behavior
- Clean up old flags after full rollout

### Traffic Inspection
- Use low sample rates in production (1-10%)
- Filter sensitive data (passwords, tokens)
- Set max body size limits
- Auto-expire old samples

### Profiling
- Profile for limited duration (30-60s)
- Avoid profiling under heavy load
- Store profiles for later analysis
- Use AI recommendations for when to profile

### Rollbacks
- Test rollback handlers in staging
- Set reasonable timeouts
- Monitor health after rollback
- Keep rollback targets (previous N versions)

---

## Troubleshooting

### SDK not detected

```bash
$ coral status api
⚠ No SDK detected - using passive observation
```

**Check:**
1. Is SDK initialized in your code?
2. Is gRPC port 6000 accessible?
3. Check logs: `coral logs api`

### Feature flags not working

**Check:**
1. Is SDK initialized before flag checks?
2. Are flags registered?
3. Check flag state: `coral flags list --service api`

### Traffic sampling performance impact

Traffic sampling is designed for <1% overhead:
- Sample rate limits impact
- Async capture (non-blocking)
- Circular buffer (bounded memory)
- Auto-sampling (throttle under load)

---

## Next Steps

- [Examples](./EXAMPLES.md) - Real-world use cases
- [Implementation Guide](./IMPLEMENTATION.md) - Technical details
- [API Reference](./API.md) - Full SDK API documentation
