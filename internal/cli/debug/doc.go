// Package debug provides CLI commands for debugging running services.
//
// The debug package implements commands for attaching to and inspecting
// running services using eBPF uprobes and other instrumentation techniques.
// It supports real-time event streaming, function tracing, call tree
// visualization, and debug session management through the Colony coordinator.
//
// Main commands include:
//   - attach: Attach debugger to specific functions in running services
//   - detach: Detach from active debug sessions
//   - list: List all debug sessions
//   - events: Stream or query events from debug sessions
//   - trace: Trace function calls and execution flow
//   - query: Query debug data with flexible filtering
//   - tree: Visualize call trees and execution hierarchies
//
// Debug sessions are managed by the Colony and can target services either
// through SDK integration or agentless binary scanning.
package debug
