package colony

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// NewPSKCmd creates the PSK management command (RFD 088).
func NewPSKCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "psk",
		Short: "Manage bootstrap pre-shared key",
		Long: `Manage the bootstrap pre-shared key (PSK) used to authorize agent certificate
issuance. The PSK is generated during colony initialization and must be
distributed to agents out-of-band.

This implements RFD 088 - Bootstrap Pre-Shared Key.`,
	}

	cmd.AddCommand(newPSKShowCmd())
	cmd.AddCommand(newPSKRotateCmd())

	return cmd
}

func newPSKShowCmd() *cobra.Command {
	var colonyID string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Display the current bootstrap PSK",
		Long: `Display the current active bootstrap PSK. The PSK is decrypted from the
colony database using the Root CA private key.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, db, _, err := helpers.GetCAManager(colonyID)
			if err != nil {
				return err
			}
			defer db.Close()

			// Import PSK from file if needed.
			if err := manager.ImportPSKFromFile(context.Background()); err != nil {
				return fmt.Errorf("failed to import PSK: %w", err)
			}

			psk, err := manager.GetActivePSK(context.Background())
			if err != nil {
				return fmt.Errorf("failed to get active PSK: %w", err)
			}

			fmt.Printf("Bootstrap PSK:\n  %s\n", psk)
			return nil
		},
	}

	helpers.AddColonyFlag(cmd, &colonyID)
	return cmd
}

func newPSKRotateCmd() *cobra.Command {
	var (
		colonyID    string
		gracePeriod time.Duration
	)

	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Rotate the bootstrap PSK",
		Long: `Generate a new bootstrap PSK and move the current PSK to grace status.
During the grace period, both old and new PSKs are accepted. After the grace
period expires, only the new PSK is valid.

Existing agents with valid certificates are unaffected (renewals use mTLS).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, db, _, err := helpers.GetCAManager(colonyID)
			if err != nil {
				return err
			}
			defer db.Close()

			// Import PSK from file if needed.
			if err := manager.ImportPSKFromFile(context.Background()); err != nil {
				return fmt.Errorf("failed to import PSK: %w", err)
			}

			newPSK, err := manager.RotatePSK(context.Background(), gracePeriod)
			if err != nil {
				return fmt.Errorf("failed to rotate PSK: %w", err)
			}

			graceExpiry := time.Now().Add(gracePeriod)
			fmt.Printf("PSK rotated successfully.\n\n")
			fmt.Printf("New Bootstrap PSK:\n  %s\n\n", newPSK)
			fmt.Printf("Previous PSK valid until: %s (%s grace period)\n", graceExpiry.Format(time.RFC3339), gracePeriod)
			fmt.Printf("\nUpdate agents with the new PSK before the grace period expires.\n")
			return nil
		},
	}

	helpers.AddColonyFlag(cmd, &colonyID)
	cmd.Flags().DurationVar(&gracePeriod, "grace-period", 24*time.Hour, "Grace period during which both old and new PSKs are accepted")
	return cmd
}
