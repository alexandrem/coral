package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/sys/proc"
)

// ContainerHandler implements the container exec RPC methods for the agent (RFD 056).
type ContainerHandler struct {
	logger logging.Logger
}

// NewContainerHandler creates a new container handler.
func NewContainerHandler(logger logging.Logger) *ContainerHandler {
	return &ContainerHandler{
		logger: logger,
	}
}

// ContainerExec executes a command in a container's namespace using nsenter (RFD 056).
func (h *ContainerHandler) ContainerExec(
	ctx context.Context,
	req *connect.Request[agentv1.ContainerExecRequest],
) (*connect.Response[agentv1.ContainerExecResponse], error) {
	input := req.Msg

	// Validate command.
	if len(input.Command) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("command cannot be empty"))
	}

	// Generate session ID for audit.
	sessionID := uuid.New().String()

	// Determine timeout (default: 30s, max: 300s).
	timeout := time.Duration(input.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if timeout > 300*time.Second {
		timeout = 300 * time.Second
	}

	// Create timeout context.
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Detect container PID.
	containerPID, err := h.detectContainerPID(input.ContainerName)
	if err != nil {
		h.logger.Error().
			Err(err).
			Str("session_id", sessionID).
			Str("container_name", input.ContainerName).
			Msg("Failed to detect container PID")
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("failed to detect container PID: %w", err))
	}

	// Determine namespaces to enter (default: ["mnt"] for filesystem access).
	namespaces := input.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{"mnt"}
	}

	// Validate namespaces.
	validNamespaces := map[string]bool{
		"mnt":    true,
		"pid":    true,
		"net":    true,
		"ipc":    true,
		"uts":    true,
		"cgroup": true,
	}
	for _, ns := range namespaces {
		if !validNamespaces[ns] {
			h.logger.Warn().
				Str("namespace", ns).
				Msg("Ignoring invalid namespace")
		}
	}

	// Log execution start.
	h.logger.Info().
		Str("session_id", sessionID).
		Str("user_id", input.UserId).
		Int32("container_pid", int32(containerPID)).
		Strs("command", input.Command).
		Strs("namespaces", namespaces).
		Uint32("timeout_seconds", input.TimeoutSeconds).
		Msg("Executing container command")

	startTime := time.Now()

	// Build nsenter command.
	nsenterArgs := h.buildNsenterCommand(containerPID, namespaces, input.WorkingDir, input.Command)

	// Create command.
	//nolint:gosec // G204: nsenter with validated arguments from buildNsenterCommand.
	cmd := exec.CommandContext(execCtx, "nsenter", nsenterArgs...)

	// Set environment variables.
	cmd.Env = os.Environ()
	for k, v := range input.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Create buffers for stdout and stderr.
	// Use bytes.Buffer directly instead of pipes to avoid race conditions.
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Start command.
	if err := cmd.Start(); err != nil {
		h.logger.Error().
			Err(err).
			Str("session_id", sessionID).
			Msg("Failed to start nsenter command")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to start nsenter command: %w", err))
	}

	// Wait for command to complete.
	execErr := cmd.Wait()

	// Get output from buffers.
	stdout := stdoutBuf.Bytes()
	stderr := stderrBuf.Bytes()

	duration := time.Since(startTime)
	exitCode := int32(0)
	errorMsg := ""

	if execErr != nil {
		// Check if it's a timeout.
		if execCtx.Err() == context.DeadlineExceeded {
			errorMsg = fmt.Sprintf("command timed out after %s", timeout)
			exitCode = -1
			h.logger.Warn().
				Str("session_id", sessionID).
				Str("error", errorMsg).
				Msg("Container command execution timeout")
		} else {
			// Extract exit code from error.
			if exitError, ok := execErr.(*exec.ExitError); ok {
				exitCode = int32(exitError.ExitCode())
			} else {
				exitCode = -1
				errorMsg = execErr.Error()
			}
			h.logger.Warn().
				Err(execErr).
				Str("session_id", sessionID).
				Int32("exit_code", exitCode).
				Msg("Container command execution failed")
		}
	}

	// Log execution complete.
	h.logger.Info().
		Str("session_id", sessionID).
		Int32("exit_code", exitCode).
		Uint32("duration_ms", uint32(duration.Milliseconds())).
		Int("stdout_bytes", len(stdout)).
		Int("stderr_bytes", len(stderr)).
		Msg("Container command execution completed")

	// Return response.
	resp := &agentv1.ContainerExecResponse{
		Stdout:            stdout,
		Stderr:            stderr,
		ExitCode:          exitCode,
		SessionId:         sessionID,
		DurationMs:        uint32(duration.Milliseconds()),
		Error:             errorMsg,
		ContainerPid:      int32(containerPID),
		NamespacesEntered: namespaces,
	}

	return connect.NewResponse(resp), nil
}

