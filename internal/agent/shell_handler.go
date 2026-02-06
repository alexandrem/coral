package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/sys/shell"
)

// ShellHandler implements the shell RPC methods for the agent (RFD 026).
type ShellHandler struct {
	logger   logging.Logger
	sessions map[string]*shell.Session
	mu       sync.RWMutex
}

// NewShellHandler creates a new shell handler.
func NewShellHandler(logger logging.Logger) *ShellHandler {
	return &ShellHandler{
		logger:   logger,
		sessions: make(map[string]*shell.Session),
	}
}

// ShellExec executes a one-off command and returns the output (RFD 045).
func (h *ShellHandler) ShellExec(
	ctx context.Context,
	req *connect.Request[agentv1.ShellExecRequest],
) (*connect.Response[agentv1.ShellExecResponse], error) {
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

	// Log execution start.
	h.logger.Info().
		Str("session_id", sessionID).
		Str("user_id", input.UserId).
		Strs("command", input.Command).
		Uint32("timeout_seconds", input.TimeoutSeconds).
		Msg("Executing shell command")

	startTime := time.Now()

	// Create command.
	//nolint:gosec // G204: Command execution is intentional for shell handler
	cmd := exec.CommandContext(execCtx, input.Command[0], input.Command[1:]...)

	// Set working directory if specified.
	if input.WorkingDir != "" {
		cmd.Dir = input.WorkingDir
	}

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
			Msg("Failed to start command")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to start command: %w", err))
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
				Msg("Command execution timeout")
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
				Msg("Command execution failed")
		}
	}

	// Log execution complete.
	h.logger.Info().
		Str("session_id", sessionID).
		Int32("exit_code", exitCode).
		Uint32("duration_ms", uint32(duration.Milliseconds())).
		Int("stdout_bytes", len(stdout)).
		Int("stderr_bytes", len(stderr)).
		Msg("Shell command execution completed")

	// Return response.
	resp := &agentv1.ShellExecResponse{
		Stdout:     stdout,
		Stderr:     stderr,
		ExitCode:   exitCode,
		SessionId:  sessionID,
		DurationMs: uint32(duration.Milliseconds()),
		Error:      errorMsg,
	}

	return connect.NewResponse(resp), nil
}

// Shell implements the streaming shell RPC (RFD 026).
func (h *ShellHandler) Shell(
	ctx context.Context,
	stream *connect.BidiStream[agentv1.ShellRequest, agentv1.ShellResponse],
) error {
	// Read the first message which should be ShellStart.
	req, err := stream.Receive()
	if err != nil {
		return fmt.Errorf("failed to receive start message: %w", err)
	}

	start := req.GetStart()
	if start == nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("first message must be ShellStart"))
	}

	// Create and start shell session.
	session, err := h.startShellSession(ctx, start)
	if err != nil {
		return fmt.Errorf("failed to start shell session: %w", err)
	}

	h.logger.Info().
		Str("session_id", session.ID).
		Str("user_id", session.UserID).
		Str("shell", start.Shell).
		Msg("Shell session started")

	// Ensure cleanup on exit.
	defer func() {
		h.cleanupSession(session)
		h.logger.Info().
			Str("session_id", session.ID).
			Interface("exit_code", session.ExitCode).
			Msg("Shell session ended")
	}()

	// Start goroutine to stream PTY output to client.
	errCh := make(chan error, 2)
	go func() {
		if err := h.streamOutput(stream, session); err != nil {
			errCh <- fmt.Errorf("output stream error: %w", err)
		}
	}()

	// Process client input.
	go func() {
		if err := h.processInput(stream, session); err != nil {
			errCh <- fmt.Errorf("input stream error: %w", err)
		}
	}()

	// Wait for session to end or error.
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// startShellSession creates and starts a new shell session.
func (h *ShellHandler) startShellSession(
	ctx context.Context,
	start *agentv1.ShellStart,
) (*shell.Session, error) {
	// Prepare config
	cfg := shell.StartConfig{
		Shell:  start.Shell,
		UserID: start.UserId,
		Env:    start.Env,
	}
	if start.Size != nil {
		cfg.Rows = uint16(start.Size.Rows) //nolint:gosec // G115: Terminal dimensions are small values
		cfg.Cols = uint16(start.Size.Cols) //nolint:gosec // G115: Terminal dimensions are small values
	}

	// Start session using shell package
	session, err := shell.Start(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to start shell session: %w", err)
	}

	// Store session.
	h.mu.Lock()
	h.sessions[session.ID] = session
	h.mu.Unlock()

	return session, nil
}

