package colony

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/colony/ca"
	"github.com/coral-mesh/coral/internal/colony/jwks"
)

// InitializeCA initializes the CA manager for the colony.
// This is a helper function for colony startup (RFD 047).
func InitializeCA(db *sql.DB, colonyID, caDir string, jwksClient *jwks.Client, logger zerolog.Logger) (*ca.Manager, error) {
	caManager, err := ca.NewManager(db, ca.Config{
		ColonyID:   colonyID,
		CADir:      caDir,
		JWKSClient: jwksClient,
		Logger:     logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create CA manager: %w", err)
	}

	// Import bootstrap PSK from filesystem if not yet in database (RFD 088).
	if err := caManager.ImportPSKFromFile(context.Background()); err != nil {
		logger.Warn().Err(err).Msg("Failed to import bootstrap PSK from file")
	}

	return caManager, nil
}
