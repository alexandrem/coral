package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Config holds the configuration for the script executor.
type Config struct {
	// Enabled indicates whether script execution is enabled.
	Enabled bool

	// DenoPath is the path to the Deno binary.
	DenoPath string

	// MaxConcurrent is the maximum number of concurrent script executions.
	MaxConcurrent int

	// MemoryLimitMB is the memory limit per script in megabytes.
	MemoryLimitMB int

	// TimeoutSeconds is the default timeout for ad-hoc scripts (60s default).
	TimeoutSeconds int

	// DaemonTimeoutSeconds is the maximum timeout for daemon scripts (24h default).
	DaemonTimeoutSeconds int

	// SDKSocketPath is the path to the SDK Unix Domain Socket.
	SDKSocketPath string

	// WorkDir is the directory where scripts are stored.
	WorkDir string

	// CPULimitPercent is the maximum CPU usage across all scripts (10% default).
	CPULimitPercent int

	// TotalMemoryLimitMB is the total memory limit across all scripts (512MB default).
	TotalMemoryLimitMB int
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Enabled:              true,
		DenoPath:             "/usr/local/bin/deno",
		MaxConcurrent:        5,
		MemoryLimitMB:        128, // Per-script limit
		TimeoutSeconds:       60,  // Ad-hoc default: 60s
		DaemonTimeoutSeconds: 86400, // Daemon max: 24 hours
		SDKSocketPath:        DefaultSocketPath,
		WorkDir:              "/var/lib/coral/scripts",
		CPULimitPercent:      10,  // Max 10% CPU across all scripts
		TotalMemoryLimitMB:   512, // Total memory limit
	}
}

// Executor manages the execution of TypeScript scripts via Deno.
type Executor struct {
	config *Config
	logger zerolog.Logger

	mu         sync.RWMutex
	executions map[string]*Execution // scriptID -> Execution

	sdkServer *SDKServer
}

// NewExecutor creates a new script executor.
func NewExecutor(config *Config, logger zerolog.Logger, sdkServer *SDKServer) *Executor {
	if config == nil {
		config = DefaultConfig()
	}

	return &Executor{
		config:     config,
		logger:     logger.With().Str("component", "script-executor").Logger(),
		executions: make(map[string]*Execution),
		sdkServer:  sdkServer,
	}
}

// Start initializes the executor and ensures Deno is available.
func (e *Executor) Start(ctx context.Context) error {
	if !e.config.Enabled {
		e.logger.Info().Msg("Script execution disabled")
		return nil
	}

	// Ensure work directory exists.
	if err := os.MkdirAll(e.config.WorkDir, 0755); err != nil {
		return fmt.Errorf("failed to create work directory: %w", err)
	}

	// Check if Deno is available.
	if err := e.checkDeno(); err != nil {
		e.logger.Warn().Err(err).Msg("Deno not available, script execution will fail")
		// Don't return error - allow agent to start.
	}

	// Start SDK server.
	if e.sdkServer != nil {
		if err := e.sdkServer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start SDK server: %w", err)
		}
	}

	e.logger.Info().
		Str("deno_path", e.config.DenoPath).
		Int("max_concurrent", e.config.MaxConcurrent).
		Str("sdk_socket", e.config.SDKSocketPath).
		Int("adhoc_timeout_sec", e.config.TimeoutSeconds).
		Int("daemon_timeout_sec", e.config.DaemonTimeoutSeconds).
		Msg("Script executor started")

	return nil
}

// Stop stops all running scripts and cleans up resources.
func (e *Executor) Stop(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for scriptID, execution := range e.executions {
		if err := execution.Stop(ctx); err != nil {
			e.logger.Error().
				Err(err).
				Str("script_id", scriptID).
				Msg("Failed to stop script")
		}
	}

	if e.sdkServer != nil {
		if err := e.sdkServer.Stop(ctx); err != nil {
			return fmt.Errorf("failed to stop SDK server: %w", err)
		}
	}

	return nil
}

// checkDeno verifies that Deno is available.
func (e *Executor) checkDeno() error {
	cmd := exec.Command(e.config.DenoPath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("deno not found at %s: %w", e.config.DenoPath, err)
	}

	e.logger.Info().
		Str("version", string(output)).
		Msg("Deno available")

	return nil
}

