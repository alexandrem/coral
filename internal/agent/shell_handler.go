package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/kr/pty"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	"github.com/coral-io/coral/internal/logging"
)

// ShellHandler implements the shell RPC methods for the agent (RFD 026).
type ShellHandler struct {
	logger   logging.Logger
	sessions map[string]*ShellSession
	mu       sync.RWMutex
}

// ShellSession represents an active shell session.
type ShellSession struct {
	ID         string
	UserID     string
	StartedAt  time.Time
	LastActive time.Time
	Status     SessionStatus
	ExitCode   *int
	cmd        *exec.Cmd
	pty        *os.File
	cancel     context.CancelFunc
	mu         sync.Mutex
}

// SessionStatus represents the status of a shell session.
type SessionStatus int

const (
	SessionActive SessionStatus = iota
	SessionExited
)

// NewShellHandler creates a new shell handler.
func NewShellHandler(logger logging.Logger) *ShellHandler {
	return &ShellHandler{
		logger:   logger,
		sessions: make(map[string]*ShellSession),
	}
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
) (*ShellSession, error) {
	// Determine shell to use.
	shell := start.Shell
	if shell == "" {
		shell = "/bin/bash"
	}

	// Check if shell exists.
	if _, err := os.Stat(shell); err != nil {
		if os.IsNotExist(err) {
			// Fallback to /bin/sh.
			shell = "/bin/sh"
			if _, err := os.Stat(shell); err != nil {
				return nil, fmt.Errorf("no shell available: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to check shell: %w", err)
		}
	}

	// Create session context with cancellation.
	sessionCtx, cancel := context.WithCancel(ctx)

	// Create command.
	cmd := exec.CommandContext(sessionCtx, shell)

	// Set environment variables.
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("CORAL_AGENT_ID=%s", "agent-local"))
	cmd.Env = append(cmd.Env, "CORAL_DATA=/var/lib/coral")
	cmd.Env = append(cmd.Env, "CORAL_CONFIG=/etc/coral/agent.yaml")
	for key, value := range start.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	// Open PTY manually for better control in containerized environments.
	ptmx, tty, err := pty.Open()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to open PTY: %w", err)
	}

	// Configure command to use the PTY.
	cmd.Stdout = tty
	cmd.Stdin = tty
	cmd.Stderr = tty
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true, // Create new session
		Setctty: true, // Set controlling terminal
		Ctty:    0,    // Use stdin as controlling terminal
	}

	// Start the command.
	if err := cmd.Start(); err != nil {
		tty.Close()
		ptmx.Close()
		cancel()
		return nil, fmt.Errorf("failed to start shell with PTY: %w", err)
	}

	// Close tty (slave side) in parent process - child process has its own copy.
	tty.Close()

	// Set initial terminal size.
	if start.Size != nil {
		if err := h.resizePTY(ptmx, uint16(start.Size.Rows), uint16(start.Size.Cols)); err != nil {
			h.logger.Warn().Err(err).Msg("Failed to set initial terminal size")
		}
	}

	// Create session.
	session := &ShellSession{
		ID:         uuid.New().String(),
		UserID:     start.UserId,
		StartedAt:  time.Now(),
		LastActive: time.Now(),
		Status:     SessionActive,
		cmd:        cmd,
		pty:        ptmx,
		cancel:     cancel,
	}

	// Store session.
	h.mu.Lock()
	h.sessions[session.ID] = session
	h.mu.Unlock()

	// Monitor process exit.
	go h.monitorProcess(session)

	return session, nil
}

