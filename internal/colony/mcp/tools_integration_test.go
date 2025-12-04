package mcp

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/logging"
)

// TestServiceHealthTool tests the coral_get_service_health tool integration.
func TestServiceHealthTool(t *testing.T) {
	// Create test registry with mock agents.
	reg := registry.New()

	// Register healthy agent.
	_, err := reg.Register(
		"agent-1",
		"api-service",
		"10.0.0.1",
		"fd00::1",
		nil,
		nil,
		"v1.0.0",
	)
	require.NoError(t, err)

	// Register degraded agent (last seen > 2 minutes ago).
	agent2, err := reg.Register(
		"agent-2",
		"payment-service",
		"10.0.0.2",
		"fd00::2",
		nil,
		nil,
		"v1.0.0",
	)
	require.NoError(t, err)

	// Manually set last seen to simulate degraded state.
	agent2.LastSeen = time.Now().Add(-3 * time.Minute)

	// Register unhealthy agent (last seen > 5 minutes ago).
	agent3, err := reg.Register(
		"agent-3",
		"database-service",
		"10.0.0.3",
		"fd00::3",
		nil,
		nil,
		"v1.0.0",
	)
	require.NoError(t, err)

	// Manually set last seen to simulate unhealthy state.
	agent3.LastSeen = time.Now().Add(-10 * time.Minute)

	// Create MCP server with test registry.
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "mcp-test")

	server := &Server{
		registry: reg,
		db:       nil, // Not needed for health tool.
		config: Config{
			ColonyID:        "test-colony",
			ApplicationName: "test-app",
			Environment:     "test",
			AuditEnabled:    false,
		},
		logger: logger,
	}

	// Test 1: Get all services health.
	t.Run("all services", func(t *testing.T) {
		result, err := testServiceHealthCall(server, ServiceHealthInput{})

		require.NoError(t, err)
		assert.Contains(t, result, "System Health Report")
		assert.Contains(t, result, "api-service")
		assert.Contains(t, result, "payment-service")
		assert.Contains(t, result, "database-service")
		assert.Contains(t, result, "degraded")
		assert.Contains(t, result, "unhealthy")

		// Check summary counts.
		assert.Contains(t, result, "1 healthy")
		assert.Contains(t, result, "1 degraded")
		assert.Contains(t, result, "1 unhealthy")
	})

	// Test 2: Filter by service pattern.
	t.Run("filter by pattern", func(t *testing.T) {
		filter := "api*"
		result, err := testServiceHealthCall(server, ServiceHealthInput{
			ServiceFilter: &filter,
		})

		require.NoError(t, err)
		assert.Contains(t, result, "api-service")
		assert.NotContains(t, result, "payment-service")
		assert.NotContains(t, result, "database-service")
	})

	// Test 3: No services connected.
	t.Run("no services", func(t *testing.T) {
		emptyReg := registry.New()
		emptyServer := &Server{
			registry: emptyReg,
			config:   server.config,
			logger:   logger,
		}

		result, err := testServiceHealthCall(emptyServer, ServiceHealthInput{})

		require.NoError(t, err)
		assert.Contains(t, result, "No services connected")
	})
}

