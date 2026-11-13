package helpers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

func TestChecksPreserveUnknownAndUnconfiguredHealth(t *testing.T) {
	old := timestamppb.New(time.Now().Add(-45 * time.Minute))
	statuses := []*agentv1.ServiceStatus{
		{HasMonitor: true, Status: "healthy", LastCheck: old},
		{HasMonitor: true, Status: "healthy"}, // No result yet, even if a status was supplied.
		{HasMonitor: false, Status: "healthy"},
	}
	live := AgentChecks(statuses, 3, "live")
	require.Equal(t, "unknown", live.Status())
	require.Equal(t, 1, live.Passing)
	require.Equal(t, 1, live.Unknown)
	require.Equal(t, 1, live.NotConfigured)
	require.Same(t, old, live.LastCheck, "keep the actual check time, not the fetch time")
	cached := AgentChecks(statuses, 3, "cached")
	require.Equal(t, "unknown", cached.Status())
	require.Equal(t, 3, cached.Unknown)
	require.Zero(t, cached.Passing)
	require.Nil(t, cached.LastCheck)
}
