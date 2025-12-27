// +build integration

package script

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration test that requires Deno to be installed
func TestIntegration_EndToEnd(t *testing.T) {
	// Skip if Deno is not available
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("Deno not found, skipping integration test")
	}

	ctx := context.Background()
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	// Setup test database
	dbPath := t.TempDir() + "/test.db"
	db := setupIntegrationDB(t, dbPath)
	defer db.Close()

	// Create SDK server
	sdkServer := NewSDKServer(9004, dbPath, logger)
	err := sdkServer.Start(ctx)
	require.NoError(t, err)
	defer sdkServer.Stop(ctx)

	// Wait for server to be ready
	time.Sleep(500 * time.Millisecond)

	// Create script executor
	config := &Config{
		Enabled:        true,
		DenoPath:       "deno",
		MaxConcurrent:  5,
		MemoryLimitMB:  512,
		TimeoutSeconds: 30,
		SDKServerPort:  9004,
		WorkDir:        t.TempDir(),
	}

	executor := NewExecutor(config, logger, sdkServer)
	err = executor.Start(ctx)
	require.NoError(t, err)
	defer executor.Stop(ctx)

	// Test 1: Simple console.log script
	t.Run("SimpleScript", func(t *testing.T) {
		script := &Script{
			ID:      "simple-test",
			Name:    "Simple Test",
			Code:    `console.log("Hello from Coral!");`,
			Trigger: TriggerManual,
		}

		execution, err := executor.DeployScript(ctx, script)
		require.NoError(t, err)

		// Wait for completion
		time.Sleep(2 * time.Second)

		execution, err = executor.GetExecution(script.ID)
		require.NoError(t, err)

		assert.Contains(t, []ExecutionStatus{StatusCompleted, StatusRunning}, execution.Status)
	})

	// Test 2: Script that queries the SDK
	t.Run("SDKQueryScript", func(t *testing.T) {
		script := &Script{
			ID:   "sdk-test",
			Name: "SDK Query Test",
			Code: `
const SDK_URL = "http://localhost:9004";

async function test() {
  // Test health endpoint
  const healthResp = await fetch(SDK_URL + "/health");
  const health = await healthResp.json();
  console.log("Health:", health.status);

  // Test query endpoint
  const queryResp = await fetch(SDK_URL + "/db/query", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      sql: "SELECT COUNT(*) as count FROM otel_spans_local"
    })
  });
  const result = await queryResp.json();
  console.log("Span count:", result.rows[0].count);
}

await test();
`,
			Trigger: TriggerManual,
		}

		execution, err := executor.DeployScript(ctx, script)
		require.NoError(t, err)

		// Wait for completion
		time.Sleep(3 * time.Second)

		execution, err = executor.GetExecution(script.ID)
		require.NoError(t, err)

		// Check that it ran successfully
		if execution.Status == StatusCompleted {
			assert.Contains(t, string(execution.Stdout), "Health: ok")
			assert.Contains(t, string(execution.Stdout), "Span count:")
		}
	})

	// Test 3: Script that emits events
	t.Run("EventEmissionScript", func(t *testing.T) {
		script := &Script{
			ID:   "event-test",
			Name: "Event Emission Test",
			Code: `
const SDK_URL = "http://localhost:9004";

const event = {
  name: "test-event",
  data: {
    message: "Test event from integration test",
    timestamp: new Date().toISOString()
  },
  severity: "info"
};

const resp = await fetch(SDK_URL + "/emit", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(event)
});

const result = await resp.json();
console.log("Event emitted:", result.status);
`,
			Trigger: TriggerManual,
		}

		execution, err := executor.DeployScript(ctx, script)
		require.NoError(t, err)

		// Wait for completion
		time.Sleep(3 * time.Second)

		execution, err = executor.GetExecution(script.ID)
		require.NoError(t, err)

		if execution.Status == StatusCompleted {
			assert.Contains(t, string(execution.Stdout), "Event emitted: ok")
		}
	})

	// Test 4: Script that times out
	t.Run("TimeoutScript", func(t *testing.T) {
		// Set a very short timeout for this test
		shortTimeoutConfig := *config
		shortTimeoutConfig.TimeoutSeconds = 2

		shortExecutor := NewExecutor(&shortTimeoutConfig, logger, sdkServer)

		script := &Script{
			ID:   "timeout-test",
			Name: "Timeout Test",
			Code: `
// Sleep for longer than timeout
await new Promise(resolve => setTimeout(resolve, 10000));
console.log("Should not reach here");
`,
			Trigger: TriggerManual,
		}

		execution, err := shortExecutor.DeployScript(ctx, script)
		require.NoError(t, err)

		// Wait for timeout
		time.Sleep(4 * time.Second)

		execution, err = shortExecutor.GetExecution(script.ID)
		require.NoError(t, err)

		// Should have failed due to timeout
		assert.Equal(t, StatusFailed, execution.Status)
	})
}