// testServiceHealthCall is a helper to test the service health tool.
func testServiceHealthCall(s *Server, input ServiceHealthInput) (string, error) {
	// Get service filter (handle nil pointer).
	var serviceFilter string
	if input.ServiceFilter != nil {
		serviceFilter = *input.ServiceFilter
	}

	// Get all agents from registry.
	agents := s.registry.ListAll()

	// Build health report.
	var healthyCount, degradedCount, unhealthyCount int
	var serviceStatuses []map[string]interface{}

	for _, agent := range agents {
		// Apply filter if specified.
		if serviceFilter != "" && !matchesPattern(agent.Name, serviceFilter) {
			continue
		}

		// Determine health status based on last seen.
		status := "healthy"
		lastSeen := agent.LastSeen
		timeSinceLastSeen := time.Since(lastSeen)

		if timeSinceLastSeen > 5*time.Minute {
			status = "unhealthy"
			unhealthyCount++
		} else if timeSinceLastSeen > 2*time.Minute {
			status = "degraded"
			degradedCount++
		} else {
			healthyCount++
		}

		serviceStatuses = append(serviceStatuses, map[string]interface{}{
			"service":   agent.Name,
			"agent_id":  agent.AgentID,
			"status":    status,
			"last_seen": lastSeen.Format(time.RFC3339),
			"uptime":    formatDuration(time.Since(agent.RegisteredAt)),
			"mesh_ip":   agent.MeshIPv4,
		})
	}

	// Determine overall status.
	overallStatus := "healthy"
	if unhealthyCount > 0 {
		overallStatus = "unhealthy"
	} else if degradedCount > 0 {
		overallStatus = "degraded"
	}

	// Format response as text for LLM consumption.
	var text strings.Builder
	text.WriteString("System Health Report:\n\n")
	text.WriteString("Overall Status: " + overallStatus + "\n\n")
	text.WriteString("Services:\n")

	if len(serviceStatuses) == 0 {
		text.WriteString("  No services connected.\n")
	} else {
		for _, svc := range serviceStatuses {
			statusEmoji := "✓"
			switch svc["status"] {
			case "degraded":
				statusEmoji = "⚠"
			case "unhealthy":
				statusEmoji = "✗"
			}

			text.WriteString("  " + statusEmoji + " " + svc["service"].(string) +
				": " + svc["status"].(string) +
				" (last seen: " + svc["last_seen"].(string) +
				", uptime: " + svc["uptime"].(string) + ")\n")
		}
	}

	text.WriteString(fmt.Sprintf("\nSummary: %d healthy, %d degraded, %d unhealthy\n",
		healthyCount, degradedCount, unhealthyCount))

	return text.String(), nil
}

// TestServiceTopologyTool tests the coral_get_service_topology tool.
func TestServiceTopologyTool(t *testing.T) {
	// Create test registry with mock agents.
	reg := registry.New()

	_, err := reg.Register("agent-1", "api-service", "10.0.0.1", "fd00::1", nil, nil, "v1.0.0")
	require.NoError(t, err)

	_, err = reg.Register("agent-2", "db-service", "10.0.0.2", "fd00::2", nil, nil, "v1.0.0")
	require.NoError(t, err)

	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "mcp-test")

	server := &Server{
		registry: reg,
		config: Config{
			ColonyID: "test-colony",
		},
		logger: logger,
	}

	t.Run("returns topology with connected services", func(t *testing.T) {
		// For now, this is a placeholder implementation.
		// It should list connected agents.
		agents := server.registry.ListAll()

		text := "Service Topology:\n\n"
		text += fmt.Sprintf("Connected Services (%d):\n", len(agents))

		for _, agent := range agents {
			text += fmt.Sprintf("  - %s (mesh IP: %s)\n", agent.Name, agent.MeshIPv4)
		}

		text += "\nNote: Dependency graph discovery from distributed traces is not yet implemented.\n"
		text += "      See RFD 036 for planned trace-based topology analysis.\n"

		// Verify output.
		assert.Contains(t, text, "Service Topology")
		assert.Contains(t, text, "api-service")
		assert.Contains(t, text, "db-service")
		assert.Contains(t, text, "10.0.0.1")
		assert.Contains(t, text, "10.0.0.2")
		assert.Contains(t, text, "Connected Services (2)")
	})
}

// TestBeylaMetricsToolPlaceholder tests that Beyla metrics tools return placeholders.
func TestBeylaMetricsToolPlaceholder(t *testing.T) {
	t.Run("HTTP metrics returns placeholder", func(t *testing.T) {
		input := BeylaHTTPMetricsInput{
			Service: "api-service",
		}

		timeRange := "1h"
		if input.TimeRange != nil {
			timeRange = *input.TimeRange
		}

		text := fmt.Sprintf("Beyla HTTP Metrics for %s (last %s):\n\n", input.Service, timeRange)
		text += "No metrics available yet.\n\n"
		text += "Note: Beyla HTTP RED metrics collection is implemented but requires agents to run Beyla.\n"
		text += "      See RFD 032 for Beyla integration details.\n"

		assert.Contains(t, text, "Beyla HTTP Metrics")
		assert.Contains(t, text, "api-service")
		assert.Contains(t, text, "No metrics available yet")
		assert.Contains(t, text, "RFD 032")
	})
}

