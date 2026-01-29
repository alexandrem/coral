package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/agent/bootstrap"
	"github.com/coral-mesh/coral/internal/agent/certs"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/logging"
)

// NewCertCmd creates the cert command group for agents.
func NewCertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cert",
		Short: "Manage agent certificates",
		Long: `Manage agent mTLS certificates (RFD 048).

The cert command provides subcommands for managing the agent's certificate lifecycle:
  - status: Display certificate information and status
  - renew: Manually renew the certificate before expiry`,
	}

	cmd.AddCommand(newCertStatusCmd())
	cmd.AddCommand(newCertRenewCmd())

	return cmd
}

// newCertStatusCmd creates the cert status subcommand.
func newCertStatusCmd() *cobra.Command {
	var certsDir string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Display certificate status",
		Long: `Display the current certificate status and metadata.

Shows:
  - Certificate validity period and days remaining
  - Agent and Colony identifiers
  - SPIFFE ID
  - Issuer information
  - File locations

Examples:
  coral agent cert status
  coral agent cert status --certs-dir /custom/path`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCertStatus(certsDir)
		},
	}

	cmd.Flags().StringVar(&certsDir, "certs-dir", os.Getenv("CORAL_CERTS_DIR"), "Directory containing certificates")

	return cmd
}

func runCertStatus(certsDir string) error {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "warn",
		Pretty: false,
	}, "cert")

	certManager := certs.NewManager(certs.Config{
		CertsDir: certsDir,
		Logger:   logger,
	})

	// Check if certificate exists.
	if !certManager.CertificateExists() {
		fmt.Println("Certificate Status: MISSING")
		fmt.Println()
		fmt.Println("No certificate found. Run 'coral agent bootstrap' to obtain a certificate.")
		return nil
	}

	// Load certificate.
	if err := certManager.Load(); err != nil {
		return fmt.Errorf("failed to load certificate: %w", err)
	}

	info := certManager.GetCertificateInfo()

	// Determine status display.
	var statusDisplay string
	switch info.Status {
	case certs.CertStatusValid:
		statusDisplay = "✓ Valid"
	case certs.CertStatusRenewalNeeded:
		statusDisplay = "⚠ Renewal needed"
	case certs.CertStatusExpiringSoon:
		statusDisplay = "⚠ Expiring soon"
	case certs.CertStatusExpired:
		statusDisplay = "✗ Expired"
	default:
		statusDisplay = string(info.Status)
	}

	fmt.Println("Certificate Status")
	fmt.Println("==================")
	fmt.Printf("Agent ID:          %s\n", info.AgentID)
	fmt.Printf("Colony ID:         %s\n", info.ColonyID)
	fmt.Printf("Certificate Path:  %s\n", certManager.GetCertPath())
	fmt.Printf("Key Path:          %s\n", certManager.GetKeyPath())
	fmt.Printf("Root CA Path:      %s\n", certManager.GetRootCAPath())
	fmt.Println()
	fmt.Println("Certificate Details:")
	fmt.Printf("  Issuer:          %s\n", info.Issuer)
	fmt.Printf("  Serial Number:   %s\n", truncateSerial(info.SerialNumber))
	fmt.Printf("  SPIFFE ID:       %s\n", info.SPIFFEID)
	fmt.Printf("  Not Before:      %s\n", info.NotBefore.Format(time.RFC3339))
	fmt.Printf("  Not After:       %s\n", info.NotAfter.Format(time.RFC3339))
	fmt.Printf("  Days Remaining:  %d\n", info.DaysRemaining)
	fmt.Println()
	fmt.Printf("Status:            %s\n", statusDisplay)

	// Show warnings for renewal.
	switch info.Status {
	case certs.CertStatusRenewalNeeded:
		fmt.Println()
		fmt.Println("Note: Certificate will need renewal soon. Use 'coral agent cert renew' or wait for automatic renewal.")
	case certs.CertStatusExpiringSoon:
		fmt.Println()
		fmt.Println("Warning: Certificate is expiring soon! Use 'coral agent cert renew' to renew immediately.")
	case certs.CertStatusExpired:
		fmt.Println()
		fmt.Println("Error: Certificate has expired. Use 'coral agent bootstrap' to obtain a new certificate.")
	}

	return nil
}

