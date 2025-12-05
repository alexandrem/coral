// Package shell provides shell session management with PTY support.
// It handles the lifecycle of interactive shell sessions including process management,
// terminal resizing, and signal handling.
package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/google/uuid"
	"github.com/kr/pty"
)

// SessionStatus represents the status of a shell session.
type SessionStatus int

const (
	SessionActive SessionStatus = iota
	SessionExited
)

// Session represents an active shell session.
type Session struct {
	ID         string
	UserID     string
	StartedAt  time.Time
	LastActive time.Time
	Status     SessionStatus
	ExitCode   *int

	cmd    *exec.Cmd
	pty    *os.File
	cancel context.CancelFunc
	mu     sync.Mutex
}

// StartConfig configuration for starting a shell session.
type StartConfig struct {
	Shell  string
	UserID string
	Env    map[string]string
	Rows   uint16
	Cols   uint16
}

// Start creates and starts a new shell session.
func Start(ctx context.Context, config StartConfig) (*Session, error) {
	// Determine shell to use.
	shell := config.Shell
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
	// Note: Generic env vars moved to caller or passed in config.Env
	for key, value := range config.Env {
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
		_ = tty.Close()
		_ = ptmx.Close()
		cancel()
		return nil, fmt.Errorf("failed to start shell with PTY: %w", err)
	}

	// Close tty (slave side) in parent process - child process has its own copy.
	_ = tty.Close()

	// Create session.
	session := &Session{
		ID:         uuid.New().String(),
		UserID:     config.UserID,
		StartedAt:  time.Now(),
		LastActive: time.Now(),
		Status:     SessionActive,
		cmd:        cmd,
		pty:        ptmx,
		cancel:     cancel,
	}

	// Set initial size if provided
	if config.Rows > 0 && config.Cols > 0 {
		// Ignore resize errors during session start - not critical
		_ = session.Resize(config.Rows, config.Cols)
	}

	// Monitor process exit.
	go session.monitorProcess()

	return session, nil
}

// Resize resizes the PTY to the specified dimensions.
func (s *Session) Resize(rows, cols uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status == SessionExited || s.pty == nil {
		return fmt.Errorf("session not active")
	}

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
		s.pty.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(ws)),
	)

	if errno != 0 {
		return fmt.Errorf("ioctl TIOCSWINSZ failed: %v", errno)
	}

	return nil
}

// Signal sends a signal to the shell process.
func (s *Session) Signal(signalName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd == nil || s.cmd.Process == nil {
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

	return s.cmd.Process.Signal(sig)
}

// Close cleans up session resources.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Cancel context.
	if s.cancel != nil {
		s.cancel()
	}

	// Close PTY.
	if s.pty != nil {
		_ = s.pty.Close()
	}

	// Kill process if still running.
	if s.cmd != nil && s.cmd.Process != nil && s.Status == SessionActive {
		// Send SIGTERM.
		_ = s.cmd.Process.Signal(syscall.SIGTERM)

		// Wait 5 seconds, then SIGKILL.
		// Use a separate goroutine or just verify state?
		// Since we are closing, we might not wait.
		// But for cleanup, we should ensure it dies.
		proc := s.cmd.Process // Capture

		go func() {
			time.Sleep(5 * time.Second)
			// Check if process is still running (wait not returned)
			// This is tricky without the Wait() result.
			// But Start ensures Wait() is called in monitorProcess.
			// We can just try to kill it.
			_ = proc.Kill()
		}()
	}

	s.Status = SessionExited
	return nil
}

// monitorProcess monitors the shell process and captures exit code.
func (s *Session) monitorProcess() {
	err := s.cmd.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.Status = SessionExited

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			s.ExitCode = &exitCode
		} else {
			// Unknown error, use exit code 1.
			exitCode := 1
			s.ExitCode = &exitCode
		}
	} else {
		// Successful exit.
		exitCode := 0
		s.ExitCode = &exitCode
	}

	// Ensure PTY is closed
	if s.pty != nil {
		_ = s.pty.Close()
	}
}

// PTY returns the PTY file for reading/writing.
func (s *Session) PTY() *os.File {
	return s.pty
}

// UpdateLastActive updates the LastActive timestamp.
func (s *Session) UpdateLastActive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastActive = time.Now()
}
