package agent

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	"github.com/coral-io/coral/coral/agent/v1/agentv1connect"
)

const (
	defaultAgentPort = 9001
	connectTimeout   = 5 * time.Second
)

// NewConnectCmd creates the connect command for attaching to services.
func NewConnectCmd() *cobra.Command {
	var (
		port      int
		healthURL string
		agentAddr string
	)

	cmd := &cobra.Command{
		Use:   "connect <service-spec>...",
		Short: "Connect one or more services",
		Long: `Connect a running Coral agent to observe services or application components.

The agent must already be running (via 'coral agent start') before using this command.

Service Specification Format:
  name:port[:health][:type]
  - name: Service/component name (alphanumeric + hyphens)
  - port: TCP port number (1-65535)
  - health: Optional health check endpoint path (e.g., /health)
  - type: Optional service type hint (e.g., http, redis, postgres)

Examples:
  # Single service (new syntax)
  coral connect frontend:3000
  coral connect api:8080:/health:http

  # Single service (legacy syntax, backward compatible)
  coral connect frontend --port 3000 --health /health

  # Multiple services
  coral connect frontend:3000:/health redis:6379 metrics:9090:/metrics
  coral connect api:8080:/health:http cache:6379::redis worker:9000

Note:
  - This command requires a running agent ('coral agent start')
  - Legacy flags (--port, --health) only work with single service specifications
  - Services are added to the agent dynamically without restart`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse service specifications
			serviceSpecs, err := parseServiceSpecsWithLegacySupport(args, port, healthURL)
			if err != nil {
				return err
			}

			// Validate service specs
			if err := ValidateServiceSpecs(serviceSpecs); err != nil {
				return fmt.Errorf("invalid service configuration: %w", err)
			}

			// Discover local agent
			if agentAddr == "" {
				agentAddr, err = discoverLocalAgent()
				if err != nil {
					return fmt.Errorf("failed to discover local agent: %w\n\nMake sure the agent is running:\n  coral agent start", err)
				}
			}

			// Display connection information
			if len(serviceSpecs) == 1 {
				fmt.Printf("Connecting to service: %s\n", serviceSpecs[0].Name)
				fmt.Printf("Port: %d\n", serviceSpecs[0].Port)
				if serviceSpecs[0].HealthEndpoint != "" {
					fmt.Printf("Health endpoint: %s\n", serviceSpecs[0].HealthEndpoint)
				}
				if serviceSpecs[0].ServiceType != "" {
					fmt.Printf("Service type: %s\n", serviceSpecs[0].ServiceType)
				}
			} else {
				fmt.Printf("Connecting to %d services:\n", len(serviceSpecs))
				for _, spec := range serviceSpecs {
					fmt.Printf("  • %s (port %d", spec.Name, spec.Port)
					if spec.HealthEndpoint != "" {
						fmt.Printf(", health: %s", spec.HealthEndpoint)
					}
					if spec.ServiceType != "" {
						fmt.Printf(", type: %s", spec.ServiceType)
					}
					fmt.Printf(")\n")
				}
			}

			fmt.Printf("Agent: %s\n", agentAddr)

			// Create gRPC client
			client := agentv1connect.NewAgentServiceClient(
				http.DefaultClient,
				fmt.Sprintf("http://%s", agentAddr),
			)

			// Connect each service
			ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
			defer cancel()

			for _, spec := range serviceSpecs {
				req := &agentv1.ConnectServiceRequest{
					ComponentName:  spec.Name,
					Port:           spec.Port,
					HealthEndpoint: spec.HealthEndpoint,
					ServiceType:    spec.ServiceType,
					Labels:         spec.Labels,
				}

				resp, err := client.ConnectService(ctx, connect.NewRequest(req))
				if err != nil {
					return fmt.Errorf("failed to connect service %s: %w", spec.Name, err)
				}

				if !resp.Msg.Success {
					return fmt.Errorf("agent rejected service connection %s: %s", spec.Name, resp.Msg.Error)
				}

				fmt.Printf("✓ Connected: %s\n", spec.Name)
			}

			fmt.Println("\n✓ All services connected successfully")
			fmt.Println("\nUse 'coral agent status' to view service health")

			return nil
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 0, "Service port (legacy, only works with single service)")
	cmd.Flags().StringVar(&healthURL, "health", "", "Health check endpoint (legacy, only works with single service)")
	cmd.Flags().StringVar(&agentAddr, "agent", "", "Agent address (default: auto-discover)")

	return cmd
}

// discoverLocalAgent attempts to discover a running local agent.
func discoverLocalAgent() (string, error) {
	// Try common agent endpoints in order.
	candidates := []string{
		fmt.Sprintf("localhost:%d", defaultAgentPort),
		fmt.Sprintf("127.0.0.1:%d", defaultAgentPort),
	}

	for _, addr := range candidates {
		// Try to connect to agent.
		client := agentv1connect.NewAgentServiceClient(
			http.DefaultClient,
			fmt.Sprintf("http://%s", addr),
		)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := client.GetRuntimeContext(ctx, connect.NewRequest(&agentv1.GetRuntimeContextRequest{}))
		cancel()

		if err == nil {
			return addr, nil
		}
	}

	return "", fmt.Errorf("no agent found at common endpoints")
}

// parseServiceSpecsWithLegacySupport parses service specs with backward compatibility.
func parseServiceSpecsWithLegacySupport(args []string, legacyPort int, legacyHealth string) ([]*ServiceSpec, error) {
	// Check if using new syntax (contains colon) or legacy syntax
	hasColonSyntax := false
	for _, arg := range args {
		if strings.Contains(arg, ":") {
			hasColonSyntax = true
			break
		}
	}

	// New syntax: parse service specs directly
	if hasColonSyntax {
		// If new syntax is used, legacy flags should not be set
		if legacyPort > 0 || legacyHealth != "" {
			return nil, fmt.Errorf("cannot use --port or --health flags with new service spec syntax (name:port[:health][:type])")
		}
		return ParseMultipleServiceSpecs(args)
	}

	// Legacy syntax: single service with --port flag required
	if len(args) > 1 {
		return nil, fmt.Errorf("multiple services require new syntax (e.g., 'coral connect frontend:3000 redis:6379')")
	}

	if legacyPort == 0 {
		return nil, fmt.Errorf("--port flag is required when using legacy syntax (or use new syntax: 'coral connect %s:PORT')", args[0])
	}

	// Build service spec from legacy format
	serviceName := args[0]
	spec := &ServiceSpec{
		Name:   serviceName,
		Port:   int32(legacyPort),
		Labels: make(map[string]string),
	}

	// Add health endpoint if provided
	if legacyHealth != "" {
		// Ensure it starts with /
		if !strings.HasPrefix(legacyHealth, "/") {
			spec.HealthEndpoint = "/" + legacyHealth
		} else {
			spec.HealthEndpoint = legacyHealth
		}
	}

	// Validate the service name
	if !serviceNameRegex.MatchString(serviceName) {
		return nil, fmt.Errorf("invalid service name '%s': must be alphanumeric with hyphens", serviceName)
	}

	return []*ServiceSpec{spec}, nil
}
