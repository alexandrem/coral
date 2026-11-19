package startup

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/discovery"
	"github.com/coral-mesh/coral/internal/logging"
)

func TestHeartbeatStatusRecordsActualResponse(t *testing.T) {
	const (
		healthy = iota
		wrongService
		rejected
		rpcError
		slowService
	)
	var outcome atomic.Int32
	slowResponse := make(chan struct{})
	heartbeatHandler := connect.NewUnaryHandler(
		meshv1connect.MeshServiceHeartbeatProcedure,
		func(_ context.Context, _ *connect.Request[meshv1.HeartbeatRequest]) (*connect.Response[meshv1.HeartbeatResponse], error) {
			if outcome.Load() == rpcError {
				return nil, connect.NewError(connect.CodeUnavailable, errors.New("registry unavailable"))
			}
			return connect.NewResponse(&meshv1.HeartbeatResponse{Ok: outcome.Load() == healthy}), nil
		},
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch outcome.Load() {
		case wrongService:
			http.Error(w, "Port 9000 is for clickhouse-client program", http.StatusBadRequest)
		case slowService:
			<-slowResponse
		default:
			heartbeatHandler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()

	addr := server.Listener.Addr().(*net.TCPAddr)
	colony := &discovery.LookupColonyResponse{
		MeshIPv4:    addr.IP.String(),
		ConnectPort: uint32(addr.Port), // #nosec G115 -- TCP ports fit in uint32.
	}
	cm := NewConnectionManager("agent", colony, nil, nil, "", nil, nil, zerolog.Nop())
	require.Equal(t, "not_attempted", cm.GetHeartbeatStatus().Result)
	require.True(t, cm.sendHeartbeat(t.Context()))
	success := cm.GetHeartbeatStatus()
	require.Equal(t, "healthy", success.Result)
	require.Equal(t, server.URL, success.Target)
	require.NotNil(t, success.LastAttempt)
	require.NotNil(t, success.LastSuccess)

	for _, tc := range []struct {
		outcome    int32
		result     string
		httpStatus int32
		rpcCode    string
	}{
		{outcome: wrongService, result: "unexpected_http_response", httpStatus: http.StatusBadRequest},
		{outcome: rejected, result: "rejected", httpStatus: http.StatusOK},
		{outcome: rpcError, result: "rpc_error", httpStatus: http.StatusServiceUnavailable, rpcCode: "unavailable"},
	} {
		t.Run(tc.result, func(t *testing.T) {
			outcome.Store(tc.outcome)
			require.False(t, cm.sendHeartbeat(t.Context()))
			status := cm.GetHeartbeatStatus()
			require.Equal(t, server.URL, status.Target)
			require.Equal(t, tc.result, status.Result)
			require.Equal(t, tc.httpStatus, status.HttpStatus)
			require.Equal(t, tc.rpcCode, status.RpcCode)
			require.GreaterOrEqual(t, status.LastAttempt.AsTime().UnixNano(), success.LastSuccess.AsTime().UnixNano())
			require.Equal(t, success.LastSuccess.AsTime(), status.LastSuccess.AsTime())
		})
	}

	outcome.Store(slowService)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	sent := cm.sendHeartbeat(ctx)
	close(slowResponse)
	require.False(t, sent)
	require.Equal(t, "timeout", cm.GetHeartbeatStatus().Result)

	outcome.Store(healthy)
	require.True(t, cm.sendHeartbeat(t.Context()))
	recovered := cm.GetHeartbeatStatus()
	require.Equal(t, "healthy", recovered.Result)
	require.Empty(t, recovered.RpcCode)
	require.Greater(t, recovered.LastSuccess.AsTime().UnixNano(), success.LastSuccess.AsTime().UnixNano())

	server.Close()
	require.False(t, cm.sendHeartbeat(t.Context()))
	refused := cm.GetHeartbeatStatus()
	require.Equal(t, "connection_refused", refused.Result)
	require.Zero(t, refused.HttpStatus)
	require.Equal(t, recovered.LastSuccess.AsTime(), refused.LastSuccess.AsTime())
}

func TestApplyBootstrapRegistration(t *testing.T) {
	cm := &ConnectionManager{
		state:  StateUnregistered,
		logger: zerolog.Nop(),
	}

	err := cm.ApplyBootstrapRegistration(&meshv1.RegisterResponse{
		Accepted:   true,
		AssignedIp: "100.64.0.42",
		MeshSubnet: "100.64.0.0/10",
	})
	if err != nil {
		t.Fatalf("ApplyBootstrapRegistration() error = %v", err)
	}
	if cm.GetState() != StateRegistered {
		t.Fatalf("state = %s, want registered", cm.GetState())
	}
	ip, subnet := cm.GetAssignedIP()
	if ip != "100.64.0.42" || subnet != "100.64.0.0/10" {
		t.Fatalf("assignment = %s/%s, want 100.64.0.42/100.64.0.0/10", ip, subnet)
	}
	if endpoint := cm.GetCurrentEndpoint(); endpoint != "" {
		t.Fatalf("endpoint = %q, want empty for WireGuard roaming", endpoint)
	}
}

func TestWaitForDiscoveryRetriesUntilColonyAppears(t *testing.T) {
	cm := &ConnectionManager{
		config: &config.ResolvedConfig{ColonyID: "test-colony"},
		state:  StateWaitingDiscovery,
		logger: zerolog.Nop(),
		discoveryBackoff: &ExponentialBackoff{
			InitialInterval: time.Millisecond,
			MaxInterval:     time.Millisecond,
			Multiplier:      1,
		},
	}

	attempts := 0
	cm.discoveryLookup = func(*config.ResolvedConfig, logging.Logger) (*discovery.LookupColonyResponse, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("colony not found")
		}
		return &discovery.LookupColonyResponse{Pubkey: "colony-key"}, nil
	}

	info, err := cm.WaitForDiscovery(context.Background())
	if err != nil {
		t.Fatalf("WaitForDiscovery() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if info.Pubkey != "colony-key" {
		t.Fatalf("pubkey = %q, want colony-key", info.Pubkey)
	}
	if cm.GetState() != StateUnregistered {
		t.Fatalf("state = %s, want unregistered", cm.GetState())
	}
}

func TestWaitForDiscoveryStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cm := &ConnectionManager{
		config: &config.ResolvedConfig{ColonyID: "test-colony"},
		state:  StateWaitingDiscovery,
		logger: zerolog.Nop(),
		discoveryBackoff: &ExponentialBackoff{
			InitialInterval: time.Hour,
			MaxInterval:     time.Hour,
			Multiplier:      1,
		},
		discoveryLookup: func(*config.ResolvedConfig, logging.Logger) (*discovery.LookupColonyResponse, error) {
			cancel()
			return nil, errors.New("colony not found")
		},
	}

	_, err := cm.WaitForDiscovery(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForDiscovery() error = %v, want context.Canceled", err)
	}
}

func TestApplyBootstrapRegistrationRejectsInvalidResponse(t *testing.T) {
	tests := []struct {
		name string
		resp *meshv1.RegisterResponse
	}{
		{name: "nil", resp: nil},
		{name: "rejected", resp: &meshv1.RegisterResponse{Reason: "denied"}},
		{name: "invalid IP", resp: &meshv1.RegisterResponse{Accepted: true, AssignedIp: "bad", MeshSubnet: "100.64.0.0/10"}},
		{name: "invalid subnet", resp: &meshv1.RegisterResponse{Accepted: true, AssignedIp: "100.64.0.42", MeshSubnet: "bad"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &ConnectionManager{state: StateUnregistered, logger: zerolog.Nop()}
			if err := cm.ApplyBootstrapRegistration(tt.resp); err == nil {
				t.Fatal("ApplyBootstrapRegistration() error = nil, want error")
			}
			if cm.GetState() != StateUnregistered {
				t.Fatalf("state = %s, want unregistered", cm.GetState())
			}
		})
	}
}
