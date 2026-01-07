// Package debug provides debug session orchestration for the colony.
package debug

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	debugpb "github.com/coral-mesh/coral/coral/colony/v1"

	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// QueryRouter handles query routing and result aggregation.
type QueryRouter struct {
	logger        zerolog.Logger
	registry      *registry.Registry
	db            *database.Database
	clientFactory func(connect.HTTPClient, string, ...connect.ClientOption) agentv1connect.AgentDebugServiceClient
}

// NewQueryRouter creates a new query router.
func NewQueryRouter(
	logger zerolog.Logger,
	registry *registry.Registry,
	db *database.Database,
	clientFactory func(connect.HTTPClient, string, ...connect.ClientOption) agentv1connect.AgentDebugServiceClient,
) *QueryRouter {
	return &QueryRouter{
		logger:        logger.With().Str("component", "query_router").Logger(),
		registry:      registry,
		db:            db,
		clientFactory: clientFactory,
	}
}

// QueryUprobeEvents retrieves events from a debug session.
func (qr *QueryRouter) QueryUprobeEvents(
	ctx context.Context,
	req *connect.Request[debugpb.QueryUprobeEventsRequest],
) (*connect.Response[debugpb.QueryUprobeEventsResponse], error) {
	qr.logger.Debug().
		Str("session_id", req.Msg.SessionId).
		Msg("Querying uprobe events")

	// Query session from database.
	session, err := qr.db.GetDebugSession(ctx, req.Msg.SessionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("session not found: %s", req.Msg.SessionId))
		}
		qr.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Msg("Failed to query debug session from database")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}
	if session == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("session not found: %s", req.Msg.SessionId))
	}

	// Determine if we should query from agent or database.
	var uprobeEvents []*agentv1.UprobeEvent
	sessionExpired := time.Now().After(session.ExpiresAt) || session.Status == "stopped"

	if sessionExpired {
		// Session expired or stopped - query from database (RFD 062 - event persistence).
		qr.logger.Debug().
			Str("session_id", req.Msg.SessionId).
			Msg("Querying events from database (session expired or stopped)")

		events, err := qr.db.GetDebugEvents(req.Msg.SessionId)
		if err != nil {
			qr.logger.Error().Err(err).
				Str("session_id", req.Msg.SessionId).
				Msg("Failed to query events from database")
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query events from database: %v", err))
		}

		// Filter events by time range if specified.
		for _, event := range events {
			if req.Msg.StartTime != nil && event.Timestamp.AsTime().Before(req.Msg.StartTime.AsTime()) {
				continue
			}
			if req.Msg.EndTime != nil && event.Timestamp.AsTime().After(req.Msg.EndTime.AsTime()) {
				continue
			}
			uprobeEvents = append(uprobeEvents, event)

			// Apply MaxEvents limit.
			if req.Msg.MaxEvents > 0 && len(uprobeEvents) >= int(req.Msg.MaxEvents) {
				break
			}
		}

		qr.logger.Debug().
			Str("session_id", req.Msg.SessionId).
			Int("event_count", len(uprobeEvents)).
			Msg("Retrieved events from database")
	} else {
		// Session still active - try to query from agent first, fallback to database.
		qr.logger.Debug().
			Str("session_id", req.Msg.SessionId).
			Msg("Querying events from agent (session active)")

		var agentQueryFailed bool

		entry, err := qr.registry.Get(session.AgentID)
		if err != nil {
			qr.logger.Warn().Err(err).
				Str("session_id", req.Msg.SessionId).
				Str("agent_id", session.AgentID).
				Msg("Agent not found in registry, will fallback to database")
			agentQueryFailed = true
		}

		if !agentQueryFailed {
			// Call agent to query events.
			agentAddr := buildAgentAddress(entry.MeshIPv4)
			agentClient := qr.clientFactory(
				http.DefaultClient,
				fmt.Sprintf("http://%s", agentAddr),
			)

			queryReq := connect.NewRequest(&agentv1.QueryUprobeEventsRequest{
				CollectorId: session.CollectorID,
				StartTime:   req.Msg.StartTime,
				EndTime:     req.Msg.EndTime,
				MaxEvents:   req.Msg.MaxEvents,
			})

			queryResp, err := agentClient.QueryUprobeEvents(ctx, queryReq)
			if err != nil {
				qr.logger.Warn().Err(err).
					Str("session_id", req.Msg.SessionId).
					Str("collector_id", session.CollectorID).
					Msg("Failed to query uprobe events from agent, will fallback to database")
				agentQueryFailed = true
			} else {
				// Events are already UprobeEvents (not wrapped in EbpfEvent).
				uprobeEvents = append(uprobeEvents, queryResp.Msg.Events...)

				qr.logger.Debug().
					Str("session_id", req.Msg.SessionId).
					Str("agent_id", session.AgentID).
					Int("event_count", len(uprobeEvents)).
					Msg("Retrieved uprobe events from agent")
			}
		}

		// Fallback to database if agent query failed.
		if agentQueryFailed {
			qr.logger.Debug().
				Str("session_id", req.Msg.SessionId).
				Msg("Falling back to database query for events")

			events, err := qr.db.GetDebugEvents(req.Msg.SessionId)
			if err != nil {
				qr.logger.Error().Err(err).
					Str("session_id", req.Msg.SessionId).
					Msg("Failed to query events from database")
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query events from database: %v", err))
			}

			// Filter events by time range if specified.
			for _, event := range events {
				if req.Msg.StartTime != nil && event.Timestamp.AsTime().Before(req.Msg.StartTime.AsTime()) {
					continue
				}
				if req.Msg.EndTime != nil && event.Timestamp.AsTime().After(req.Msg.EndTime.AsTime()) {
					continue
				}
				uprobeEvents = append(uprobeEvents, event)

				// Apply MaxEvents limit.
				if req.Msg.MaxEvents > 0 && len(uprobeEvents) >= int(req.Msg.MaxEvents) {
					break
				}
			}

			qr.logger.Debug().
				Str("session_id", req.Msg.SessionId).
				Int("event_count", len(uprobeEvents)).
				Msg("Retrieved events from database (fallback)")
		}
	}

	return connect.NewResponse(&debugpb.QueryUprobeEventsResponse{
		Events:  uprobeEvents,
		HasMore: false, // Database queries return all events at once
	}), nil
}

