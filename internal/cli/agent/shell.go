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
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"golang.org/x/net/http2"
	"golang.org/x/term"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/config"
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
			_, _ = fmt.Scanln(&response) // TODO: errcheck
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

// runCommandExecution executes a one-off command on the agent (RFD 045).
// This is similar to kubectl exec pod -- command args.
func runCommandExecution(ctx context.Context, agentAddr, userID string, command []string) error {
	// Get current user if not specified.
	if userID == "" {
		userID = os.Getenv("USER")
		if userID == "" {
			userID = "unknown"
		}
	}

	// Normalize agent address (strip http:// or https:// prefix if present).
	normalizedAddr := agentAddr
	if strings.HasPrefix(agentAddr, "http://") {
		normalizedAddr = strings.TrimPrefix(agentAddr, "http://")
	} else if strings.HasPrefix(agentAddr, "https://") {
		normalizedAddr = strings.TrimPrefix(agentAddr, "https://")
	}

	// Create HTTP client.
	httpClient := &http.Client{
		Timeout: 35 * time.Second, // Slightly longer than default command timeout
	}
	client := agentv1connect.NewAgentServiceClient(
		httpClient,
		fmt.Sprintf("http://%s", normalizedAddr),
	)

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

// runShellSession runs the interactive shell session.
func runShellSession(ctx context.Context, agentAddr, userID string) error {
	// Create cancellable context for cleanup.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Ensure terminal is restored on any exit path.
	var oldState *term.State
	defer func() {
		if oldState != nil {
			_ = term.Restore(int(os.Stdin.Fd()), oldState) // TODO: errcheck
		}
	}()

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
			// Set reasonable timeouts to detect dead connections.
			ReadIdleTimeout: 30 * time.Second,
			PingTimeout:     15 * time.Second,
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
	oldState, err = term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set terminal to raw mode: %w", err)
	}

	// Handle terminal resize.
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	go func() {
		for range sigwinch {
			width, height, err := term.GetSize(int(os.Stdin.Fd()))
			if err != nil {
				continue
			}

			_ = stream.Send(&agentv1.ShellRequest{ // TODO: errcheck
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
	// First Ctrl+C forwards to remote shell, second Ctrl+C within 1s kills client.
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
					if oldState != nil {
						_ = term.Restore(int(os.Stdin.Fd()), oldState) // TODO: errcheck
					}
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
			_ = stream.Send(&agentv1.ShellRequest{ // TODO: errcheck
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
		defer close(stdinDone)
		buf := make([]byte, 4096)
		for {
			// Check if context is cancelled.
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
		// Check if context is cancelled.
		select {
		case <-ctx.Done():
			// Restore terminal before exiting.
			if oldState != nil {
				_ = term.Restore(int(os.Stdin.Fd()), oldState) // TODO: errcheck
				oldState = nil                                 // Prevent double restore in defer.
			}
			fmt.Fprintf(os.Stderr, "\n\nConnection lost to agent.\n")
			return fmt.Errorf("connection interrupted")
		default:
		}

		resp, err := stream.Receive()
		if err != nil {
			if err == io.EOF {
				break
			}
			// Check if stdin goroutine had an error.
			select {
			case stdinErr := <-stdinDone:
				if stdinErr != nil {
					// Restore terminal before showing error.
					if oldState != nil {
						_ = term.Restore(int(os.Stdin.Fd()), oldState) // TODO: errcheck
						oldState = nil
					}
					fmt.Fprintf(os.Stderr, "\n\nConnection lost to agent.\n")
					return fmt.Errorf("stdin error: %w", stdinErr)
				}
			default:
			}
			// Restore terminal before showing error.
			if oldState != nil {
				_ = term.Restore(int(os.Stdin.Fd()), oldState) // TODO: errcheck
				oldState = nil
			}
			fmt.Fprintf(os.Stderr, "\n\nConnection lost to agent.\n")
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
	if oldState != nil {
		_ = term.Restore(int(os.Stdin.Fd()), oldState) // TODO: errcheck
		oldState = nil                                 // Prevent double restore in defer.
	}

	// Show exit message.
	fmt.Fprintf(os.Stderr, "\nSession ended. Audit ID: %s\n", sessionID)

	// Exit with shell's exit code.
	if exitCode != 0 {
		os.Exit(int(exitCode))
	}

	return nil
}

// resolveAgentID resolves an agent ID to mesh IP:port via colony registry (RFD 044).
// This enables targeting agents by ID instead of requiring manual mesh IP lookup.
func resolveAgentID(ctx context.Context, agentID, colonyID string) (string, error) {
	// Create config resolver.
	resolver, err := config.NewResolver()
	if err != nil {
		return "", fmt.Errorf("failed to create config resolver: %w", err)
	}

	// Resolve colony ID if not specified.
	if colonyID == "" {
		colonyID, err = resolver.ResolveColonyID()
		if err != nil {
			return "", fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
		}
	}

	// Load colony configuration.
	loader := resolver.GetLoader()
	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return "", fmt.Errorf("failed to load colony config: %w", err)
	}

	// Get connect port.
	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = 9000
	}

	// Create RPC client - try localhost first, then mesh IP.
	baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
	client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

	// Call ListAgents RPC with timeout.
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
	resp, err := client.ListAgents(ctxWithTimeout, req)
	if err != nil {
		// Try mesh IP as fallback.
		meshIP := colonyConfig.WireGuard.MeshIPv4
		if meshIP == "" {
			meshIP = "10.42.0.1"
		}
		baseURL = fmt.Sprintf("http://%s:%d", meshIP, connectPort)
		client = colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

		ctxWithTimeout2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
		defer cancel2()

		resp, err = client.ListAgents(ctxWithTimeout2, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
		if err != nil {
			return "", fmt.Errorf("failed to query colony (is colony running?): %w", err)
		}
	}

	// Find agent with matching ID.
	for _, agent := range resp.Msg.Agents {
		if agent.AgentId == agentID {
			// Return mesh IP with agent port (default: 9001).
			// Note: This assumes agents listen on 9001, which is the default agent port.
			return fmt.Sprintf("%s:9001", agent.MeshIpv4), nil
		}
	}

	return "", fmt.Errorf("agent not found: %s\n\nAvailable agents:\n%s", agentID, formatAvailableAgents(resp.Msg.Agents))
}

// formatAvailableAgents formats the list of available agents for error messages.
func formatAvailableAgents(agents []*colonyv1.Agent) string {
	if len(agents) == 0 {
		return "  (no agents connected)"
	}

	var result strings.Builder
	for _, agent := range agents {
		result.WriteString(fmt.Sprintf("  - %s (mesh IP: %s)\n", agent.AgentId, agent.MeshIpv4))
	}
	return result.String()
}
