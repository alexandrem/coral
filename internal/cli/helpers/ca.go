package helpers

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "github.com/marcboeker/go-duckdb"

	"github.com/coral-mesh/coral/internal/colony/ca"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/logging"
)

// GetCAManager returns an initialized CA manager and its associated resources.
func GetCAManager(colonyID string) (*ca.Manager, *sql.DB, *config.ResolvedConfig, error) {
	// Create resolver.
	resolver, err := config.NewResolver()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create config resolver: %w", err)
	}

	// Resolve colony ID.
	if colonyID == "" {
		colonyID, err = resolver.ResolveColonyID()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
		}
	}

	// Load colony config.
	cfg, err := resolver.ResolveConfig(colonyID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to load colony config: %w", err)
	}

	// Open database.
	dbPath := fmt.Sprintf("%s/colony.db", cfg.StoragePath)
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to open database: %w", err)
	}

	// CA directory path - stored in ~/.coral/colonies/<colony-id>/ca/
	colonyDir := resolver.GetLoader().ColonyDir(cfg.ColonyID)
	caDir := filepath.Join(colonyDir, "ca")

	// TODO: Generate a proper JWT signing key (this should be from config).
	jwtSigningKey := []byte("temporary-signing-key-change-in-production")

	// Create logger for CA operations.
	logger := logging.New(logging.Config{
		Level:  "info",
		Pretty: false,
	})

	// Initialize CA manager.
	manager, err := ca.NewManager(db, ca.Config{
		ColonyID:      cfg.ColonyID,
		CADir:         caDir,
		JWTSigningKey: jwtSigningKey,
		Logger:        logger,
	})
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, fmt.Errorf("failed to initialize CA: %w", err)
	}

	return manager, db, cfg, nil
}
