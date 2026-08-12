package httpapi

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newProxyWithPort creates an AgentDuckDBProxyHandler that targets the given port.
// Used in tests to point the proxy at a non-9001 test server.
func newProxyWithPort(lookup AgentLookup, port string) *AgentDuckDBProxyHandler {
	return &AgentDuckDBProxyHandler{
		registry:  lookup,
		logger:    zerolog.Nop(),
		agentPort: port,
	}
}

// TestAgentProxy_VortexStatusCodePassthrough verifies that the proxy propagates
// the agent's HTTP status codes (200, 501, 413) unchanged to the caller (RFD 097).
func TestAgentProxy_VortexStatusCodePassthrough(t *testing.T) {
	tests := []struct {
		name           string
		agentStatus    int
		agentBody      string
		requestPath    string
		wantStatus     int
		wantBodySubstr string
	}{
		{
			name:           "vortex 200 propagated",
			agentStatus:    http.StatusOK,
			agentBody:      "fake-vortex-bytes",
			requestPath:    "/agent/agent123/vortex/beyla/beyla_http_metrics_local",
			wantStatus:     http.StatusOK,
			wantBodySubstr: "fake-vortex-bytes",
		},
		{
			name:           "vortex 501 propagated",
			agentStatus:    http.StatusNotImplemented,
			agentBody:      "Vortex extension unavailable on this agent",
			requestPath:    "/agent/agent123/vortex/beyla/some_table",
			wantStatus:     http.StatusNotImplemented,
			wantBodySubstr: "unavailable",
		},
		{
			name:           "vortex 413 propagated",
			agentStatus:    http.StatusRequestEntityTooLarge,
			agentBody:      `{"available_bytes":1000,"projected_bytes":2000}`,
			requestPath:    "/agent/agent123/vortex/beyla/some_table",
			wantStatus:     http.StatusRequestEntityTooLarge,
			wantBodySubstr: "available_bytes",
		},
		{
			name:           "duckdb 200 still works",
			agentStatus:    http.StatusOK,
			agentBody:      `{"databases":["metrics"],"vortex_enabled":true}`,
			requestPath:    "/agent/agent123/duckdb/",
			wantStatus:     http.StatusOK,
			wantBodySubstr: `"vortex_enabled"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Start a mock agent server that returns the configured response.
			agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.agentStatus)
				_, _ = fmt.Fprint(w, tt.agentBody)
			}))
			defer agentServer.Close()

			// Extract the port the test agent is listening on.
			_, agentPort, err := net.SplitHostPort(agentServer.Listener.Addr().String())
			require.NoError(t, err)

			// Build proxy pointing at the test agent's port.
			lookup := &mockAgentLookup{agents: map[string]string{"agent123": "127.0.0.1"}}
			handler := newProxyWithPort(lookup, agentPort)

			req := httptest.NewRequest(http.MethodGet, tt.requestPath, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.wantStatus, rr.Code)
			if tt.wantBodySubstr != "" {
				assert.Contains(t, rr.Body.String(), tt.wantBodySubstr)
			}
		})
	}
}

// TestAgentProxy_VortexPathRewriting verifies that the proxy correctly rewrites
// /agent/{id}/vortex/* paths to /vortex/* on the agent.
func TestAgentProxy_VortexPathRewriting(t *testing.T) {
	tests := []struct {
		requestPath   string
		wantAgentPath string
	}{
		{"/agent/a/vortex/beyla/table1", "/vortex/beyla/table1"},
		{"/agent/a/vortex/beyla", "/vortex/beyla"},
		{"/agent/a/vortex/", "/vortex/"},
		{"/agent/a/duckdb/metrics.duckdb", "/duckdb/metrics.duckdb"},
		{"/agent/a/duckdb/", "/duckdb/"},
	}

	for _, tt := range tests {
		t.Run(tt.requestPath, func(t *testing.T) {
			receivedPath := ""
			agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			defer agentServer.Close()

			_, agentPort, err := net.SplitHostPort(agentServer.Listener.Addr().String())
			require.NoError(t, err)

			lookup := &mockAgentLookup{agents: map[string]string{"a": "127.0.0.1"}}
			handler := newProxyWithPort(lookup, agentPort)

			req := httptest.NewRequest(http.MethodGet, tt.requestPath, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, tt.wantAgentPath, receivedPath)
		})
	}
}

// TestAgentProxy_VortexQueryStringForwarded verifies that ?query= parameters are
// forwarded to the agent without modification.
func TestAgentProxy_VortexQueryStringForwarded(t *testing.T) {
	receivedQuery := ""
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer agentServer.Close()

	_, agentPort, err := net.SplitHostPort(agentServer.Listener.Addr().String())
	require.NoError(t, err)

	lookup := &mockAgentLookup{agents: map[string]string{"agent123": "127.0.0.1"}}
	handler := newProxyWithPort(lookup, agentPort)

	rawQuery := "query=SELECT+%2A+FROM+beyla_http_metrics_local"
	req := httptest.NewRequest(http.MethodGet, "/agent/agent123/vortex/beyla?"+rawQuery, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, rawQuery, receivedQuery, "query string must be forwarded unchanged")
}