// newCertRenewCmd creates the cert renew subcommand.
func newCertRenewCmd() *cobra.Command {
	var (
		colonyID       string
		caFingerprint  string
		discoveryURL   string
		colonyEndpoint string
		certsDir       string
		force          bool
	)

	cmd := &cobra.Command{
		Use:   "renew",
		Short: "Renew agent certificate",
		Long: `Renew the agent certificate before expiry.

Certificate renewal uses the existing mTLS certificate for authentication,
so no Discovery interaction is required (no new bootstrap token needed).

When --colony-endpoint is provided, renewal uses direct mTLS authentication
with the colony (preferred, no Discovery required). Otherwise, falls back
to the bootstrap flow via Discovery.

If the certificate is expired or invalid, a full bootstrap is required instead.

Examples:
  # Renew using direct mTLS (no Discovery needed)
  coral agent cert renew --colony-endpoint https://colony.example.com:9000

  # Renew via Discovery (fallback)
  coral agent cert renew

  # Force renewal even if certificate is still valid
  coral agent cert renew --colony-endpoint https://colony:9000 --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCertRenew(cmd.Context(), certRenewOptions{
				ColonyID:       colonyID,
				CAFingerprint:  caFingerprint,
				DiscoveryURL:   discoveryURL,
				ColonyEndpoint: colonyEndpoint,
				CertsDir:       certsDir,
				Force:          force,
			})
		},
	}

	helpers.AddColonyFlag(cmd, &colonyID)
	cmd.Flags().StringVar(&caFingerprint, "fingerprint", os.Getenv("CORAL_CA_FINGERPRINT"), "Expected Root CA fingerprint")
	cmd.Flags().StringVar(&discoveryURL, "discovery", os.Getenv("CORAL_DISCOVERY_ENDPOINT"), "Discovery service URL (fallback)")
	cmd.Flags().StringVar(&colonyEndpoint, "colony-endpoint", os.Getenv("CORAL_COLONY_ENDPOINT"), "Colony HTTPS endpoint for direct mTLS renewal")
	cmd.Flags().StringVar(&certsDir, "certs-dir", os.Getenv("CORAL_CERTS_DIR"), "Directory containing certificates")
	cmd.Flags().BoolVar(&force, "force", false, "Force renewal even if not near expiry")

	return cmd
}

type certRenewOptions struct {
	ColonyID       string
	CAFingerprint  string
	DiscoveryURL   string
	ColonyEndpoint string
	CertsDir       string
	Force          bool
}

func runCertRenew(ctx context.Context, opts certRenewOptions) error {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "info",
		Pretty: true,
	}, "cert")

	certManager := certs.NewManager(certs.Config{
		CertsDir: opts.CertsDir,
		Logger:   logger,
	})

	// 1. Initial State Checks
	if !certManager.CertificateExists() {
		return fmt.Errorf("no certificate found; use 'coral agent bootstrap' to obtain a certificate first")
	}

	if err := certManager.Load(); err != nil {
		return fmt.Errorf("failed to load certificate: %w", err)
	}

	info := certManager.GetCertificateInfo()

	// 2. Policy Checks (Force vs Expiry)
	if !opts.Force && info.Status == certs.CertStatusValid {
		fmt.Printf("Certificate is valid for %d more days. Use --force to renew anyway.\n", info.DaysRemaining)
		return nil
	}

	if info.Status == certs.CertStatusExpired {
		return fmt.Errorf("certificate has expired; use 'coral agent bootstrap' for a new certificate")
	}

	// 3. Resolve Missing Configuration
	agentID, err := certManager.LoadAgentID()
	if err != nil {
		agentID = info.AgentID
	}

	if opts.ColonyID == "" {
		opts.ColonyID = info.ColonyID
	}

	// 4. Initialize the Unified Bootstrap Client
	// This client now handles both mTLS renewal and Token-based bootstrap
	client := bootstrap.NewClient(bootstrap.Config{
		AgentID:           agentID,
		ColonyID:          opts.ColonyID,
		CAFingerprint:     opts.CAFingerprint,
		DiscoveryEndpoint: opts.DiscoveryURL,
		Logger:            logger,
	})

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var result *bootstrap.Result

	// 5. Strategy A: Attempt Direct mTLS Renewal
	// This is preferred as it doesn't require Discovery or a new Token
	fmt.Print("Attempting mTLS renewal... ")
	result, err = client.Renew(
		ctx,
		certManager.GetCertPath(),
		certManager.GetKeyPath(),
		certManager.GetRootCAPath(),
	)

	// 6. Strategy B: Fallback to Token-based Bootstrap
	if err != nil {
		fmt.Println("✗")
		logger.Warn().Err(err).Msg("mTLS renewal failed; falling back to bootstrap flow")

		if opts.DiscoveryURL == "" {
			return fmt.Errorf("renewal failed and no discovery URL provided for fallback: %w", err)
		}

		fmt.Print("Attempting bootstrap renewal (Token-based)... ")
		result, err = client.Bootstrap(ctx)
		if err != nil {
			fmt.Println("✗")
			return fmt.Errorf("certificate recovery failed: %w", err)
		}
	}
	fmt.Println("✓")

	// 7. Persist the New Identity
	fmt.Print("Saving updated certificates... ")
	if err := certManager.Save(result); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("failed to save certificates: %w", err)
	}
	fmt.Println("✓")

	fmt.Printf("\n✓ Success! Certificate renewed. Valid until: %s\n", result.ExpiresAt.Format(time.RFC3339))
	return nil
}

// truncateSerial truncates a serial number for display.
func truncateSerial(serial string) string {
	if len(serial) > 32 {
		return serial[:32] + "..."
	}
	return serial
}