// cgroupMatchesName reports whether any line in cgroupContent contains name
// as a case-sensitive substring.
func cgroupMatchesName(cgroupContent, name string) bool {
	for _, line := range strings.Split(cgroupContent, "\n") {
		if strings.Contains(line, name) {
			return true
		}
	}
	return false
}

// findPidByCgroupName finds the main process PID of the container whose cgroup
// path contains name as a substring. All processes within the same container
// share an identical cgroup file, so they are counted as one match.
//
// Returns CodeNotFound when no container matches and CodeFailedPrecondition
// when the name is ambiguous across multiple containers.
func (h *ContainerHandler) findPidByCgroupName(name string) (int, error) {
	allPids, err := proc.ListPids()
	if err != nil {
		return 0, fmt.Errorf("failed to read /proc: %w", err)
	}

	// Group by cgroup content: all threads/processes in the same container
	// produce an identical /proc/<pid>/cgroup file, so the content is a
	// stable container identity key. Track the lowest PID per container
	// (the init process, which started before any other process).
	containerPIDs := make(map[string]int) // cgroup content -> lowest PID
	for _, pid := range allPids {
		if pid <= 0 || pid == os.Getpid() {
			continue
		}
		content, err := proc.ReadCgroup(pid)
		if err != nil {
			continue // process may have exited or be inaccessible.
		}
		if !cgroupMatchesName(content, name) {
			continue
		}
		key := strings.TrimSpace(content)
		if existing, ok := containerPIDs[key]; !ok || pid < existing {
			containerPIDs[key] = pid
		}
	}

	switch len(containerPIDs) {
	case 0:
		return 0, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("no container matching %q found", name))
	case 1:
		for _, pid := range containerPIDs {
			return pid, nil
		}
	}

	// Multiple distinct containers match — include lowest PIDs as a hint.
	pids := make([]int, 0, len(containerPIDs))
	for _, pid := range containerPIDs {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return 0, connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("ambiguous container name %q: matches %d containers (PIDs: %v); use a more specific name",
			name, len(containerPIDs), pids))
}

// detectContainerPID finds the main container process PID.
// Works in:
// - Docker-compose sidecar: shared PID namespace with app container
// - K8s sidecar: shareProcessNamespace: true
// - K8s DaemonSet: hostPID: true (sees all node containers)
func (h *ContainerHandler) detectContainerPID(containerName string) (int, error) {
	// When a name is provided, use cgroup-based lookup for precise targeting.
	// This supports DaemonSet mode and multi-container pods.
	if containerName != "" {
		return h.findPidByCgroupName(containerName)
	}

	// Unnamed fallback: return the lowest visible PID (sidecar mode).
	// Scan /proc for numeric directories (PIDs).
	allPids, err := proc.ListPids()
	if err != nil {
		return 0, fmt.Errorf("failed to read /proc: %w", err)
	}

	var pids []int
	for _, pid := range allPids {
		// Skip our own process.
		// Note: PID 1 is valid in shared PID namespace (sidecar mode).
		if pid <= 0 || pid == os.Getpid() {
			continue
		}

		pids = append(pids, pid)
	}

	// Sort PIDs (lowest first).
	sort.Ints(pids)

	if len(pids) == 0 {
		return 0, fmt.Errorf("no container PID found")
	}

	// Return lowest PID (container starts before agent in sidecar mode).
	// In sidecar mode, the application container's main process will have
	// a lower PID than the agent's process.
	return pids[0], nil
}

// buildNsenterCommand constructs the nsenter command arguments.
func (h *ContainerHandler) buildNsenterCommand(
	containerPID int,
	namespaces []string,
	workingDir string,
	command []string,
) []string {
	args := []string{
		"-t", strconv.Itoa(containerPID),
	}

	// Map namespace names to nsenter flags.
	nsFlags := map[string]string{
		"mnt":    "-m",
		"pid":    "-p",
		"net":    "-n",
		"ipc":    "-i",
		"uts":    "-u",
		"cgroup": "-C",
	}

	// Add namespace flags.
	for _, ns := range namespaces {
		if flag, ok := nsFlags[ns]; ok {
			args = append(args, flag)
		}
	}

	// Add working directory if specified.
	if workingDir != "" {
		args = append(args, "--wd", workingDir)
	}

	// Add separator.
	args = append(args, "--")

	// Add command and arguments.
	args = append(args, command...)

	return args
}
