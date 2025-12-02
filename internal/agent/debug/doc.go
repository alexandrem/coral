// Package debug provides functionality for managing debug sessions within the Coral agent.
// It allows starting and stopping debug sessions, which involve attaching eBPF uprobes
// to specific functions in running services to capture debug events (arguments, return values, etc.).
//
// The core component is the DebugSessionManager, which handles the lifecycle of debug sessions,
// resolves service addresses, queries the SDK for function metadata, and manages the underlying
// eBPF resources.
package debug
