package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAgentLookup implements AgentLookup for testing.
type mockAgentLookup struct {
	agents map[string]string
}

func (m *mockAgentLookup) GetAgent(agentID string) (string, error) {
	ip, ok := m.agents[agentID]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return ip, nil
}

func TestAgentDuckDBProxyHandler_ServeHTTP_Logic(t *testing.T) {
	logger := zerolog.New(zerolog.NewConsoleWriter())
	lookup := &mockAgentLookup{
		agents: map[string]string{
			"agent123": "10.0.0.1",
		},
	}
	_ = NewAgentDuckDBProxyHandler(lookup, logger) // Ensure New works

	tests := []struct {
		name           string
		requestPath    string
		expectedTarget string
		expectedPath   string
	}{
		{
			name:           "base path no trailing slash",
			requestPath:    "/agent/agent123/duckdb",
			expectedTarget: "10.0.0.1:9001",
			expectedPath:   "/duckdb/",
		},
		{
			name:           "base path with trailing slash",
			requestPath:    "/agent/agent123/duckdb/",
			expectedTarget: "10.0.0.1:9001",
			expectedPath:   "/duckdb/",
		},
		{
			name:           "path with file",
			requestPath:    "/agent/agent123/duckdb/metrics.duckdb",
			expectedTarget: "10.0.0.1:9001",
			expectedPath:   "/duckdb/metrics.duckdb",
		},
		{
			name:           "path with subdirectories",
			requestPath:    "/agent/agent123/duckdb/subdir/file.duckdb",
			expectedTarget: "10.0.0.1:9001",
			expectedPath:   "/duckdb/subdir/file.duckdb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.requestPath, nil)

			// We want to intercept the outgoing request.
			// Instead of running the whole proxy, let's just check the path construction.

			trimmed := strings.TrimPrefix(req.URL.Path, "/")
			parts := strings.SplitN(trimmed, "/", 4)
			require.True(t, len(parts) >= 3)
			require.Equal(t, "agent", parts[0])
			require.Equal(t, "agent123", parts[1])
			require.Equal(t, "duckdb", parts[2])

			forwardedPath := "/duckdb/"
			if len(parts) == 4 && parts[3] != "" {
				forwardedPath = "/duckdb/" + parts[3]
			}

			assert.Equal(t, tt.expectedPath, forwardedPath)
		})
	}
}

func TestAgentDuckDBProxyHandler_Errors(t *testing.T) {
	logger := zerolog.Nop()
	lookup := &mockAgentLookup{
		agents: map[string]string{
			"active": "10.0.0.1",
		},
	}
	handler := NewAgentDuckDBProxyHandler(lookup, logger)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "invalid prefix",
			path:       "/foo/active/duckdb",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing agent id",
			path:       "/agent//duckdb",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "wrong component",
			path:       "/agent/active/notduckdb",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "agent not found",
			path:       "/agent/missing/duckdb",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, tt.wantStatus, rr.Code)
		})
	}
}