// DeployScript deploys and starts a script execution.
func (e *Executor) DeployScript(ctx context.Context, script *Script) (*Execution, error) {
	if !e.config.Enabled {
		return nil, errors.New("script execution is disabled")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if script is already running.
	if existing, ok := e.executions[script.ID]; ok {
		if existing.IsRunning() {
			return nil, fmt.Errorf("script %s is already running", script.ID)
		}
		// Remove old execution.
		delete(e.executions, script.ID)
	}

	// Check concurrent execution limit.
	if len(e.executions) >= e.config.MaxConcurrent {
		return nil, fmt.Errorf("maximum concurrent executions reached (%d)", e.config.MaxConcurrent)
	}

	// Create execution.
	execution := NewExecution(script, e.config, e.logger)
	e.executions[script.ID] = execution

	// Start execution in background.
	go func() {
		if err := execution.Start(ctx); err != nil {
			e.logger.Error().
				Err(err).
				Str("script_id", script.ID).
				Msg("Failed to start script execution")
		}
	}()

	return execution, nil
}

// StopScript stops a running script.
func (e *Executor) StopScript(ctx context.Context, scriptID string) error {
	e.mu.RLock()
	execution, ok := e.executions[scriptID]
	e.mu.RUnlock()

	if !ok {
		return fmt.Errorf("script %s not found", scriptID)
	}

	return execution.Stop(ctx)
}

// GetExecution returns the execution status for a script.
func (e *Executor) GetExecution(scriptID string) (*Execution, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	execution, ok := e.executions[scriptID]
	if !ok {
		return nil, fmt.Errorf("script %s not found", scriptID)
	}

	return execution, nil
}

// ListExecutions returns all active executions.
func (e *Executor) ListExecutions() []*Execution {
	e.mu.RLock()
	defer e.mu.RUnlock()

	executions := make([]*Execution, 0, len(e.executions))
	for _, execution := range e.executions {
		executions = append(executions, execution)
	}

	return executions
}

// Script represents a TypeScript script to execute.
type Script struct {
	ID         string
	Name       string
	Code       string
	Trigger    TriggerType
	ScriptType ScriptType
}

// ScriptType defines the execution model for the script.
type ScriptType string

const (
	// ScriptTypeAdhoc is for short-lived diagnostic scripts (default: 60s timeout).
	ScriptTypeAdhoc ScriptType = "adhoc"

	// ScriptTypeDaemon is for long-running monitor scripts (max: 24h timeout).
	ScriptTypeDaemon ScriptType = "daemon"
)

// TriggerType defines how a script is triggered.
type TriggerType string

const (
	TriggerManual   TriggerType = "manual"
	TriggerSchedule TriggerType = "schedule"
	TriggerEvent    TriggerType = "event"
)

// Execution represents a running script execution.
type Execution struct {
	ID            string
	ScriptID      string
	ScriptName    string
	Status        ExecutionStatus
	StartedAt     time.Time
	CompletedAt   *time.Time
	ExitCode      int
	Stdout        []byte
	Stderr        []byte
	Events        []Event
	Error         string

	mu     sync.RWMutex
	cmd    *exec.Cmd
	cancel context.CancelFunc
	logger zerolog.Logger
	config *Config
	script *Script
}

// ExecutionStatus represents the status of a script execution.
type ExecutionStatus string

const (
	StatusPending   ExecutionStatus = "pending"
	StatusRunning   ExecutionStatus = "running"
	StatusCompleted ExecutionStatus = "completed"
	StatusFailed    ExecutionStatus = "failed"
	StatusStopped   ExecutionStatus = "stopped"
)

// Event represents a custom event emitted by a script.
type Event struct {
	Name      string                 `json:"name"`
	Data      map[string]interface{} `json:"data"`
	Timestamp time.Time              `json:"timestamp"`
	Severity  string                 `json:"severity"`
}

// NewExecution creates a new execution.
func NewExecution(script *Script, config *Config, logger zerolog.Logger) *Execution {
	return &Execution{
		ID:         uuid.New().String(),
		ScriptID:   script.ID,
		ScriptName: script.Name,
		Status:     StatusPending,
		Events:     make([]Event, 0),
		logger:     logger.With().Str("execution_id", uuid.New().String()).Logger(),
		config:     config,
		script:     script,
	}
}

// Start starts the script execution.
func (e *Execution) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.Status == StatusRunning {
		return errors.New("execution already running")
	}

	// Create script file.
	scriptPath := filepath.Join(e.config.WorkDir, e.ScriptID+".ts")
	if err := os.WriteFile(scriptPath, []byte(e.script.Code), 0644); err != nil {
		e.Status = StatusFailed
		e.Error = fmt.Sprintf("failed to write script file: %v", err)
		return fmt.Errorf("failed to write script file: %w", err)
	}

	// Determine timeout based on script type (Dual-TTL model).
	timeout := time.Duration(e.config.TimeoutSeconds) * time.Second
	if e.script.ScriptType == ScriptTypeDaemon {
		timeout = time.Duration(e.config.DaemonTimeoutSeconds) * time.Second
	}

	// Create Deno command with sandboxing.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	e.cancel = cancel

	// Build Deno command with permissions.
	// Only allow read access to the SDK socket, no network or filesystem access.
	args := []string{
		"run",
		"--allow-read=" + e.config.SDKSocketPath, // UDS socket access only
		"--v8-flags=--max-old-space-size=" + fmt.Sprint(e.config.MemoryLimitMB),
		"--no-prompt",
		scriptPath,
	}

	e.cmd = exec.CommandContext(ctx, e.config.DenoPath, args...)

	// Set up environment.
	e.cmd.Env = []string{
		"CORAL_SDK_SOCKET=" + e.config.SDKSocketPath,
		"CORAL_SCRIPT_ID=" + e.ScriptID,
		"CORAL_EXECUTION_ID=" + e.ID,
		"CORAL_SCRIPT_TYPE=" + string(e.script.ScriptType),
	}

	// Capture stdout and stderr.
	stdoutPipe, err := e.cmd.StdoutPipe()
	if err != nil {
		e.Status = StatusFailed
		e.Error = fmt.Sprintf("failed to create stdout pipe: %v", err)
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := e.cmd.StderrPipe()
	if err != nil {
		e.Status = StatusFailed
		e.Error = fmt.Sprintf("failed to create stderr pipe: %v", err)
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command.
	if err := e.cmd.Start(); err != nil {
		e.Status = StatusFailed
		e.Error = fmt.Sprintf("failed to start deno: %v", err)
		return fmt.Errorf("failed to start deno: %w", err)
	}

	e.Status = StatusRunning
	e.StartedAt = time.Now()

	e.logger.Info().
		Str("script_id", e.ScriptID).
		Str("script_name", e.ScriptName).
		Str("script_type", string(e.script.ScriptType)).
		Dur("timeout", timeout).
		Int("memory_limit_mb", e.config.MemoryLimitMB).
		Msg("Script execution started")

	// Read stdout and stderr in background.
	go e.readOutput(stdoutPipe, &e.Stdout)
	go e.readOutput(stderrPipe, &e.Stderr)

	// Wait for completion in background.
	go e.wait(ctx)

	return nil
}

// readOutput reads from a pipe and stores in buffer.
func (e *Execution) readOutput(pipe io.ReadCloser, buffer *[]byte) {
	data, err := io.ReadAll(pipe)
	if err != nil {
		e.logger.Error().Err(err).Msg("Failed to read output")
		return
	}

	e.mu.Lock()
	*buffer = data
	e.mu.Unlock()
}

// wait waits for the command to complete.
func (e *Execution) wait(ctx context.Context) {
	err := e.cmd.Wait()

	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	e.CompletedAt = &now

	if err != nil {
		e.Status = StatusFailed
		e.Error = err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok {
			e.ExitCode = exitErr.ExitCode()
		}
		e.logger.Error().
			Err(err).
			Int("exit_code", e.ExitCode).
			Str("script_id", e.ScriptID).
			Msg("Script execution failed")
	} else {
		e.Status = StatusCompleted
		e.ExitCode = 0
		e.logger.Info().
			Str("script_id", e.ScriptID).
			Str("script_name", e.ScriptName).
			Dur("duration", now.Sub(e.StartedAt)).
			Msg("Script execution completed")
	}

	// Parse events from stdout if any.
	e.parseEvents()
}

// parseEvents parses custom events from stdout.
// Events are emitted as JSON lines prefixed with "CORAL_EVENT:".
func (e *Execution) parseEvents() {
	// TODO: Parse events from stdout.
	// Format: CORAL_EVENT:{"name":"alert","data":{...},"timestamp":"..."}
}

// Stop stops the execution.
func (e *Execution) Stop(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.Status != StatusRunning {
		return nil
	}

	if e.cancel != nil {
		e.cancel()
	}

	if e.cmd != nil && e.cmd.Process != nil {
		if err := e.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
	}

	e.Status = StatusStopped
	now := time.Now()
	e.CompletedAt = &now

	e.logger.Info().
		Str("script_id", e.ScriptID).
		Msg("Script execution stopped")

	return nil
}

// IsRunning returns true if the execution is currently running.
func (e *Execution) IsRunning() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Status == StatusRunning
}

// GetStatus returns the current execution status.
func (e *Execution) GetStatus() ExecutionStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Status
}

// AddEvent adds a custom event to the execution.
func (e *Execution) AddEvent(event Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Events = append(e.Events, event)
}

// MarshalJSON implements json.Marshaler.
func (e *Execution) MarshalJSON() ([]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return json.Marshal(map[string]interface{}{
		"id":           e.ID,
		"script_id":    e.ScriptID,
		"script_name":  e.ScriptName,
		"status":       e.Status,
		"started_at":   e.StartedAt,
		"completed_at": e.CompletedAt,
		"exit_code":    e.ExitCode,
		"stdout":       string(e.Stdout),
		"stderr":       string(e.Stderr),
		"events":       e.Events,
		"error":        e.Error,
	})
}
