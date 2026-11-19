package startup

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-mesh/coral/internal/agent/heartbeat"
	"github.com/coral-mesh/coral/internal/constants"
)

// GetHeartbeatStatus returns a snapshot of the latest completed heartbeat.
func (cm *ConnectionManager) GetHeartbeatStatus() *agentv1.ColonyHeartbeatStatus {
	cm.stateMu.RLock()
	defer cm.stateMu.RUnlock()
	if cm.heartbeatStatus == nil {
		return &agentv1.ColonyHeartbeatStatus{Result: "not_attempted"}
	}
	return proto.CloneOf(cm.heartbeatStatus)
}

// StartHeartbeatLoop sends periodic heartbeats to the colony and monitors connection health.
func (cm *ConnectionManager) StartHeartbeatLoop(ctx context.Context, interval time.Duration) {
	cm.logger.Info().Dur("interval", interval).Msg("Starting heartbeat loop")
	cm.sendHeartbeat(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			cm.logger.Info().Msg("Heartbeat loop stopping")
			return
		case <-ticker.C:
			if !cm.sendHeartbeat(ctx) && cm.consecutiveFailures >= 3 {
				cm.logger.Warn().
					Int("consecutive_failures", cm.consecutiveFailures).
					Msg("Multiple heartbeat failures detected - triggering reconnection")
				cm.setState(StateUnregistered)
				cm.triggerReconnection()
			}
		}
	}
}

func (cm *ConnectionManager) sendHeartbeat(ctx context.Context) bool {
	colonyInfo := cm.GetColonyInfo()
	if colonyInfo == nil {
		return false
	}

	connectPort := colonyInfo.ConnectPort
	if connectPort == 0 {
		connectPort = constants.DefaultColonyPort
	}
	target := "http://" + net.JoinHostPort(colonyInfo.MeshIPv4, strconv.FormatUint(uint64(connectPort), 10))
	attempt := &agentv1.ColonyHeartbeatStatus{
		Target:      target,
		LastAttempt: timestamppb.Now(),
	}
	httpClient := &heartbeatHTTPClient{}
	client := meshv1connect.NewMeshServiceClient(httpClient, target)

	heartbeatCtx, cancel := context.WithTimeout(ctx, constants.DefaultHeartbeatTimeout)
	defer cancel()
	resp, err := heartbeat.NewAgent(cm.agentID, client).SendHeartbeat(heartbeatCtx)

	attempt.HttpStatus = httpClient.status
	accepted := err == nil && resp.GetOk()
	switch {
	case accepted:
		attempt.Result = "healthy"
		attempt.LastSuccess = timestamppb.Now()
	case err == nil:
		attempt.Result = "rejected"
	case connect.IsWireError(err):
		attempt.Result = "rpc_error"
		attempt.RpcCode = connect.CodeOf(err).String()
	case connect.CodeOf(err) == connect.CodeDeadlineExceeded:
		attempt.Result = "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		attempt.Result = "connection_refused"
	case attempt.HttpStatus != 0:
		attempt.Result = "unexpected_http_response"
	default:
		attempt.Result = "request_failed"
	}

	cm.stateMu.Lock()
	if !accepted {
		attempt.LastSuccess = cm.heartbeatStatus.GetLastSuccess()
	}
	cm.heartbeatStatus = attempt
	cm.stateMu.Unlock()

	if !accepted {
		cm.consecutiveFailures++
		cm.logger.Warn().
			Err(err).
			Str("agent_id", cm.agentID).
			Str("target", target).
			Str("result", attempt.Result).
			Int32("http_status", attempt.HttpStatus).
			Int("consecutive_failures", cm.consecutiveFailures).
			Msg("Heartbeat failed")
		return false
	}

	cm.consecutiveFailures = 0
	cm.setState(StateHealthy)
	cm.backoff.Reset()

	// A working heartbeat confirms the current WireGuard endpoint.
	currentEndpoint := cm.GetCurrentEndpoint()
	if currentEndpoint != "" && currentEndpoint != cm.GetLastSuccessfulEndpoint() {
		cm.SetLastSuccessfulEndpoint(currentEndpoint)
	}

	cm.logger.Debug().Str("agent_id", cm.agentID).Msg("Heartbeat sent successfully")
	return true
}

// heartbeatHTTPClient records the HTTP status for a single unary heartbeat RPC.
type heartbeatHTTPClient struct {
	status int32
}

func (c *heartbeatHTTPClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultClient.Do(req) // #nosec G704 -- Connect builds this heartbeat request for the discovered colony mesh endpoint.
	if resp != nil {
		c.status = int32(resp.StatusCode) // #nosec G115 -- HTTP status codes are three digits.
	}
	return resp, err
}
