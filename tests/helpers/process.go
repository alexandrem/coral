package helpers

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/stretchr/testify/require"
)

// Process represents a managed test process.
type Process struct {
	Name    string
	Cmd     *exec.Cmd
	stdout  *LogWriter
	stderr  *LogWriter
	started bool
	mu      sync.Mutex
}

// LogWriter captures process output for testing.
type LogWriter struct {
	name   string
	prefix string
	lines  []string
	mu     sync.Mutex
}

// NewLogWriter creates a new log writer.
func NewLogWriter(name, prefix string) *LogWriter {
	return &LogWriter{
		name:   name,
		prefix: prefix,
		lines:  make([]string, 0),
	}
}

// Write implements io.Writer.
func (w *LogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	line := string(p)
	w.lines = append(w.lines, line)
	fmt.Printf("[%s-%s] %s", w.name, w.prefix, line)
	return len(p), nil
}

// Lines returns all captured lines.
func (w *LogWriter) Lines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := make([]string, len(w.lines))
	copy(result, w.lines)
	return result
}

// NewProcess creates a new managed process.
func NewProcess(name string, command string, args ...string) *Process {
	cmd := exec.Command(command, args...)
	p := &Process{
		Name:   name,
		Cmd:    cmd,
		stdout: NewLogWriter(name, "OUT"),
		stderr: NewLogWriter(name, "ERR"),
	}
	cmd.Stdout = p.stdout
	cmd.Stderr = p.stderr

	// Set process group for clean termination
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	return p
}

// SetEnv sets environment variables for the process.
func (p *Process) SetEnv(env map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.Cmd.Env = os.Environ()
	for k, v := range env {
		p.Cmd.Env = append(p.Cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
}

// SetDir sets the working directory for the process.
func (p *Process) SetDir(dir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Cmd.Dir = dir
}

// Start starts the process.
func (p *Process) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return fmt.Errorf("process %s already started", p.Name)
	}

	if err := p.Cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", p.Name, err)
	}

	p.started = true
	return nil
}

// Wait waits for the process to exit.
func (p *Process) Wait() error {
	return p.Cmd.Wait()
}

// Signal sends a signal to the process.
func (p *Process) Signal(sig os.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started || p.Cmd.Process == nil {
		return fmt.Errorf("process %s not running", p.Name)
	}

	return p.Cmd.Process.Signal(sig)
}

// Stop stops the process gracefully.
func (p *Process) Stop(timeout time.Duration) error {
	p.mu.Lock()
	if !p.started || p.Cmd.Process == nil {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// Try graceful shutdown first
	if err := p.Signal(syscall.SIGTERM); err != nil {
		return err
	}

	// Wait for process to exit or timeout
	done := make(chan error, 1)
	go func() {
		done <- p.Wait()
	}()

	select {
	case <-time.After(timeout):
		// Force kill if timeout
		if err := p.Signal(syscall.SIGKILL); err != nil {
			return fmt.Errorf("failed to kill %s: %w", p.Name, err)
		}
		return fmt.Errorf("process %s killed after timeout", p.Name)
	case err := <-done:
		return err
	}
}

// StdoutLines returns captured stdout lines.
func (p *Process) StdoutLines() []string {
	return p.stdout.Lines()
}

// StderrLines returns captured stderr lines.
func (p *Process) StderrLines() []string {
	return p.stderr.Lines()
}

// ProcessManager manages multiple test processes.
type ProcessManager struct {
	processes []*Process
	mu        sync.Mutex
	t         require.TestingT
}

// NewProcessManager creates a new process manager.
func NewProcessManager(t require.TestingT) *ProcessManager {
	return &ProcessManager{
		processes: make([]*Process, 0),
		t:         t,
	}
}

// Start starts a new process and tracks it.
func (pm *ProcessManager) Start(ctx context.Context, name string, command string, args ...string) *Process {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p := NewProcess(name, command, args...)
	err := p.Start()
	require.NoError(pm.t, err, "Failed to start process %s", name)

	pm.processes = append(pm.processes, p)

	// Monitor for context cancellation
	go func() {
		<-ctx.Done()
		_ = p.Stop(5 * time.Second)
	}()

	return p
}

// StopAll stops all managed processes.
func (pm *ProcessManager) StopAll(timeout time.Duration) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range pm.processes {
		wg.Add(1)
		go func(proc *Process) {
			defer wg.Done()
			if err := proc.Stop(timeout); err != nil {
				fmt.Printf("Warning: Failed to stop %s: %v\n", proc.Name, err)
			}
		}(p)
	}

	wg.Wait()
	pm.processes = pm.processes[:0]
}

// Get returns a process by name.
func (pm *ProcessManager) Get(name string) *Process {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for _, p := range pm.processes {
		if p.Name == name {
			return p
		}
	}
	return nil
}
