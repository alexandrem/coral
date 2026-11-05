package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/logging"
)

// ContainerHandler implements the container exec RPC methods for the agent (RFD 056).
type ContainerHandler struct {
	logger logging.Logger

	// procRoot is the procfs mount point, overridable in tests.
	procRoot string

	// selfPID is the agent's own PID, never selected as a target.
	selfPID int
}

// NewContainerHandler creates a new container handler.
func NewContainerHandler(logger logging.Logger) *ContainerHandler {
	return &ContainerHandler{
		logger:   logger,
		procRoot: "/proc",
		selfPID:  os.Getpid(),
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

// containerIDPattern matches the 64-hex container IDs that Docker,
// containerd, CRI-O and Podman embed in cgroup paths, e.g.
// "/docker/<id>", "/system.slice/docker-<id>.scope" or
// "/kubepods/.../cri-containerd-<id>.scope".
var containerIDPattern = regexp.MustCompile(`[0-9a-f]{64}`)

// Container selector prefixes accepted in ContainerExecRequest.ContainerName.
const (
	selectorPID = "pid:"
	selectorID  = "id:"
)

// detectContainerPID finds the PID whose namespaces a container exec enters.
// Works in:
// - Docker-compose sidecar: shared PID namespace with app container
// - K8s sidecar: shareProcessNamespace: true
// - K8s DaemonSet: hostPID: true (sees all node containers)
//
// The selector takes one of these forms:
//   - "": the lowest visible PID other than the agent's own. That is the
//     application in sidecar mode, where PID 1 is the application itself. In
//     the host PID namespace the lowest PID is host init, so an empty
//     selector is rejected there.
//   - "pid:<n>": that PID, as seen from the agent's PID namespace.
//   - "id:<hex>": the container whose ID, as found in /proc/<pid>/cgroup,
//     starts with the given prefix.
//   - anything else: an exact process name, matched against /proc/<pid>/comm.
//
// ID and name matches must resolve to a single container. In the host PID
// namespace, a process sharing host init's mount namespace is never
// selected, whatever the form.
func (h *ContainerHandler) detectContainerPID(selector string) (int, error) {
	// Kernel threads are only visible from the initial PID namespace, and
	// PID 2 is always kthreadd there.
	var hostMntNS string
	if comm, ok := h.readComm(2); ok && comm == "kthreadd" {
		var err error
		hostMntNS, err = h.readNamespace(1, "mnt")
		if err != nil {
			return 0, fmt.Errorf("failed to read host mount namespace: %w", err)
		}
	}

	selector = strings.TrimSpace(selector)
	switch {
	case selector == "":
		if hostMntNS != "" {
			return 0, fmt.Errorf("container selector required: agent shares the host PID namespace")
		}
		pids, err := h.listPids()
		if err != nil {
			return 0, err
		}
		for _, pid := range pids {
			if pid != h.selfPID {
				return pid, nil
			}
		}
		return 0, fmt.Errorf("no container PID found")

	case strings.HasPrefix(selector, selectorPID):
		pid, err := strconv.Atoi(strings.TrimPrefix(selector, selectorPID))
		if err != nil || pid <= 0 {
			return 0, fmt.Errorf("invalid container selector %q: expected pid:<positive integer>", selector)
		}
		if pid == h.selfPID {
			return 0, fmt.Errorf("container selector %q is the agent itself", selector)
		}
		mntNS, err := h.readNamespace(pid, "mnt")
		if err != nil {
			return 0, fmt.Errorf("process %d not accessible: %w", pid, err)
		}
		if mntNS == hostMntNS {
			return 0, fmt.Errorf("process %d runs in the host mount namespace, not a container", pid)
		}
		return pid, nil

	case strings.HasPrefix(selector, selectorID):
		prefix := strings.ToLower(strings.TrimPrefix(selector, selectorID))
		if prefix == "" || !isHex(prefix) {
			return 0, fmt.Errorf("invalid container selector %q: expected id:<hex prefix>", selector)
		}
		return h.resolveUnique(selector, hostMntNS, func(pid int) string {
			return h.matchContainerID(pid, prefix)
		})

	default:
		return h.resolveUnique(selector, hostMntNS, func(pid int) string {
			if comm, ok := h.readComm(pid); !ok || comm != selector {
				return ""
			}
			// nsenter needs the same access, so an unreadable namespace
			// cannot be a target anyway.
			mntNS, _ := h.readNamespace(pid, "mnt")
			return mntNS
		})
	}
}

// resolveUnique returns the lowest PID among the visible processes for which
// match returns a non-empty container key. It fails when no process matches
// or the matches span more than one container. With a non-empty hostMntNS,
// processes in that mount namespace, or whose namespace is unreadable, are
// skipped.
func (h *ContainerHandler) resolveUnique(selector, hostMntNS string, match func(pid int) string) (int, error) {
	pids, err := h.listPids()
	if err != nil {
		return 0, err
	}

	// Map each matched container key to its lowest PID.
	matches := make(map[string]int)
	for _, pid := range pids {
		if pid == h.selfPID {
			continue
		}
		key := match(pid)
		if key == "" {
			continue
		}
		if hostMntNS != "" {
			mntNS, err := h.readNamespace(pid, "mnt")
			if err != nil || mntNS == hostMntNS {
				continue
			}
		}
		if _, ok := matches[key]; !ok {
			matches[key] = pid
		}
	}

	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("container %q not found among visible processes", selector)
	case 1:
		for _, pid := range matches {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("container %q is ambiguous: matches %d containers, use id:<container ID> or pid:<n>", selector, len(matches))
}

// listPids returns the PIDs under the handler's proc root in ascending order.
func (h *ContainerHandler) listPids() ([]int, error) {
	entries, err := os.ReadDir(h.procRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", h.procRoot, err)
	}

	var pids []int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if pid, err := strconv.Atoi(entry.Name()); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids, nil
}

// readComm returns the trimmed contents of <proc>/<pid>/comm.
func (h *ContainerHandler) readComm(pid int) (string, bool) {
	//nolint:gosec // G304: path is built from the proc root and a numeric PID.
	data, err := os.ReadFile(filepath.Join(h.procRoot, strconv.Itoa(pid), "comm"))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// readNamespace returns the <proc>/<pid>/ns/<ns> link target, such as
// "mnt:[4026531841]", which identifies the namespace.
func (h *ContainerHandler) readNamespace(pid int, ns string) (string, error) {
	return os.Readlink(filepath.Join(h.procRoot, strconv.Itoa(pid), "ns", ns))
}

// matchContainerID returns the container ID in <proc>/<pid>/cgroup that
// starts with prefix, or "" if there is none.
func (h *ContainerHandler) matchContainerID(pid int, prefix string) string {
	//nolint:gosec // G304: path is built from the proc root and a numeric PID.
	data, err := os.ReadFile(filepath.Join(h.procRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return ""
	}
	for _, id := range containerIDPattern.FindAllString(string(data), -1) {
		if strings.HasPrefix(id, prefix) {
			return id
		}
	}
	return ""
}

// isHex reports whether s consists only of lowercase hex digits.
func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
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
