package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/safe"
)

// NewShellCmd creates the shell command for interactive debugging (RFD 026, RFD 044).
func NewShellCmd() *cobra.Command {
	var (
		agentAddr string
		agent     string
		colony    string
		userID    string
	)

	cmd := &cobra.Command{
		Use:   "shell [command...]",
		Short: "Open interactive shell or execute command in agent environment",
		Long: `Open an interactive shell session or execute a one-off command in the agent's environment.

This provides access to the agent's container/process with debugging utilities:
  - Network tools: tcpdump, netcat, curl, dig
  - Process inspection: ps, top
  - DuckDB CLI: query agent's local database
  - File access: agent config, logs, data

⚠️  WARNING: This shell runs with agent privileges, which may include:
  - Access to CRI socket (can exec into containers)
  - eBPF monitoring capabilities
  - WireGuard mesh network access
  - Agent configuration and storage

All sessions are fully recorded for audit purposes.

Examples:
  # Open interactive shell in local agent
  coral shell

  # Execute one-off command (like kubectl exec)
  coral shell -- ps aux
  coral shell -- sh -c "ps aux && netstat -tunlp"

  # Specify agent by ID
  coral shell --agent hostname-api-1
  coral shell --agent hostname-api-1 -- ps aux

  # Specify agent address explicitly
  coral shell --agent-addr localhost:9001 -- ls -la

  # Specify user ID for audit
  coral shell --user-id alice@company.com -- whoami

Interactive mode:
  - Uses /bin/bash if available, otherwise /bin/sh
  - Sets environment variables for agent context (CORAL_AGENT_ID, etc.)
  - Supports terminal resize and signals (Ctrl+C, Ctrl+Z)
  - Exits cleanly with the shell's exit code

Command execution mode:
  - Executes command and returns stdout/stderr
  - Returns command's exit code
  - Timeout: 30s (default), max 300s with --timeout
  - No TTY required`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// RFD 044: Agent ID resolution via colony registry.
			// If --agent is specified, query colony to resolve mesh IP.
			if agent != "" {
				if agentAddr != "" {
					return fmt.Errorf("cannot specify both --agent and --agent-addr")
				}

				// Resolve agent ID to mesh IP via colony registry.
				resolvedAddr, err := resolveAgentID(ctx, agent, colony)
				if err != nil {
					return fmt.Errorf("failed to resolve agent ID: %w", err)
				}
				agentAddr = resolvedAddr
			}

			// Discover local agent if address not specified.
			if agentAddr == "" {
				var err error
				agentAddr, err = discoverLocalAgent()
				if err != nil {
					return fmt.Errorf("failed to discover local agent: %w\n\nMake sure the agent is running:\n  coral agent start", err)
				}
			}

			// Check if command execution mode (args provided).
			if len(args) > 0 {
				// One-off command execution (like kubectl exec).
				return runCommandExecution(ctx, agentAddr, userID, args)
			}

			// Interactive shell mode.
			// Ensure we're running in a terminal.
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("interactive shell requires a TTY (use command execution mode for non-interactive: coral shell -- command)")
			}

			// Show warning and get confirmation.
			fmt.Fprintln(os.Stderr, "⚠️  WARNING: Entering agent debug shell with elevated privileges.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "This shell runs in the agent's container with access to:")
			fmt.Fprintln(os.Stderr, "  • CRI socket (can exec into containers)")
			fmt.Fprintln(os.Stderr, "  • eBPF monitoring data")
			fmt.Fprintln(os.Stderr, "  • WireGuard mesh network")
			fmt.Fprintln(os.Stderr, "  • Agent configuration and storage")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "This session will be fully recorded (input and output).")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprint(os.Stderr, "Continue? [y/N] ")

			var response string
			// Best effort - we just check the response value.
			_, _ = fmt.Scanln(&response)
			if response != "y" && response != "Y" {
				return fmt.Errorf("cancelled by user")
			}

			// Start shell session.
			return runShellSession(ctx, agentAddr, userID)
		},
	}

	cmd.Flags().StringVar(&agentAddr, "agent-addr", "", "Agent address (default: auto-discover)")
	cmd.Flags().StringVar(&agent, "agent", "", "Agent ID (resolves via colony registry)")
	cmd.Flags().StringVar(&colony, "colony", "", "Colony ID (default: auto-detect)")
	cmd.Flags().StringVar(&userID, "user-id", "", "User ID for audit (default: $USER)")

	return cmd
}

// resolveUserID returns the USER environment variable, or "unknown" if unset.
func resolveUserID() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

