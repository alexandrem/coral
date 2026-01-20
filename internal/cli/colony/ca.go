// Package colony provides CLI commands for colony management.
package colony

import (
	"database/sql"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// NewCACmd creates the CA management command (RFD 047).
func NewCACmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ca",
		Short: "Manage colony certificate authority",
		Long: `Manage the embedded certificate authority for agent mTLS authentication.

The CA is used to issue and revoke certificates for agents connecting to the colony.
This implements RFD 047 - Colony CA Infrastructure & Policy Signing.`,
	}

	cmd.AddCommand(newCAStatusCmd())
	cmd.AddCommand(newCARotateCmd())

	return cmd
}

func newCAStatusCmd() *cobra.Command {
	var colonyID string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show CA status and fingerprint",
		Long:  `Display the status of the colony certificate authority including root CA fingerprint and hierarchy.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// 1. Try to use Colony RPC first (RFD 031).
			client, err := helpers.GetColonyClient(colonyID)
			if err == nil {
				resp, err := client.GetCAStatus(ctx, connect.NewRequest(&colonyv1.GetCAStatusRequest{}))
				if err == nil {
					// Print RPC response.
					status := resp.Msg
					fmt.Printf("Root CA:\n")
					fmt.Printf("  Path:        %s\n", status.RootCa.Path)
					fmt.Printf("  Fingerprint: sha256:%s\n", status.RootCa.Fingerprint)
					fmt.Printf("  Expires:     %s (%s)\n", status.RootCa.ExpiresAt.AsTime().Format("2006-01-02"), formatCADuration(time.Until(status.RootCa.ExpiresAt.AsTime())))

					fmt.Printf("\nIntermediates:\n")
					fmt.Printf("  Server: %s (Expires %s)\n", formatValidity(status.ServerIntermediate.ExpiresAt.AsTime()), status.ServerIntermediate.ExpiresAt.AsTime().Format("2006-01-02"))
					fmt.Printf("  Agent:  %s (Expires %s)\n", formatValidity(status.AgentIntermediate.ExpiresAt.AsTime()), status.AgentIntermediate.ExpiresAt.AsTime().Format("2006-01-02"))

					fmt.Printf("\nPolicy Signing:\n")
					fmt.Printf("  Certificate: %s (Expires %s)\n", formatValidity(status.PolicySigning.ExpiresAt.AsTime()), status.PolicySigning.ExpiresAt.AsTime().Format("2006-01-02"))

					fmt.Printf("\nColony SPIFFE ID: %s\n", status.ColonySpiffeId)

					fmt.Printf("\nCertificate Statistics:\n")
					fmt.Printf("  Total Issued: %d\n", status.Statistics.TotalIssued)
					fmt.Printf("  Active:       %d\n", status.Statistics.Active)
					fmt.Printf("  Revoked:      %d\n", status.Statistics.Revoked)

					return nil
				}
				// If RPC failed due to connection, fall back to local access.
			}

			// 2. Fallback to local database access (requires same-host execution).
			manager, db, _, err := helpers.GetCAManager(colonyID)
			if err != nil {
				return fmt.Errorf("failed to get CA status: colony is unreachable and local access failed: %w", err)
			}
			defer db.Close()

			// Get CA status.
			status := manager.GetStatus()

			// Print formatted output per RFD 047.
			fmt.Printf("Root CA:\n")
			fmt.Printf("  Path:        %s\n", status.RootCA.Path)
			fmt.Printf("  Fingerprint: sha256:%s\n", status.RootCA.Fingerprint)
			fmt.Printf("  Expires:     %s (%s)\n", status.RootCA.ExpiresAt.Format("2006-01-02"), formatCADuration(time.Until(status.RootCA.ExpiresAt)))

			fmt.Printf("\nIntermediates:\n")
			fmt.Printf("  Server: %s (Expires %s)\n", formatValidity(status.ServerIntermediate.ExpiresAt), status.ServerIntermediate.ExpiresAt.Format("2006-01-02"))
			fmt.Printf("  Agent:  %s (Expires %s)\n", formatValidity(status.AgentIntermediate.ExpiresAt), status.AgentIntermediate.ExpiresAt.Format("2006-01-02"))

			fmt.Printf("\nPolicy Signing:\n")
			fmt.Printf("  Certificate: %s (Expires %s)\n", formatValidity(status.PolicySigning.ExpiresAt), status.PolicySigning.ExpiresAt.Format("2006-01-02"))

			fmt.Printf("\nColony SPIFFE ID: %s\n", status.ColonySPIFFEID)

			// Query certificate statistics from database.
			var totalCerts, activeCerts, revokedCerts int
			err = db.QueryRow(`
				SELECT
					COUNT(*) as total,
					SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END) as active,
					SUM(CASE WHEN status = 'revoked' THEN 1 ELSE 0 END) as revoked
				FROM issued_certificates
			`).Scan(&totalCerts, &activeCerts, &revokedCerts)
			if err != nil && err != sql.ErrNoRows {
				// Table may not exist yet, ignore.
				totalCerts, activeCerts, revokedCerts = 0, 0, 0
			}

			fmt.Printf("\nCertificate Statistics:\n")
			fmt.Printf("  Total Issued: %d\n", totalCerts)
			fmt.Printf("  Active:       %d\n", activeCerts)
			fmt.Printf("  Revoked:      %d\n", revokedCerts)

			return nil
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (defaults to current colony)")

	return cmd
}

// formatValidity returns "Valid" or "Expired" based on expiry time.
func formatValidity(expiresAt time.Time) string {
	if time.Now().Before(expiresAt) {
		return "Valid"
	}
	return "Expired"
}

// formatCADuration formats a duration in human-readable form for CA status.
func formatCADuration(d time.Duration) string {
	years := int(d.Hours() / 24 / 365)
	if years > 0 {
		return fmt.Sprintf("%d years", years)
	}
	days := int(d.Hours() / 24)
	if days > 0 {
		return fmt.Sprintf("%d days", days)
	}
	return fmt.Sprintf("%d hours", int(d.Hours()))
}

func newCARotateCmd() *cobra.Command {
	var colonyID string
	var certType string
	var confirm bool

	cmd := &cobra.Command{
		Use:   "rotate-intermediate",
		Short: "Rotate an intermediate CA certificate",
		Long: `Rotate an intermediate CA certificate with zero-downtime issuance.

This command generates a new intermediate CA certificate signed by the root CA.
The old intermediate remains valid for 7 days (overlap period), allowing for gradual migration.

Certificate types: server, agent

WARNING: This is a sensitive operation. Use --confirm to proceed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return fmt.Errorf("CA rotation requires --confirm flag to proceed")
			}

			if certType != "server" && certType != "agent" {
				return fmt.Errorf("--type must be 'server' or 'agent'")
			}

			// Get CA manager.
			manager, db, cfg, err := helpers.GetCAManager(colonyID)
			if err != nil {
				return err
			}
			defer db.Close()

			// Perform rotation.
			fmt.Printf("Rotating %s intermediate CA for colony %s...\n", certType, cfg.ColonyID)
			if err := manager.RotateIntermediate(certType); err != nil {
				return fmt.Errorf("failed to rotate intermediate CA: %w", err)
			}

			fmt.Println("Successfully rotated intermediate CA.")
			fmt.Println("The old certificate has been archived.")
			fmt.Println("You may need to restart services to pick up the new certificate if they cache it.")

			return nil
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (defaults to current colony)")
	cmd.Flags().StringVar(&certType, "type", "", "Type of intermediate to rotate (server, agent)")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm rotation operation")
	_ = cmd.MarkFlagRequired("type")

	return cmd
}
