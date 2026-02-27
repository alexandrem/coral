package httpapi

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/rs/zerolog"
)

// AgentLookup resolves an agent ID to its WireGuard mesh IPv4 address.
type AgentLookup interface {
	GetAgent(agentID string) (meshIP string, err error)
}

// AgentDuckDBProxyHandler reverse-proxies DuckDB HTTP requests from the colony's
// public endpoint to the target agent's DuckDB server on the WireGuard mesh (RFD 095).
//
// Routes: /agent/{agentID}/duckdb[/{rest}]
//
// The /agent/{agentID} prefix is stripped before forwarding; the agent receives the
// request at its own /duckdb[/*] path on port 9001.
type AgentDuckDBProxyHandler struct {
	registry AgentLookup
	logger   zerolog.Logger
}

// NewAgentDuckDBProxyHandler creates a proxy handler for /agent/{id}/duckdb/* requests.
func NewAgentDuckDBProxyHandler(registry AgentLookup, logger zerolog.Logger) *AgentDuckDBProxyHandler {
	return &AgentDuckDBProxyHandler{
		registry: registry,
		logger:   logger.With().Str("component", "agent-duckdb-proxy").Logger(),
	}
}

// ServeHTTP proxies an incoming /agent/{agentID}/duckdb/* request to the agent.
func (h *AgentDuckDBProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Path format: /agent/{agentID}/duckdb[/{rest}]
	// Split into at most 4 parts: ["agent", "{id}", "duckdb", "{rest}"]
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(trimmed, "/", 4)

	if len(parts) < 3 || parts[0] != "agent" || parts[2] != "duckdb" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	agentID := parts[1]
	if agentID == "" {
		http.Error(w, "missing agent ID", http.StatusBadRequest)
		return
	}

	meshIP, err := h.registry.GetAgent(agentID)
	if err != nil {
		h.logger.Warn().
			Str("agent_id", agentID).
			Err(err).
			Msg("Agent not found for DuckDB proxy request")
		http.Error(w, fmt.Sprintf("agent not found: %s", agentID), http.StatusNotFound)
		return
	}

	// Build target URL: http://{meshIP}:9001
	target := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(meshIP, "9001"),
	}

	// Rewrite path: strip /agent/{agentID} prefix, keep /duckdb/[{rest}].
	// Always use a trailing slash on the base path to prevent Go's http.ServeMux from
	// issuing a 301 redirect for /duckdb → /duckdb/. The ReverseProxy passes that
	// redirect back to the CLI with a relative Location header, which the CLI resolves
	// against the original proxy URL and ends up hitting the colony's own /duckdb/
	// endpoint instead of the agent's.
	forwardedPath := "/duckdb/"
	if len(parts) == 4 && parts[3] != "" {
		// If there is a rest of the path, join it but ensure we don't accidentally
		// strip the base slash if parts[3] is empty (already handled by the if).
		forwardedPath = "/duckdb/" + parts[3]
	}

	h.logger.Debug().
		Str("agent_id", agentID).
		Str("mesh_ip", meshIP).
		Str("forwarded_path", forwardedPath).
		Msg("Proxying DuckDB request to agent")

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = forwardedPath
		req.URL.RawPath = ""
		// Preserve query query string (important for some DuckDB features).
		req.URL.RawQuery = r.URL.RawQuery
		req.Host = target.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		h.logger.Warn().
			Str("agent_id", agentID).
			Str("mesh_ip", meshIP).
			Err(err).
			Msg("DuckDB proxy request to agent failed")
		http.Error(w, "agent unreachable", http.StatusBadGateway)
	}

	proxy.ServeHTTP(w, r)
}
