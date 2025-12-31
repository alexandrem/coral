// Package errors provides utilities for error handling in Coral.
package errors

import (
	"database/sql"
	"fmt"
	"io"

	"github.com/rs/zerolog"
)

// DeferClose properly closes an io.Closer with logging.
// Use this in defer statements to avoid suppressing close errors.
func DeferClose(logger zerolog.Logger, closer io.Closer, msg string) {
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil {
		logger.Warn().Err(err).Msg(msg)
	}
}

// DeferRollback properly rolls back a transaction with logging.
// Use this in defer statements to ensure cleanup errors are logged.
// Ignores sql.ErrTxDone which is expected after successful commits.
func DeferRollback(logger zerolog.Logger, tx *sql.Tx) {
	if tx == nil {
		return
	}
	if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
		logger.Warn().Err(err).Msg("transaction rollback failed")
	}
}

// Must panics if error is not nil.
// Use only for initialization code where failure should halt the program.
func Must(err error, msg string) {
	if err != nil {
		panic(fmt.Sprintf("%s: %v", msg, err))
	}
}
