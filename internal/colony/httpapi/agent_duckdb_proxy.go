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

// AgentDuckDBProxyHandler reverse-proxies DuckDB and Vortex HTTP requests from the
// colony's public endpoint to the target agent's HTTP server on the WireGuard mesh
// (RFD 095, RFD 097).
//
// Routes:
//
//	/agent/{agentID}/duckdb[/{rest}]  → /duckdb/[{rest}]   on agent port 9001
//	/agent/{agentID}/vortex[/{rest}]  → /vortex/[{rest}]   on agent port 9001
//
// The /agent/{agentID} prefix is stripped before forwarding; the agent receives the
// request at its own path on port 9001.
type AgentDuckDBProxyHandler struct {
	registry  AgentLookup
	logger    zerolog.Logger
	agentPort string // port agents listen on; defaults to "9001"
}

// NewAgentDuckDBProxyHandler creates a proxy handler for /agent/{id}/duckdb/* and
// /agent/{id}/vortex/* requests.
func NewAgentDuckDBProxyHandler(registry AgentLookup, logger zerolog.Logger) *AgentDuckDBProxyHandler {
	return &AgentDuckDBProxyHandler{
		registry:  registry,
		logger:    logger.With().Str("component", "agent-duckdb-proxy").Logger(),
		agentPort: "9001",
	}
}

// ServeHTTP proxies an incoming /agent/{agentID}/duckdb/* or
// /agent/{agentID}/vortex/* request to the agent.
func (h *AgentDuckDBProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Path format: /agent/{agentID}/{service}[/{rest}]
	// Split into at most 4 parts: ["agent", "{id}", "{service}", "{rest}"]
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(trimmed, "/", 4)

	if len(parts) < 3 || parts[0] != "agent" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	service := parts[2]
	if service != "duckdb" && service != "vortex" {
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
			Msg("Agent not found for proxy request")
		http.Error(w, fmt.Sprintf("agent not found: %s", agentID), http.StatusNotFound)
		return
	}

	// Build target URL: http://{meshIP}:{agentPort}
	target := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(meshIP, h.agentPort),
	}

	// Rewrite path: strip /agent/{agentID} prefix, keep /{service}/[{rest}].
	// Always use a trailing slash on the base path to prevent Go's http.ServeMux from
	// issuing a 301 redirect. The ReverseProxy passes that redirect back to the CLI
	// with a relative Location header, which the CLI resolves against the original
	// proxy URL and ends up hitting the colony's own endpoint instead of the agent's.
	forwardedPath := "/" + service + "/"
	if len(parts) == 4 && parts[3] != "" {
		forwardedPath = "/" + service + "/" + parts[3]
	}

	h.logger.Debug().
		Str("agent_id", agentID).
		Str("mesh_ip", meshIP).
		Str("service", service).
		Str("forwarded_path", forwardedPath).
		Msg("Proxying request to agent")

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = forwardedPath
		req.URL.RawPath = ""
		// Preserve query string (important for ?query= and DuckDB features).
		req.URL.RawQuery = r.URL.RawQuery
		req.Host = target.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		h.logger.Warn().
			Str("agent_id", agentID).
			Str("mesh_ip", meshIP).
			Str("service", service).
			Err(err).
			Msg("Proxy request to agent failed")
		http.Error(w, "agent unreachable", http.StatusBadGateway)
	}

	proxy.ServeHTTP(w, r)
}
