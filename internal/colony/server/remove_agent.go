package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/auth"
	"github.com/coral-mesh/coral/internal/colony/httpapi"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// RemoveAgent removes a decommissioned agent from the colony registry.
func (s *Server) RemoveAgent(
	ctx context.Context,
	req *connect.Request[colonyv1.RemoveAgentRequest],
) (*connect.Response[colonyv1.RemoveAgentResponse], error) {
	host, _, _ := net.SplitHostPort(req.Peer().Addr)
	token := httpapi.GetAuthenticatedToken(ctx)
	if !net.ParseIP(host).IsLoopback() && (token == nil || !auth.HasPermission(token, auth.PermissionAdmin)) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("agent removal requires a local connection or an admin API token"))
	}

	agentID := req.Msg.GetAgentId()
	if strings.TrimSpace(agentID) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("agent ID is required"))
	}
	colonyID := req.Msg.GetColonyId()
	if colonyID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("colony ID is required"))
	}
	if colonyID != s.config.ColonyID {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("connected to colony %q, expected %q", s.config.ColonyID, colonyID))
	}

	switch err := s.registry.Remove(ctx, agentID); {
	case errors.Is(err, registry.ErrAgentNotFound):
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent %q not found", agentID))
	case errors.Is(err, registry.ErrAgentActive):
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("cannot remove agent %q: %w", agentID, err))
	case errors.Is(err, registry.ErrAgentRemoving):
		return nil, connect.NewError(connect.CodeAborted, fmt.Errorf("agent %q is already being removed", agentID))
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logger.Info().Str("agent_id", agentID).Msg("Agent removed from registry")
	return connect.NewResponse(&colonyv1.RemoveAgentResponse{}), nil
}
