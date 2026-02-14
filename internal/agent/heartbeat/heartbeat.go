// Package heartbeat provides a composable agent for sending periodic heartbeats to the colony.
//
// The Agent type uses dependency injection to accept a mesh service client,
// making it easy to test with mock clients and reusable across different contexts.
//
// Example usage:
//
//	client := meshv1connect.NewMeshServiceClient(http.DefaultClient, colonyURL)
//	agent := heartbeat.NewAgent("agent-id", client)
//	agent.StartHeartbeat(ctx, 15*time.Second)
package heartbeat

import (
	"context"
	"time"

	"connectrpc.com/connect"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
)

// Agent manages periodic heartbeats to the colony.
// It is composable and accepts a mesh client for easy testing.
type Agent struct {
	id     string
	client meshv1connect.MeshServiceClient
}

// NewAgent creates a new heartbeat agent with the given ID and mesh client.
func NewAgent(id string, client meshv1connect.MeshServiceClient) *Agent {
	return &Agent{
		id:     id,
		client: client,
	}
}

// StartHeartbeat begins the heartbeat loop with the specified interval.
// It returns only when the context is cancelled.
// Errors are ignored to ensure the loop continues even after failures.
func (a *Agent) StartHeartbeat(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Use a timeout context for each heartbeat to prevent hanging.
			heartbeatCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, _ = a.client.Heartbeat(heartbeatCtx, connect.NewRequest(&meshv1.HeartbeatRequest{
				AgentId: a.id,
				Status:  "healthy",
			}))
			cancel()
		}
	}
}

// SendHeartbeat sends a single heartbeat and returns the response and any error.
// This method is useful for explicit heartbeat attempts with error handling.
func (a *Agent) SendHeartbeat(ctx context.Context) (*meshv1.HeartbeatResponse, error) {
	resp, err := a.client.Heartbeat(ctx, connect.NewRequest(&meshv1.HeartbeatRequest{
		AgentId: a.id,
		Status:  "healthy",
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