// openShellStream creates an HTTP/2 streaming connection to the agent shell and
// sends the initial start request.
func openShellStream(
	ctx context.Context,
	agentAddr, userID string,
	width, height int,
) (*connect.BidiStreamForClient[agentv1.ShellRequest, agentv1.ShellResponse], error) {
	client := newStreamingAgentClient(agentAddr)

	stream := client.Shell(ctx)
	rows, _ := safe.IntToUint32(height)
	cols, _ := safe.IntToUint32(width)
	if err := stream.Send(&agentv1.ShellRequest{
		Payload: &agentv1.ShellRequest_Start{
			Start: &agentv1.ShellStart{
				Shell:  "/bin/bash",
				UserId: userID,
				Size: &agentv1.TerminalSize{
					Rows: rows,
					Cols: cols,
				},
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to start shell: %w", err)
	}
	return stream, nil
}

// watchResizeSignals handles SIGWINCH by forwarding terminal resize events to
// the stream. The returned function stops signal delivery.
func watchResizeSignals(stream *connect.BidiStreamForClient[agentv1.ShellRequest, agentv1.ShellResponse]) func() {
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	go func() {
		for range sigwinch {
			width, height, err := term.GetSize(int(os.Stdin.Fd())) // #nosec G115 unlikely
			if err != nil {
				continue
			}
			// Best effort - if resize fails, user will notice terminal doesn't resize.
			rows, _ := safe.IntToUint32(height)
			cols, _ := safe.IntToUint32(width)
			_ = stream.Send(&agentv1.ShellRequest{
				Payload: &agentv1.ShellRequest_Resize{
					Resize: &agentv1.ShellResize{
						Rows: rows,
						Cols: cols,
					},
				},
			})
		}
	}()
	return func() { signal.Stop(sigwinch) }
}

// watchInterruptSignals handles SIGINT and SIGTERM for the shell session.
// onForceQuit is called before os.Exit when the user double-presses Ctrl+C
// within one second. The returned function stops signal delivery.
func watchInterruptSignals(
	cancel context.CancelFunc,
	stream *connect.BidiStreamForClient[agentv1.ShellRequest, agentv1.ShellResponse],
	onForceQuit func(),
) func() {
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, syscall.SIGINT, syscall.SIGTERM)
	var lastSigint time.Time
	go func() {
		for sig := range sigint {
			var sigName string
			switch sig {
			case syscall.SIGINT:
				sigName = "SIGINT"
				// Check for double Ctrl+C within 1 second to force-quit.
				now := time.Now()
				if !lastSigint.IsZero() && now.Sub(lastSigint) < time.Second {
					// Double Ctrl+C - force exit.
					onForceQuit()
					fmt.Fprintf(os.Stderr, "\n\nForce quit (double Ctrl+C).\n")
					cancel()
					os.Exit(130) // 128 + SIGINT
				}
				lastSigint = now
			case syscall.SIGTERM:
				// SIGTERM always exits immediately.
				cancel()
				return
			default:
				continue
			}
			// Forward signal to remote shell.
			// Best effort - if signal send fails, user may need to interrupt again.
			_ = stream.Send(&agentv1.ShellRequest{
				Payload: &agentv1.ShellRequest_Signal{
					Signal: &agentv1.ShellSignal{
						Signal: sigName,
					},
				},
			})
		}
	}()
	return func() { signal.Stop(sigint) }
}

// pipeStdin reads from stdin and forwards bytes to the shell stream until
// context cancellation or EOF. The returned channel receives any non-EOF error.
func pipeStdin(ctx context.Context, stream *connect.BidiStreamForClient[agentv1.ShellRequest, agentv1.ShellResponse]) <-chan error {
	done := make(chan error, 1)
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, err := os.Stdin.Read(buf)
			if err != nil {
				if err == io.EOF {
					return
				}
				done <- err
				return
			}
			if err := stream.Send(&agentv1.ShellRequest{
				Payload: &agentv1.ShellRequest_Stdin{
					Stdin: buf[:n],
				},
			}); err != nil {
				done <- err
				return
			}
		}
	}()
	return done
}