// streamOutput streams PTY output to the client.
func (h *ShellHandler) streamOutput(
	stream *connect.BidiStream[agentv1.ShellRequest, agentv1.ShellResponse],
	session *ShellSession,
) error {
	buf := make([]byte, 4096)
	for {
		n, err := session.pty.Read(buf)
		if err != nil {
			// Check if this is a normal exit condition (EOF or EIO).
			// PTYs can return EIO (input/output error) when the slave side is closed.
			isExitError := err == io.EOF
			if !isExitError {
				// Check for syscall.EIO (input/output error from PTY).
				if pathErr, ok := err.(*os.PathError); ok {
					isExitError = pathErr.Err == syscall.EIO
				}
			}

			if isExitError {
				// Process exited, send exit response.
				session.mu.Lock()
				exitCode := session.ExitCode
				session.mu.Unlock()

				if exitCode == nil {
					code := 0
					exitCode = &code
				}

				return stream.Send(&agentv1.ShellResponse{
					Payload: &agentv1.ShellResponse_Exit{
						Exit: &agentv1.ShellExit{
							ExitCode:  int32(*exitCode),
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
		session.mu.Lock()
		session.LastActive = time.Now()
		session.mu.Unlock()
	}
}

// processInput processes input from the client and writes to PTY.
func (h *ShellHandler) processInput(
	stream *connect.BidiStream[agentv1.ShellRequest, agentv1.ShellResponse],
	session *ShellSession,
) error {
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
			if _, err := session.pty.Write(payload.Stdin); err != nil {
				return fmt.Errorf("failed to write to PTY: %w", err)
			}

			// Update last active time.
			session.mu.Lock()
			session.LastActive = time.Now()
			session.mu.Unlock()

		case *agentv1.ShellRequest_Resize:
			// Resize PTY.
			if err := h.resizePTY(session.pty, uint16(payload.Resize.Rows), uint16(payload.Resize.Cols)); err != nil {
				h.logger.Warn().Err(err).Msg("Failed to resize PTY")
			}

		case *agentv1.ShellRequest_Signal:
			// Send signal to process.
			if err := h.sendSignal(session, payload.Signal.Signal); err != nil {
				h.logger.Warn().Str("signal", payload.Signal.Signal).Err(err).Msg("Failed to send signal")
			}
		}
	}
}

// monitorProcess monitors the shell process and captures exit code.
func (h *ShellHandler) monitorProcess(session *ShellSession) {
	err := session.cmd.Wait()

	session.mu.Lock()
	defer session.mu.Unlock()

	session.Status = SessionExited

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			session.ExitCode = &exitCode
		} else {
			// Unknown error, use exit code 1.
			exitCode := 1
			session.ExitCode = &exitCode
		}
	} else {
		// Successful exit.
		exitCode := 0
		session.ExitCode = &exitCode
	}
}

// cleanupSession cleans up session resources.
func (h *ShellHandler) cleanupSession(session *ShellSession) {
	// Cancel context.
	if session.cancel != nil {
		session.cancel()
	}

	// Close PTY.
	if session.pty != nil {
		session.pty.Close()
	}

	// Kill process if still running.
	if session.cmd != nil && session.cmd.Process != nil {
		// Send SIGTERM.
		session.cmd.Process.Signal(syscall.SIGTERM)

		// Wait 5 seconds, then SIGKILL.
		time.AfterFunc(5*time.Second, func() {
			if session.cmd.ProcessState == nil {
				session.cmd.Process.Kill()
			}
		})
	}

	// Remove from sessions map.
	h.mu.Lock()
	delete(h.sessions, session.ID)
	h.mu.Unlock()
}

// resizePTY resizes the PTY to the specified dimensions.
func (h *ShellHandler) resizePTY(ptmx *os.File, rows, cols uint16) error {
	ws := &struct {
		Row uint16
		Col uint16
		X   uint16
		Y   uint16
	}{
		Row: rows,
		Col: cols,
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		ptmx.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(ws)),
	)

	if errno != 0 {
		return fmt.Errorf("ioctl TIOCSWINSZ failed: %v", errno)
	}

	return nil
}

// sendSignal sends a signal to the shell process.
func (h *ShellHandler) sendSignal(session *ShellSession, signalName string) error {
	if session.cmd == nil || session.cmd.Process == nil {
		return fmt.Errorf("process not running")
	}

	var sig syscall.Signal
	switch signalName {
	case "SIGINT":
		sig = syscall.SIGINT
	case "SIGTERM":
		sig = syscall.SIGTERM
	case "SIGTSTP":
		sig = syscall.SIGTSTP
	case "SIGKILL":
		sig = syscall.SIGKILL
	case "SIGHUP":
		sig = syscall.SIGHUP
	default:
		return fmt.Errorf("unsupported signal: %s", signalName)
	}

	return session.cmd.Process.Signal(sig)
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

	if err := h.resizePTY(session.pty, uint16(req.Msg.Rows), uint16(req.Msg.Cols)); err != nil {
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

	if err := h.sendSignal(session, req.Msg.Signal); err != nil {
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
