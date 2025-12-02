package debug

import (
	"github.com/coral-mesh/coral/internal/cli/shared"
)

// getColonyURL returns the colony URL using shared config resolution.
// Delegates to the shared CLI helper to avoid duplication.
func getColonyURL() (string, error) {
	return shared.GetColonyURL("")
}
