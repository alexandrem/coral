package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/constants"
)

// refreshAgentServices keeps registration fallback distinct from live check results.
func refreshAgentServices(ctx context.Context, agent *colonyv1.Agent, client agentv1connect.AgentServiceClient) {
	agent.ServicesSource = "cached"
	agent.ServiceStatuses = nil
	agent.ServicesObservedAt = nil
	agent.ServicesError = ""
	if agent.Status != "healthy" && agent.Status != "degraded" {
		agent.ServicesError = "live query skipped: agent heartbeat is " + agent.Status
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, constants.DefaultColonyRealtimeQueryTimeout)
	defer cancel()
	resp, err := client.ListServices(queryCtx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	if err != nil {
		agent.ServicesError = err.Error()
		return
	}
	agent.Services = make([]*meshv1.ServiceInfo, 0, len(resp.Msg.Services))
	for _, service := range resp.Msg.Services {
		agent.Services = append(agent.Services, &meshv1.ServiceInfo{
			Name: service.Name, Port: service.Port, HealthEndpoint: service.HealthEndpoint,
			ServiceType: service.ServiceType, Labels: service.Labels, ProcessId: service.ProcessId,
			BinaryPath: service.BinaryPath, BinaryHash: service.BinaryHash,
		})
	}
	agent.ServiceStatuses = resp.Msg.Services
	agent.ServicesObservedAt = timestamppb.New(time.Now())
	agent.ServicesSource = "live"
}