// GetDebugResults retrieves aggregated debug results.
func (qr *QueryRouter) GetDebugResults(
	ctx context.Context,
	req *connect.Request[debugpb.GetDebugResultsRequest],
) (*connect.Response[debugpb.GetDebugResultsResponse], error) {
	qr.logger.Info().
		Str("session_id", req.Msg.SessionId).
		Str("format", req.Msg.Format).
		Msg("Getting debug results")

	// Query session from database.
	session, err := qr.db.GetDebugSession(ctx, req.Msg.SessionId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("session not found: %s", req.Msg.SessionId))
		}
		qr.logger.Error().Err(err).
			Str("session_id", req.Msg.SessionId).
			Msg("Failed to query debug session from database")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}
	if session == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("session not found: %s", req.Msg.SessionId))
	}

	// Determine if we should query from agent or database.
	var uprobeEvents []*agentv1.UprobeEvent
	var processID int32
	var binaryPath string

	// Try to resolve process info from registry if agent is available.
	if entry, err := qr.registry.Get(session.AgentID); err == nil {
		for _, svc := range entry.Services {
			if svc.Name == session.ServiceName {
				processID = svc.ProcessId
				binaryPath = svc.BinaryPath
				break
			}
		}
	}

	sessionExpired := time.Now().After(session.ExpiresAt) || session.Status == "stopped"

	if sessionExpired {
		// Session expired or stopped - query from database (RFD 062 - event persistence).
		qr.logger.Info().
			Str("session_id", req.Msg.SessionId).
			Bool("expired", time.Now().After(session.ExpiresAt)).
			Str("status", session.Status).
			Msg("Querying events from database (session expired or stopped)")

		events, err := qr.db.GetDebugEvents(req.Msg.SessionId)
		if err != nil {
			qr.logger.Error().Err(err).
				Str("session_id", req.Msg.SessionId).
				Msg("Failed to query events from database")
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query events from database: %v", err))
		}
		uprobeEvents = events

		qr.logger.Info().
			Str("session_id", req.Msg.SessionId).
			Int("event_count", len(uprobeEvents)).
			Msg("Retrieved events from database")
	} else {
		// Session still active - query from agent.
		qr.logger.Info().
			Str("session_id", req.Msg.SessionId).
			Msg("Querying events from agent (session active)")

		entry, err := qr.registry.Get(session.AgentID)
		if err != nil {
			qr.logger.Error().Err(err).
				Str("session_id", req.Msg.SessionId).
				Str("agent_id", session.AgentID).
				Msg("Failed to get agent from registry")
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent not found: %v", err))
		}

		// Call agent to query uprobe events.
		agentAddr := buildAgentAddress(entry.MeshIPv4)
		agentClient := qr.clientFactory(
			http.DefaultClient,
			fmt.Sprintf("http://%s", agentAddr),
		)

		queryReq := connect.NewRequest(&agentv1.QueryUprobeEventsRequest{
			CollectorId: session.CollectorID,
			StartTime:   timestamppb.New(session.StartedAt),
			EndTime:     timestamppb.New(session.ExpiresAt),
			MaxEvents:   10000, // Limit to prevent overwhelming response
		})

		queryResp, err := agentClient.QueryUprobeEvents(ctx, queryReq)
		if err != nil {
			qr.logger.Error().Err(err).
				Str("session_id", req.Msg.SessionId).
				Str("collector_id", session.CollectorID).
				Msg("Failed to query uprobe events from agent")
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query events: %v", err))
		}

		// Events are already UprobeEvents (not wrapped in EbpfEvent).
		uprobeEvents = append(uprobeEvents, queryResp.Msg.Events...)

		qr.logger.Info().
			Str("session_id", req.Msg.SessionId).
			Int("event_count", len(uprobeEvents)).
			Msg("Retrieved uprobe events from agent")
	}

	// Aggregate statistics.
	statistics := AggregateStatistics(uprobeEvents)

	// Find slow outliers.
	p95Duration := time.Duration(0)
	if statistics.DurationP95 != nil {
		p95Duration = statistics.DurationP95.AsDuration()
	}
	slowOutliers := FindSlowOutliers(uprobeEvents, p95Duration)

	// Build call tree.
	callTree := BuildCallTreeFromEvents(uprobeEvents, p95Duration)

	// Calculate session duration.
	sessionDuration := session.ExpiresAt.Sub(session.StartedAt)

	return connect.NewResponse(&debugpb.GetDebugResultsResponse{
		SessionId:    req.Msg.SessionId,
		Function:     session.FunctionName,
		Duration:     durationpb.New(sessionDuration),
		Statistics:   statistics,
		SlowOutliers: slowOutliers,
		CallTree:     callTree,
		ProcessId:    processID,
		BinaryPath:   binaryPath,
	}), nil
}
