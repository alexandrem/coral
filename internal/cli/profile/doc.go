// Package profile implements on-demand profiling commands for the Coral CLI.
//
// This package provides the 'coral profile' command group, which enables
// developers to collect performance profiles from running services on-demand.
// Unlike the 'coral query' commands that retrieve historical profiling data,
// these commands actively initiate new profiling sessions with configurable
// duration and sampling parameters.
//
// # Command Structure
//
// The package follows a hierarchical command structure:
//
//	coral profile <type> [flags]
//
// Where <type> can be:
//   - cpu: Statistical CPU sampling to identify compute hotspots
//   - memory: Heap allocation profiling to track memory usage (RFD 077)
//
// # CPU Profiling
//
// CPU profiling uses eBPF perf_event sampling to capture stack traces at a
// specified frequency (default 99Hz). The output can be used to generate
// flame graphs showing where CPU time is being spent:
//
//	coral profile cpu --service api --duration 30
//	coral profile cpu --service api --duration 30 | flamegraph.pl > cpu.svg
//
// Key features:
//   - Configurable sampling frequency (1-1000 Hz)
//   - Configurable duration (1-300 seconds)
//   - Multiple output formats (folded stacks, JSON)
//   - Low overhead (<5% CPU during profiling)
//
// # Memory Profiling
//
// Memory profiling tracks heap allocations to identify memory usage patterns
// and potential leaks. This feature is planned for RFD 077 and currently
// returns a stub implementation:
//
//	coral profile memory --service api --duration 30
//
// # Output Formats
//
// All profiling commands support multiple output formats:
//
//   - folded: Folded stack format compatible with flamegraph.pl (default)
//   - json: JSON format for programmatic processing
//
// Progress messages and metadata are written to stderr, while profile data
// is written to stdout, enabling easy piping to visualization tools.
//
// # Architecture
//
// The profiling workflow follows this sequence:
//
//  1. CLI sends ProfileCPURequest to Colony via gRPC
//  2. Colony identifies the target agent and forwards the request
//  3. Agent collects profile samples using eBPF or SDK endpoints
//  4. Agent returns aggregated stack traces to Colony
//  5. Colony returns profile data to CLI
//  6. CLI formats and outputs the profile
//
// # Related Commands
//
// For querying historical profiling data collected by continuous profiling:
//   - coral query cpu-profile --service api --since 1h
//   - coral query memory-profile --service api --since 1h
//
// For function-level debugging and instrumentation:
//   - coral debug attach --service api --function processOrder
//   - coral debug profile --service api --query "database"
//
// # RFD References
//
//   - RFD 070: On-demand CPU profiling
//   - RFD 072: Continuous profiling and historical queries
//   - RFD 077: Memory profiling (in progress)
package profile
