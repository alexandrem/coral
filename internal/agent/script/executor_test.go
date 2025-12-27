package script

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutor_DeployScript(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	config := &Config{
		Enabled:        true,
		DenoPath:       "/usr/local/bin/deno",
		MaxConcurrent:  5,
		MemoryLimitMB:  512,
		TimeoutSeconds: 30,
		WorkDir:        t.TempDir(),
	}

	executor := NewExecutor(config, logger)
	require.NotNil(t, executor)

	// Create a simple test script
	script := &Script{
		ID:      "test-script-1",
		Name:    "Hello World",
		Code:    `console.log("Hello from Coral!");`,
		Trigger: TriggerManual,
	}

	execution, err := executor.DeployScript(ctx, script)
	require.NoError(t, err)
	require.NotNil(t, execution)

	assert.Equal(t, "test-script-1", execution.ScriptID)
	assert.Equal(t, "Hello World", execution.ScriptName)
	assert.Equal(t, StatusPending, execution.Status)
}

func TestExecutor_ConcurrencyLimit(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	config := &Config{
		Enabled:        true,
		DenoPath:       "/usr/local/bin/deno",
		MaxConcurrent:  2, // Only allow 2 concurrent scripts
		MemoryLimitMB:  512,
		TimeoutSeconds: 30,
		WorkDir:        t.TempDir(),
	}

	executor := NewExecutor(config, logger)

	// Deploy first script
	script1 := &Script{
		ID:      "script-1",
		Name:    "Script 1",
		Code:    `await new Promise(r => setTimeout(r, 1000));`,
		Trigger: TriggerManual,
	}
	_, err := executor.DeployScript(ctx, script1)
	require.NoError(t, err)

	// Deploy second script
	script2 := &Script{
		ID:      "script-2",
		Name:    "Script 2",
		Code:    `await new Promise(r => setTimeout(r, 1000));`,
		Trigger: TriggerManual,
	}
	_, err = executor.DeployScript(ctx, script2)
	require.NoError(t, err)

	// Third script should fail due to concurrency limit
	script3 := &Script{
		ID:      "script-3",
		Name:    "Script 3",
		Code:    `console.log("Should not run");`,
		Trigger: TriggerManual,
	}
	_, err = executor.DeployScript(ctx, script3)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "maximum concurrent executions")
}

func TestExecutor_StopScript(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	config := &Config{
		Enabled:        true,
		DenoPath:       "/usr/local/bin/deno",
		MaxConcurrent:  5,
		MemoryLimitMB:  512,
		TimeoutSeconds: 30,
		WorkDir:        t.TempDir(),
	}

	executor := NewExecutor(config, logger)

	// Deploy a long-running script
	script := &Script{
		ID:      "long-running",
		Name:    "Long Running",
		Code:    `while (true) { await new Promise(r => setTimeout(r, 100)); }`,
		Trigger: TriggerManual,
	}

	_, err := executor.DeployScript(ctx, script)
	require.NoError(t, err)

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Check if the script is actually running (skip test if Deno not available)
	execution, err := executor.GetExecution(script.ID)
	require.NoError(t, err)
	if execution.Status != StatusRunning {
		t.Skipf("Script failed to start (status: %s, error: %s) - Deno may not be available", execution.Status, execution.Error)
	}

	// Stop the script
	err = executor.StopScript(ctx, script.ID)
	assert.NoError(t, err)

	// Verify it stopped
	execution, err = executor.GetExecution(script.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusStopped, execution.Status)
}

func TestExecutor_GetExecution(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	config := &Config{
		Enabled:        true,
		DenoPath:       "/usr/local/bin/deno",
		MaxConcurrent:  5,
		MemoryLimitMB:  512,
		TimeoutSeconds: 30,
		WorkDir:        t.TempDir(),
	}

	executor := NewExecutor(config, logger)

	script := &Script{
		ID:      "test-get",
		Name:    "Test Get",
		Code:    `console.log("test");`,
		Trigger: TriggerManual,
	}

	_, err := executor.DeployScript(ctx, script)
	require.NoError(t, err)

	// Get execution
	execution, err := executor.GetExecution("test-get")
	require.NoError(t, err)
	assert.Equal(t, "test-get", execution.ScriptID)
}

func TestExecutor_ListExecutions(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	config := &Config{
		Enabled:        true,
		DenoPath:       "/usr/local/bin/deno",
		MaxConcurrent:  5,
		MemoryLimitMB:  512,
		TimeoutSeconds: 30,
		WorkDir:        t.TempDir(),
	}

	executor := NewExecutor(config, logger)

	// Deploy multiple scripts
	for i := 1; i <= 3; i++ {
		script := &Script{
			ID:      "script-" + string(rune(i)),
			Name:    "Script " + string(rune(i)),
			Code:    `console.log("test");`,
			Trigger: TriggerManual,
		}
		_, err := executor.DeployScript(ctx, script)
		require.NoError(t, err)
	}

	// List all executions
	executions := executor.ListExecutions()
	assert.Len(t, executions, 3)
}

func TestExecution_MarshalJSON(t *testing.T) {
	execution := &Execution{
		ID:         "exec-1",
		ScriptID:   "script-1",
		ScriptName: "Test Script",
		Status:     StatusRunning,
		StartedAt:  time.Now(),
		ExitCode:   0,
		Stdout:     []byte("output"),
		Stderr:     []byte("error"),
		Events:     []Event{},
	}

	data, err := execution.MarshalJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), "exec-1")
	assert.Contains(t, string(data), "script-1")
	assert.Contains(t, string(data), "running")
}

func TestExecution_IsRunning(t *testing.T) {
	execution := &Execution{
		Status: StatusRunning,
	}
	assert.True(t, execution.IsRunning())

	execution.Status = StatusCompleted
	assert.False(t, execution.IsRunning())
}

func TestExecution_GetStatus(t *testing.T) {
	execution := &Execution{
		Status: StatusRunning,
	}
	assert.Equal(t, StatusRunning, execution.GetStatus())
}

func TestExecution_AddEvent(t *testing.T) {
	execution := &Execution{
		Events: make([]Event, 0),
	}

	event := Event{
		Name: "test-event",
		Data: map[string]interface{}{
			"key": "value",
		},
		Timestamp: time.Now(),
		Severity:  "info",
	}

	execution.AddEvent(event)
	assert.Len(t, execution.Events, 1)
	assert.Equal(t, "test-event", execution.Events[0].Name)
}
