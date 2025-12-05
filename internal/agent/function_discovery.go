package agent

import (
	"database/sql"

	"github.com/coral-mesh/coral/internal/agent/debug"
	"github.com/rs/zerolog"
)

// FunctionCache wraps the internal function cache implementation.
type FunctionCache = debug.FunctionCache

// NewFunctionCache creates a new function cache with agent's DuckDB.
func NewFunctionCache(db *sql.DB, logger zerolog.Logger) (*FunctionCache, error) {
	return debug.NewFunctionCache(db, logger)
}
