package colony

import (
	"database/sql"
	"fmt"

	"github.com/coral-io/coral/internal/colony/ca"
)

// InitializeCA initializes the CA manager for the colony.
// This is a helper function for colony startup (RFD 022).
func InitializeCA(db *sql.DB, colonyID string, jwtSigningKey []byte) (*ca.Manager, error) {
	caManager, err := ca.NewManager(db, ca.Config{
		ColonyID:      colonyID,
		JWTSigningKey: jwtSigningKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create CA manager: %w", err)
	}

	return caManager, nil
}
