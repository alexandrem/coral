package colony

import (
	"database/sql"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/coral-io/coral/internal/colony/ca"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/logging"
	_ "github.com/marcboeker/go-duckdb"
)

// NewCACmd creates the CA management command (RFD 022).
func NewCACmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ca",
		Short: "Manage colony certificate authority",
		Long: `Manage the embedded certificate authority for agent mTLS authentication.

The CA is used to issue and revoke certificates for agents connecting to the colony.
This implements RFD 022 - Embedded step-ca for Agent mTLS Bootstrap.`,
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
		Long:  `Display the status of the colony certificate authority including root CA fingerprint and rotation schedule.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create resolver.
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID.
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			// Load colony config.
			cfg, err := resolver.ResolveConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Initialize logger.
			logger := logging.NewWithComponent(logging.Config{
				Level:  "info",
				Pretty: true,
			}, "ca")

			// Open database.
			dbPath := fmt.Sprintf("%s/colony.db", cfg.StoragePath)
			db, err := sql.Open("duckdb", dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database: %w", err)
			}
			defer db.Close()

			// TODO: Generate a proper JWT signing key (this should be from config).
			jwtSigningKey := []byte("temporary-signing-key-change-in-production")

			// Initialize CA manager.
			_, err = ca.NewManager(db, ca.Config{
				ColonyID:      cfg.ColonyID,
				JWTSigningKey: jwtSigningKey,
			})
			if err != nil {
				return fmt.Errorf("failed to initialize CA: %w", err)
			}

			// Query CA metadata from database.
			var rootFingerprint, intermediateFingerprint string
			var createdAt, nextRotationAt sql.NullTime
			err = db.QueryRow(`
				SELECT root_fingerprint, intermediate_fingerprint, created_at, next_rotation_at
				FROM ca_metadata WHERE id = 1
			`).Scan(&rootFingerprint, &intermediateFingerprint, &createdAt, &nextRotationAt)
			if err != nil {
				return fmt.Errorf("failed to query CA metadata: %w", err)
			}

			logger.Info().
				Str("colony_id", cfg.ColonyID).
				Str("root_fingerprint", rootFingerprint).
				Str("intermediate_fingerprint", intermediateFingerprint).
				Time("created_at", createdAt.Time).
				Time("next_rotation_at", nextRotationAt.Time).
				Msg("CA status")

			// Print formatted output.
			fmt.Printf("Colony CA Status\n")
			fmt.Printf("================\n\n")
			fmt.Printf("Colony ID:              %s\n", cfg.ColonyID)
			fmt.Printf("Root Fingerprint:       %s\n", rootFingerprint)
			fmt.Printf("Intermediate Fingerprint: %s\n", intermediateFingerprint)
			if createdAt.Valid {
				fmt.Printf("Created At:             %s\n", createdAt.Time.Format("2006-01-02 15:04:05"))
			}
			if nextRotationAt.Valid {
				fmt.Printf("Next Rotation:          %s\n", nextRotationAt.Time.Format("2006-01-02 15:04:05"))
			}

			// Query certificate statistics.
			var totalCerts, activeCerts, revokedCerts int
			err = db.QueryRow(`
				SELECT
					COUNT(*) as total,
					SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END) as active,
					SUM(CASE WHEN status = 'revoked' THEN 1 ELSE 0 END) as revoked
				FROM issued_certificates
			`).Scan(&totalCerts, &activeCerts, &revokedCerts)
			if err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("failed to query certificate statistics: %w", err)
			}

			fmt.Printf("\nCertificate Statistics\n")
			fmt.Printf("======================\n")
			fmt.Printf("Total Issued:           %d\n", totalCerts)
			fmt.Printf("Active:                 %d\n", activeCerts)
			fmt.Printf("Revoked:                %d\n", revokedCerts)

			return nil
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (defaults to current colony)")

	return cmd
}

func newCARotateCmd() *cobra.Command {
	var colonyID string
	var confirm bool

	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Rotate the intermediate CA certificate",
		Long: `Rotate the intermediate CA certificate with zero-downtime issuance.

This command generates a new intermediate CA certificate signed by the root CA.
The old intermediate remains valid until its expiration, allowing for gradual migration.

WARNING: This is a sensitive operation. Use --confirm to proceed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return fmt.Errorf("CA rotation requires --confirm flag to proceed")
			}

			// Create resolver.
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID.
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			// Load colony config.
			cfg, err := resolver.ResolveConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Initialize logger.
			logger := logging.NewWithComponent(logging.Config{
				Level:  "info",
				Pretty: true,
			}, "ca")

			logger.Info().
				Str("colony_id", cfg.ColonyID).
				Msg("Starting CA intermediate rotation")

			// TODO: Implement actual rotation logic.
			// This would involve:
			// 1. Loading the current root CA
			// 2. Generating a new intermediate CA
			// 3. Signing the new intermediate with the root
			// 4. Storing the new intermediate in the database
			// 5. Updating the next_rotation_at timestamp

			return fmt.Errorf("CA rotation not yet implemented - see RFD 022 Phase 1")
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (defaults to current colony)")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm rotation operation")

	return cmd
}