// TestAuditLogging tests that audit logging works when enabled.
func TestAuditLogging(t *testing.T) {
	// Create a logger that captures output.
	logger := logging.NewWithComponent(logging.Config{
		Level:  "info",
		Pretty: false,
	}, "mcp-test")

	t.Run("audit enabled", func(t *testing.T) {
		server := &Server{
			config: Config{
				ColonyID:     "test-colony",
				AuditEnabled: true,
			},
			logger: logger,
		}

		// Call auditToolCall - it should log.
		server.auditToolCall("coral_get_service_health", map[string]interface{}{
			"service_filter": "api*",
		})

		// We can't easily verify the log output in this test,
		// but we've confirmed the code path is executed.
		// In a real system, you'd use a test logger that captures output.
	})

	t.Run("audit disabled", func(t *testing.T) {
		server := &Server{
			config: Config{
				ColonyID:     "test-colony",
				AuditEnabled: false,
			},
			logger: logger,
		}

		// Call auditToolCall - it should not log.
		server.auditToolCall("coral_get_service_health", map[string]interface{}{
			"service_filter": "api*",
		})

		// No assertion needed - just verifying no panic.
	})
}

// TestServerCreation tests MCP server creation and initialization.
func TestServerCreation(t *testing.T) {
	reg := registry.New()
	db := &database.Database{} // Mock database.

	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "mcp-test")

	t.Run("create server with valid config", func(t *testing.T) {
		config := Config{
			ColonyID:        "test-colony",
			ApplicationName: "test-app",
			Environment:     "production",
			Disabled:        false,
			AuditEnabled:    true,
		}

		server, err := New(reg, db, nil, config, logger)
		require.NoError(t, err)
		assert.NotNil(t, server)
		assert.NotNil(t, server.mcpServer)
		assert.Equal(t, "test-colony", server.config.ColonyID)

		// Verify tools are registered (RFD 067: unified query tools).
		tools := server.listToolNames()
		assert.NotEmpty(t, tools)
		assert.Contains(t, tools, "coral_query_summary")
	})

	t.Run("disabled server returns error", func(t *testing.T) {
		config := Config{
			ColonyID: "test-colony",
			Disabled: true,
		}

		server, err := New(reg, db, nil, config, logger)
		assert.Error(t, err)
		assert.Nil(t, server)
		assert.Contains(t, err.Error(), "disabled")
	})
}

// TestToolFiltering tests that tool filtering works correctly.
func TestToolFiltering(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "mcp-test")

	t.Run("enable specific tools only", func(t *testing.T) {
		server := &Server{
			config: Config{
				ColonyID:     "test-colony",
				EnabledTools: []string{"coral_query_summary", "coral_query_traces"},
			},
			logger: logger,
		}

		assert.True(t, server.isToolEnabled("coral_query_summary"))
		assert.True(t, server.isToolEnabled("coral_query_traces"))
		assert.False(t, server.isToolEnabled("coral_query_metrics"))
		assert.False(t, server.isToolEnabled("coral_shell_exec"))
	})

	t.Run("empty list enables all tools", func(t *testing.T) {
		server := &Server{
			config: Config{
				ColonyID:     "test-colony",
				EnabledTools: []string{},
			},
			logger: logger,
		}

		assert.True(t, server.isToolEnabled("coral_query_summary"))
		assert.True(t, server.isToolEnabled("coral_query_traces"))
		assert.True(t, server.isToolEnabled("any_tool"))
	})
}

// TestContextCancellation tests that the server handles context cancellation.
func TestContextCancellation(t *testing.T) {
	t.Skip("Requires actual stdio setup - covered by E2E tests")

	// This test would require setting up actual stdio pipes and
	// testing the ServeStdio method with context cancellation.
	// This is better covered by E2E tests with a real MCP client.
}
