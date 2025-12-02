// Package sdk provides the Coral SDK for embedding runtime debugging capabilities into Go applications.
//
// The Coral SDK enables distributed applications to be monitored and debugged
// by the Coral mesh without requiring code changes, restarts, or redeployment.
// Applications integrate the SDK through simple initialization calls, which
// automatically expose debugging capabilities to local agents and the Colony
// coordinator.
//
// Key features:
//   - Zero-configuration service registration with local Coral agents
//   - Automatic binary metadata extraction (DWARF symbols, function offsets)
//   - Runtime monitoring via embedded debug server (see pkg/sdk/debug)
//   - Health check endpoint integration
//   - Automatic retry logic for agent connectivity
//   - Support for stripped and unstripped binaries
//
// Basic integration:
//
//	import "github.com/coral-mesh/coral/pkg/sdk"
//
//	func main() {
//	    // Register service with Coral
//	    sdk.RegisterService("my-service", sdk.Options{
//	        Port:           8080,
//	        HealthEndpoint: "/health",
//	        AgentAddr:      "localhost:9091",
//	    })
//
//	    // Enable runtime monitoring (starts debug server and registers with agent)
//	    sdk.EnableRuntimeMonitoring()
//
//	    // Your application code
//	    http.ListenAndServe(":8080", handler)
//	}
//
// Advanced usage with custom configuration:
//
//	sdk, err := sdk.New(sdk.Config{
//	    ServiceName: "my-service",
//	    EnableDebug: true,
//	    Logger:      slog.New(handler),
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer sdk.Close()
//
// The SDK runs a gRPC server that exposes function metadata to agents,
// enabling them to attach eBPF uprobes for distributed tracing and debugging
// without application awareness. All debugging operations happen out-of-band
// in the control plane.
//
// For applications using the SDK, the Colony can:
//   - Discover available functions and their signatures
//   - Attach to function entry/exit points with full type information
//   - Capture arguments and return values at runtime
//   - Build distributed call trees across service boundaries
//
// The SDK automatically handles agent reconnection and graceful shutdown,
// making it suitable for production environments where observability is
// critical but performance overhead must be minimal.
package sdk
