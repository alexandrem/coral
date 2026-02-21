package debug

import (
	"fmt"
	"time"

	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// getColonyDebugClient returns a colony debug client using shared config resolution.
func getColonyDebugClient() (colonyv1connect.ColonyDebugServiceClient, error) {
	return helpers.GetColonyDebugClient("")
}

// parseDuration parses a duration string and returns a protobuf Duration.
func parseDuration(s string) (*durationpb.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, fmt.Errorf("invalid duration format: %w", err)
	}
	return durationpb.New(d), nil
}
