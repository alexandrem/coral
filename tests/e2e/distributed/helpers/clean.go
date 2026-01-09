package helpers

import (
	"context"
	"fmt"
	"time"

	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

// CleanupColonyDatabase clears telemetry and service data from colony database.
//
// This function is designed to be called in TearDownSuite to reset the database
// state between test suites. It clears:
//   - Service registry (services, service_heartbeats, service_connections)
//   - Telemetry data (otel_summaries, beyla_*, system_metrics_summaries)
//   - Debug data (debug_sessions, debug_events, functions, function_metrics)
//   - Profiling data (cpu_profile_summaries)
//
// It does NOT clear:
//   - Mesh configuration (agent_ip_allocations)
//   - Security data (issued_certificates, certificate_revocations)
//   - Metadata (binary_metadata_registry, profile_frame_dictionary)
func CleanupColonyDatabase(
	ctx context.Context,
	client colonyv1connect.ColonyServiceClient,
) error {
	// List of tables to clear, in dependency order (child tables first).
	tables := []string{
		// Service-related tables.
		"service_heartbeats",
		"service_connections",
		"services",

		// Telemetry tables.
		"otel_summaries",
		"beyla_http_metrics",
		"beyla_grpc_metrics",
		"beyla_sql_metrics",
		"beyla_traces",
		"system_metrics_summaries",

		// Debug tables.
		"debug_events",
		"debug_sessions",
		"function_metrics",
		"functions",

		// Profiling tables.
		"cpu_profile_summaries",
	}

	for _, table := range tables {
		query := fmt.Sprintf("DELETE FROM %s", table)
		_, err := ExecuteColonyQuery(ctx, client, query, 1)
		if err != nil {
			// Table might not exist yet (early in development), log but don't fail.
			// This makes cleanup more resilient during incremental development.
			continue
		}
	}

	return nil
}

// CleanupAllServices disconnects all known services from all agents.
// This is designed to be called between test phases to ensure clean state.
//
// Common services cleaned up:
//   - cpu-app, otel-app from agent-0
//   - sdk-app from agent-1
func CleanupAllServices(
	ctx context.Context,
	getAgentEndpoint func(ctx context.Context, index int) (string, error),
) error {
	// Disconnect from agent-0.
	agent0Endpoint, err := getAgentEndpoint(ctx, 0)
	if err == nil {
		agent0Client := NewAgentClient(agent0Endpoint)
		_, _ = DisconnectService(ctx, agent0Client, "cpu-app")
		_, _ = DisconnectService(ctx, agent0Client, "otel-app")
		// Ignore errors - services may not be connected.
	}

	// Disconnect from agent-1.
	agent1Endpoint, err := getAgentEndpoint(ctx, 1)
	if err == nil {
		agent1Client := NewAgentClient(agent1Endpoint)
		_, _ = DisconnectService(ctx, agent1Client, "sdk-app")
		// Ignore errors - service may not be connected.
	}

	// Give agents time to process disconnections.
	time.Sleep(2 * time.Second)

	return nil
}