// streamOutput streams PTY output to the client.
func (h *ShellHandler) streamOutput(
	stream *connect.BidiStream[agentv1.ShellRequest, agentv1.ShellResponse],
	session *shell.Session,
) error {
	buf := make([]byte, 4096)
	pty := session.PTY()

	for {
		// Check if PTY is available
		if pty == nil {
			return fmt.Errorf("PTY not available")
		}

		n, err := pty.Read(buf)
		if err != nil {
			// Check if this is a normal exit condition (EOF or EIO).
			isExitError := err == io.EOF
			if !isExitError {
				// Check for syscall.EIO (input/output error from PTY).
				if pathErr, ok := err.(*os.PathError); ok {
					isExitError = pathErr.Err == syscall.EIO
				}
			}

			if isExitError {
				// Process exited, send exit response.
				exitCode := 0
				if session.ExitCode != nil {
					exitCode = *session.ExitCode
				}

				return stream.Send(&agentv1.ShellResponse{
					Payload: &agentv1.ShellResponse_Exit{
						Exit: &agentv1.ShellExit{
							ExitCode:  int32(exitCode),
							SessionId: session.ID,
						},
					},
				})
			}
			return fmt.Errorf("failed to read from PTY: %w", err)
		}

		// Send output to client.
		if err := stream.Send(&agentv1.ShellResponse{
			Payload: &agentv1.ShellResponse_Output{
				Output: buf[:n],
			},
		}); err != nil {
			return fmt.Errorf("failed to send output: %w", err)
		}

		// Update last active time.
		session.UpdateLastActive()
	}
}

// processInput processes input from the client and writes to PTY.
func (h *ShellHandler) processInput(
	stream *connect.BidiStream[agentv1.ShellRequest, agentv1.ShellResponse],
	session *shell.Session,
) error {
	pty := session.PTY()

	for {
		req, err := stream.Receive()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("failed to receive request: %w", err)
		}

		switch payload := req.Payload.(type) {
		case *agentv1.ShellRequest_Stdin:
			// Write stdin to PTY.
			if pty != nil {
				if _, err := pty.Write(payload.Stdin); err != nil {
					return fmt.Errorf("failed to write to PTY: %w", err)
				}
				session.UpdateLastActive()
			}

		case *agentv1.ShellRequest_Resize:
			// Resize PTY.
			//nolint:gosec // G115: Terminal dimensions are small values
			if err := session.Resize(uint16(payload.Resize.Rows), uint16(payload.Resize.Cols)); err != nil {
				h.logger.Warn().Err(err).Msg("Failed to resize PTY")
			}

		case *agentv1.ShellRequest_Signal:
			// Send signal to process.
			if err := session.Signal(payload.Signal.Signal); err != nil {
				h.logger.Warn().Str("signal", payload.Signal.Signal).Err(err).Msg("Failed to send signal")
			}
		}
	}
}

// cleanupSession cleans up session resources.
func (h *ShellHandler) cleanupSession(session *shell.Session) {
	if err := session.Close(); err != nil {
		h.logger.Warn().Err(err).Str("session_id", session.ID).Msg("Failed to close session")
	}

	// Remove from sessions map.
	h.mu.Lock()
	delete(h.sessions, session.ID)
	h.mu.Unlock()
}

// ResizeShellTerminal resizes a shell terminal (RFD 026).
func (h *ShellHandler) ResizeShellTerminal(
	ctx context.Context,
	req *connect.Request[agentv1.ResizeShellTerminalRequest],
) (*connect.Response[agentv1.ResizeShellTerminalResponse], error) {
	h.mu.RLock()
	session, exists := h.sessions[req.Msg.SessionId]
	h.mu.RUnlock()

	if !exists {
		return connect.NewResponse(&agentv1.ResizeShellTerminalResponse{
			Success: false,
			Error:   "session not found",
		}), nil
	}

	if err := session.Resize(uint16(req.Msg.Rows), uint16(req.Msg.Cols)); err != nil {
		return connect.NewResponse(&agentv1.ResizeShellTerminalResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&agentv1.ResizeShellTerminalResponse{
		Success: true,
	}), nil
}

// SendShellSignal sends a signal to a shell session (RFD 026).
func (h *ShellHandler) SendShellSignal(
	ctx context.Context,
	req *connect.Request[agentv1.SendShellSignalRequest],
) (*connect.Response[agentv1.SendShellSignalResponse], error) {
	h.mu.RLock()
	session, exists := h.sessions[req.Msg.SessionId]
	h.mu.RUnlock()

	if !exists {
		return connect.NewResponse(&agentv1.SendShellSignalResponse{
			Success: false,
			Error:   "session not found",
		}), nil
	}

	if err := session.Signal(req.Msg.Signal); err != nil {
		return connect.NewResponse(&agentv1.SendShellSignalResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&agentv1.SendShellSignalResponse{
		Success: true,
	}), nil
}

// KillShellSession kills a shell session (RFD 026).
func (h *ShellHandler) KillShellSession(
	ctx context.Context,
	req *connect.Request[agentv1.KillShellSessionRequest],
) (*connect.Response[agentv1.KillShellSessionResponse], error) {
	h.mu.RLock()
	session, exists := h.sessions[req.Msg.SessionId]
	h.mu.RUnlock()

	if !exists {
		return connect.NewResponse(&agentv1.KillShellSessionResponse{
			Success: false,
			Error:   "session not found",
		}), nil
	}

	// Kill the session.
	h.cleanupSession(session)

	return connect.NewResponse(&agentv1.KillShellSessionResponse{
		Success: true,
	}), nil
}