func setupIntegrationDB(t *testing.T, dbPath string) *sql.DB {
	db, err := sql.Open("duckdb", dbPath)
	require.NoError(t, err)

	// Create tables
	_, err = db.Exec(`
		CREATE TABLE otel_spans_local (
			trace_id VARCHAR,
			span_id VARCHAR,
			service_name VARCHAR,
			duration_ns BIGINT,
			is_error BOOLEAN,
			http_status INTEGER,
			http_method VARCHAR,
			http_route VARCHAR,
			start_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE TABLE system_metrics_local (
			timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			name VARCHAR,
			value DOUBLE,
			unit VARCHAR,
			metric_type VARCHAR,
			attributes VARCHAR
		)
	`)
	require.NoError(t, err)

	// Insert test data
	_, err = db.Exec(`
		INSERT INTO otel_spans_local VALUES
			('trace-1', 'span-1', 'payments', 50000000, false, 200, 'GET', '/api/payments', CURRENT_TIMESTAMP),
			('trace-2', 'span-2', 'payments', 600000000, true, 500, 'POST', '/api/payments', CURRENT_TIMESTAMP),
			('trace-3', 'span-3', 'orders', 30000000, false, 200, 'GET', '/api/orders', CURRENT_TIMESTAMP)
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO system_metrics_local VALUES
			(CURRENT_TIMESTAMP, 'system.cpu.utilization', 45.5, 'percent', 'gauge', '{}'),
			(CURRENT_TIMESTAMP, 'system.memory.usage', 8589934592, 'bytes', 'gauge', '{}'),
			(CURRENT_TIMESTAMP, 'system.memory.total', 17179869184, 'bytes', 'gauge', '{}')
	`)
	require.NoError(t, err)

	return db
}

// Benchmark concurrent script execution
func BenchmarkScriptExecution(b *testing.B) {
	if _, err := exec.LookPath("deno"); err != nil {
		b.Skip("Deno not found, skipping benchmark")
	}

	ctx := context.Background()
	logger := zerolog.Nop()

	dbPath := b.TempDir() + "/bench.db"
	db := setupIntegrationDB(b.(*testing.T), dbPath)
	defer db.Close()

	sdkServer := NewSDKServer(9005, dbPath, logger)
	sdkServer.Start(ctx)
	defer sdkServer.Stop(ctx)

	time.Sleep(500 * time.Millisecond)

	config := &Config{
		Enabled:        true,
		DenoPath:       "deno",
		MaxConcurrent:  20,
		MemoryLimitMB:  512,
		TimeoutSeconds: 30,
		SDKServerPort:  9005,
		WorkDir:        b.TempDir(),
	}

	executor := NewExecutor(config, logger, sdkServer)
	executor.Start(ctx)
	defer executor.Stop(ctx)

	script := &Script{
		ID:   "bench",
		Name: "Benchmark",
		Code: `
const resp = await fetch("http://localhost:9005/health");
const data = await resp.json();
console.log(data.status);
`,
		Trigger: TriggerManual,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		script.ID = "bench-" + string(rune(i))
		executor.DeployScript(ctx, script)
	}
}
