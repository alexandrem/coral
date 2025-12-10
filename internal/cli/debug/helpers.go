package debug

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// getColonyURL returns the colony URL using shared config resolution.
// Delegates to the shared CLI helper to avoid duplication.
func getColonyURL() (string, error) {
	return helpers.GetColonyURL("")
}

// parseDuration parses a duration string and returns a protobuf Duration.
func parseDuration(s string) (*durationpb.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, fmt.Errorf("invalid duration format: %w", err)
	}
	return durationpb.New(d), nil
}
