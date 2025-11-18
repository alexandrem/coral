package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/net/http2"
	"golang.org/x/term"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	"github.com/coral-io/coral/coral/agent/v1/agentv1connect"
)

// NewShellCmd creates the shell command for interactive debugging (RFD 026).
func NewShellCmd() *cobra.Command {
	var (
		agentAddr string
		userID    string
	)

	cmd := &cobra.Command{
		Use:   "shell [target]",
		Short: "Open interactive shell in agent environment (RFD 026)",
		Long: `Open an interactive shell session within the agent's environment.

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
  # Open shell in local agent
  coral agent shell

  # Specify agent address explicitly
  coral agent shell --agent-addr localhost:9001

  # Specify user ID for audit
  coral agent shell --user-id alice@company.com

The shell session will:
  - Use /bin/bash if available, otherwise /bin/sh
  - Set environment variables for agent context (CORAL_AGENT_ID, etc.)
  - Support terminal resize and signals (Ctrl+C, Ctrl+Z)
  - Exit cleanly with the shell's exit code`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// Discover local agent if address not specified.
			if agentAddr == "" {
				var err error
				agentAddr, err = discoverLocalAgent()
				if err != nil {
					return fmt.Errorf("failed to discover local agent: %w\n\nMake sure the agent is running:\n  coral agent start", err)
				}
			}

			// Ensure we're running in a terminal.
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("shell command requires a TTY (interactive terminal)")
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
			fmt.Scanln(&response)
			if response != "y" && response != "Y" {
				return fmt.Errorf("cancelled by user")
			}

			// Start shell session.
			return runShellSession(ctx, agentAddr, userID)
		},
	}

	cmd.Flags().StringVar(&agentAddr, "agent-addr", "", "Agent address (default: auto-discover)")
	cmd.Flags().StringVar(&userID, "user-id", "", "User ID for audit (default: $USER)")

	return cmd
}

// runShellSession runs the interactive shell session.
func runShellSession(ctx context.Context, agentAddr, userID string) error {
	// Get current user if not specified.
	if userID == "" {
		userID = os.Getenv("USER")
		if userID == "" {
			userID = "unknown"
		}
	}

	// Get terminal size.
	width, height, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to get terminal size: %w", err)
	}

	// Normalize agent address (strip http:// or https:// prefix if present).
	normalizedAddr := agentAddr
	if strings.HasPrefix(agentAddr, "http://") {
		normalizedAddr = strings.TrimPrefix(agentAddr, "http://")
	} else if strings.HasPrefix(agentAddr, "https://") {
		normalizedAddr = strings.TrimPrefix(agentAddr, "https://")
	}

	// Create HTTP client with HTTP/2 support for bidirectional streaming.
	httpClient := &http.Client{
		Transport: &http2.Transport{
			// Allow HTTP/2 over plaintext (h2c).
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				// Dial without TLS for h2c.
				return net.Dial(network, addr)
			},
		},
	}
	client := agentv1connect.NewAgentServiceClient(
		httpClient,
		fmt.Sprintf("http://%s", normalizedAddr),
	)

	// Create streaming shell connection.
	stream := client.Shell(ctx)

	// Send initial shell start request.
	if err := stream.Send(&agentv1.ShellRequest{
		Payload: &agentv1.ShellRequest_Start{
			Start: &agentv1.ShellStart{
				Shell:  "/bin/bash",
				UserId: userID,
				Size: &agentv1.TerminalSize{
					Rows: uint32(height),
					Cols: uint32(width),
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("failed to start shell: %w", err)
	}

	// Put terminal in raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set terminal to raw mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Handle terminal resize.
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	go func() {
		for range sigwinch {
			width, height, err := term.GetSize(int(os.Stdin.Fd()))
			if err != nil {
				continue
			}

			stream.Send(&agentv1.ShellRequest{
				Payload: &agentv1.ShellRequest_Resize{
					Resize: &agentv1.ShellResize{
						Rows: uint32(height),
						Cols: uint32(width),
					},
				},
			})
		}
	}()
	defer signal.Stop(sigwinch)

	// Handle Ctrl+C and other signals.
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigint {
			var sigName string
			switch sig {
			case syscall.SIGINT:
				sigName = "SIGINT"
			case syscall.SIGTERM:
				sigName = "SIGTERM"
			default:
				continue
			}

			stream.Send(&agentv1.ShellRequest{
				Payload: &agentv1.ShellRequest_Signal{
					Signal: &agentv1.ShellSignal{
						Signal: sigName,
					},
				},
			})
		}
	}()
	defer signal.Stop(sigint)

	// Start goroutine to copy stdin to shell.
	stdinDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				if err == io.EOF {
					stdinDone <- nil
					return
				}
				stdinDone <- err
				return
			}

			if err := stream.Send(&agentv1.ShellRequest{
				Payload: &agentv1.ShellRequest_Stdin{
					Stdin: buf[:n],
				},
			}); err != nil {
				stdinDone <- err
				return
			}
		}
	}()

	// Read shell output and write to stdout.
	var exitCode int32
	var sessionID string

	for {
		resp, err := stream.Receive()
		if err != nil {
			if err == io.EOF {
				break
			}
			// Check if stdin goroutine had an error.
			select {
			case stdinErr := <-stdinDone:
				if stdinErr != nil {
					return fmt.Errorf("stdin error: %w", stdinErr)
				}
			default:
			}
			return fmt.Errorf("failed to receive from shell: %w", err)
		}

		switch payload := resp.Payload.(type) {
		case *agentv1.ShellResponse_Output:
			// Write output to stdout.
			if _, err := os.Stdout.Write(payload.Output); err != nil {
				return fmt.Errorf("failed to write output: %w", err)
			}

		case *agentv1.ShellResponse_Exit:
			// Shell exited.
			exitCode = payload.Exit.ExitCode
			sessionID = payload.Exit.SessionId
			goto exit
		}
	}

exit:
	// Close the stream.
	if err := stream.CloseRequest(); err != nil {
		return fmt.Errorf("failed to close stream: %w", err)
	}

	// Restore terminal before showing exit message.
	term.Restore(int(os.Stdin.Fd()), oldState)

	// Show exit message.
	fmt.Fprintf(os.Stderr, "\nSession ended. Audit ID: %s\n", sessionID)

	// Exit with shell's exit code.
	if exitCode != 0 {
		os.Exit(int(exitCode))
	}

	return nil
}