// receiveShellOutput reads responses from the shell stream until the shell
// exits or the context is cancelled. It returns the exit code, session ID, and
// any error.
func receiveShellOutput(
	ctx context.Context,
	stream *connect.BidiStreamForClient[agentv1.ShellRequest, agentv1.ShellResponse],
	stdinDone <-chan error,
) (exitCode int32, sessionID string, err error) {
	for {
		select {
		case <-ctx.Done():
			return 0, "", fmt.Errorf("connection interrupted")
		default:
		}

		resp, recvErr := stream.Receive()
		if recvErr != nil {
			if recvErr == io.EOF {
				return exitCode, sessionID, nil
			}
			select {
			case stdinErr := <-stdinDone:
				if stdinErr != nil {
					return 0, "", fmt.Errorf("stdin error: %w", stdinErr)
				}
			default:
			}
			return 0, "", fmt.Errorf("failed to receive from shell: %w", recvErr)
		}

		switch payload := resp.Payload.(type) {
		case *agentv1.ShellResponse_Output:
			if _, err := os.Stdout.Write(payload.Output); err != nil {
				return 0, "", fmt.Errorf("failed to write output: %w", err)
			}
		case *agentv1.ShellResponse_Exit:
			return payload.Exit.ExitCode, payload.Exit.SessionId, nil
		}
	}
}

// runCommandExecution executes a one-off command on the agent (RFD 045).
// This is similar to kubectl exec pod -- command args.
func runCommandExecution(ctx context.Context, agentAddr, userID string, command []string) error {
	// Get current user if not specified.
	if userID == "" {
		userID = resolveUserID()
	}

	client := newAgentClient(agentAddr)

	// Prepare request.
	req := &agentv1.ShellExecRequest{
		Command:        command,
		UserId:         userID,
		TimeoutSeconds: 30, // Default timeout
	}

	// Execute command with timeout.
	execCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()

	resp, err := client.ShellExec(execCtx, connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("failed to execute command on agent: %w", err)
	}

	// Write stdout.
	if len(resp.Msg.Stdout) > 0 {
		if _, err := os.Stdout.Write(resp.Msg.Stdout); err != nil {
			return fmt.Errorf("failed to write stdout: %w", err)
		}
	}

	// Write stderr.
	if len(resp.Msg.Stderr) > 0 {
		if _, err := os.Stderr.Write(resp.Msg.Stderr); err != nil {
			return fmt.Errorf("failed to write stderr: %w", err)
		}
	}

	// Show error if present.
	if resp.Msg.Error != "" {
		fmt.Fprintf(os.Stderr, "\nError: %s\n", resp.Msg.Error)
	}

	// Exit with command's exit code.
	if resp.Msg.ExitCode != 0 {
		os.Exit(int(resp.Msg.ExitCode))
	}

	return nil
}

// restoreTerminal restores terminal state and logs errors to stderr.
func restoreTerminal(state *term.State) {
	if state == nil {
		return
	}
	if err := term.Restore(int(os.Stdin.Fd()), state); err != nil { // #nosec G115 unlikely
		fmt.Fprintf(os.Stderr, "Warning: failed to restore terminal: %v\n", err)
	}
}

// runShellSession runs the interactive shell session.
func runShellSession(ctx context.Context, agentAddr, userID string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Ensure terminal is restored on any exit path.
	var oldState *term.State
	defer func() { restoreTerminal(oldState) }()

	if userID == "" {
		userID = resolveUserID()
	}

	width, height, err := term.GetSize(int(os.Stdin.Fd())) // #nosec G115 unlikely
	if err != nil {
		return fmt.Errorf("failed to get terminal size: %w", err)
	}

	stream, err := openShellStream(ctx, agentAddr, userID, width, height)
	if err != nil {
		return err
	}

	oldState, err = term.MakeRaw(int(os.Stdin.Fd())) // #nosec G115 unlikely
	if err != nil {
		return fmt.Errorf("failed to set terminal to raw mode: %w", err)
	}

	stopResize := watchResizeSignals(stream)
	defer stopResize()

	stopInterrupt := watchInterruptSignals(cancel, stream, func() {
		restoreTerminal(oldState)
		oldState = nil
	})
	defer stopInterrupt()

	stdinDone := pipeStdin(ctx, stream)

	exitCode, sessionID, err := receiveShellOutput(ctx, stream, stdinDone)

	// Restore terminal before any output regardless of success or failure.
	restoreTerminal(oldState)
	oldState = nil

	if err != nil {
		fmt.Fprintf(os.Stderr, "\n\nConnection lost to agent.\n")
		return err
	}

	if err := stream.CloseRequest(); err != nil {
		return fmt.Errorf("failed to close stream: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nSession ended. Audit ID: %s\n", sessionID)

	if exitCode != 0 {
		os.Exit(int(exitCode))
	}

	return nil
}
