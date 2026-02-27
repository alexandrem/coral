package duckdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/duckdb"
)

// AgentInfo contains information about an agent with available databases.
type AgentInfo struct {
	AgentID   string
	MeshIP    string
	Databases []string
	LastSeen  string
	Status    string
}

// getColonyClient returns a colony gRPC client using the colony URL from config.
func getColonyClient() (colonyv1connect.ColonyServiceClient, error) {
	// Use shared CLI helper for colony client creation.
	return helpers.GetColonyClient("")
}

// listAgents queries the colony to get all registered agents.
// If fetchDatabases is true, queries each agent for available databases.
func listAgents(ctx context.Context, fetchDatabases bool) ([]AgentInfo, error) {
	client, err := getColonyClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create colony client: %w", err)
	}

	// Call colony to list agents.
	req := connect.NewRequest(&colonyv1.ListAgentsRequest{})
	resp, err := client.ListAgents(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}

	var agents []AgentInfo
	for _, agent := range resp.Msg.Agents {
		status := "unknown"
		if agent.Status != "" {
			status = agent.Status
		}

		// Format last seen timestamp.
		lastSeen := "never"
		if agent.LastSeen != nil {
			lastSeen = agent.LastSeen.AsTime().Format("2006-01-02 15:04:05")
		}

		agentInfo := AgentInfo{
			AgentID:  agent.AgentId,
			MeshIP:   agent.MeshIpv4,
			LastSeen: lastSeen,
			Status:   status,
		}

		// Optionally query agent for available databases.
		if fetchDatabases {
			agentBase, baseErr := agentDuckDBBase(ctx, agent.AgentId, agent.MeshIpv4)
			if baseErr == nil {
				databases, err := listAgentDatabases(ctx, agentBase)
				if err != nil {
					// Log error but don't fail - agent might be offline.
					agentInfo.Databases = []string{}
				} else {
					agentInfo.Databases = databases
				}
			}
		}

		agents = append(agents, agentInfo)
	}

	return agents, nil
}

// shouldUseColonyProxy returns true when agent DuckDB access should be routed
// through the colony's /agent/{id}/duckdb proxy (RFD 095).
//
// The signal is the URL scheme: https:// means the CLI is talking to the public
// endpoint (RFD 031), which has TLS but no direct WireGuard mesh membership.
// http:// means the internal colony server, reachable only from the same host,
// where the CLI can also reach agents directly over the mesh.
func shouldUseColonyProxy(baseURL string) bool {
	return strings.HasPrefix(baseURL, "https://")
}

// agentDuckDBBase returns the base URL for accessing an agent's DuckDB server.
//
// When the colony URL is HTTPS (public endpoint), requests are routed through
// the colony's /agent/{id}/duckdb proxy (RFD 095). When HTTP (internal server),
// the agent's mesh IP is used directly.
//
// knownMeshIP may be provided to skip an extra colony round-trip in local mode (e.g.,
// when called from listAgents where the mesh IP is already in scope).
func agentDuckDBBase(ctx context.Context, agentID string, knownMeshIP string) (string, error) {
	baseURL, err := resolveColonyBaseURL()
	if err != nil {
		return "", err
	}

	if shouldUseColonyProxy(baseURL) {
		// Route through the colony proxy.
		// Use HTTP attach base to avoid TLS issues with DuckDB's httpfs on self-signed certs.
		attachBase := duckdbAttachBase(baseURL)
		return strings.TrimRight(attachBase, "/") + "/agent/" + agentID, nil
	}

	// Local mode: connect directly to the agent's mesh IP.
	meshIP := knownMeshIP
	if meshIP == "" {
		// Resolve via colony registry (only needed when meshIP wasn't passed in).
		meshIP, err = resolveAgentMeshIP(ctx, agentID)
		if err != nil {
			return "", err
		}
	}
	return "http://" + net.JoinHostPort(meshIP, "9001"), nil
}

// resolveAgentMeshIP resolves an agent ID to its WireGuard mesh IP via the colony registry.
func resolveAgentMeshIP(ctx context.Context, agentID string) (string, error) {
	agents, err := listAgents(ctx, false)
	if err != nil {
		return "", fmt.Errorf("failed to list agents: %w", err)
	}

	for _, agent := range agents {
		if agent.AgentID == agentID {
			if agent.MeshIP == "" {
				return "", fmt.Errorf("agent %s has no mesh IP address", agentID)
			}
			return agent.MeshIP, nil
		}
	}

	return "", fmt.Errorf("agent %s not found in colony registry", agentID)
}

// createDuckDBConnection creates a DuckDB connection with httpfs extension loaded.
func createDuckDBConnection(ctx context.Context) (*sql.DB, error) {
	db, err := duckdb.OpenDB("")
	if err != nil {
		return nil, fmt.Errorf("failed to open DuckDB connection: %w", err)
	}

	// Load httpfs extension for HTTP remote attach.
	if _, err := db.ExecContext(ctx, "INSTALL httpfs;"); err != nil {
		_ = db.Close() // TODO: errcheck
		return nil, fmt.Errorf("failed to install httpfs extension: %w", err)
	}

	if _, err := db.ExecContext(ctx, "LOAD httpfs;"); err != nil {
		_ = db.Close() // TODO: errcheck
		return nil, fmt.Errorf("failed to load httpfs extension: %w", err)
	}

	return db, nil
}

// attachAgentDatabase attaches a remote agent database to the DuckDB connection.
// agentBase is the base URL for the agent's DuckDB server (from agentDuckDBBase).
func attachAgentDatabase(ctx context.Context, db *sql.DB, agentID string, agentBase string, dbName string) error {
	dbURL := strings.TrimRight(agentBase, "/") + "/duckdb/" + dbName

	// Use agent ID as database alias (with agent_ prefix to ensure valid identifier).
	alias := fmt.Sprintf("agent_%s", sanitizeAgentID(agentID))
	attachSQL := fmt.Sprintf("ATTACH '%s' AS %s (READ_ONLY);", dbURL, alias)

	if _, err := db.ExecContext(ctx, attachSQL); err != nil {
		return fmt.Errorf("failed to attach database from %s: %w", dbURL, err)
	}

	return nil
}

// sanitizeAgentID converts an agent ID to a valid DuckDB identifier.
// Replaces dashes and dots with underscores.
func sanitizeAgentID(agentID string) string {
	result := ""
	for _, ch := range agentID {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			result += string(ch)
		} else {
			result += "_"
		}
	}
	return result
}

// listAgentDatabases queries an agent for available databases.
// agentBase is the base URL for the agent's DuckDB server (from agentDuckDBBase).
func listAgentDatabases(ctx context.Context, agentBase string) ([]string, error) {
	listURL := strings.TrimRight(agentBase, "/") + "/duckdb/"

	// Use TLS-capable client when routing through the HTTPS colony proxy.
	var httpClient *http.Client
	if strings.HasPrefix(agentBase, "https://") {
		var err error
		httpClient, err = helpers.BuildHTTPClient("", agentBase)
		if err != nil {
			httpClient = http.DefaultClient
		}
	} else {
		httpClient = http.DefaultClient
	}

	// Make HTTP request.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query agent: %w", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close() // TODO: errcheck
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent returned status %d", resp.StatusCode)
	}

	// Parse JSON response.
	var result struct {
		Databases []string `json:"databases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Databases, nil
}

// resolveInternalColonyURL returns the internal HTTP colony URL (http://localhost:{port})
// by reading the colony config directly, bypassing CORAL_COLONY_ENDPOINT.
// Used for DuckDB ATTACH on localhost setups to avoid TLS certificate issues with httpfs.
func resolveInternalColonyURL() (string, error) {
	resolver, err := config.NewResolver()
	if err != nil {
		return "", fmt.Errorf("failed to create config resolver: %w", err)
	}
	colonyID, err := resolver.ResolveColonyID()
	if err != nil {
		return "", fmt.Errorf("failed to resolve colony ID: %w", err)
	}
	loader := resolver.GetLoader()
	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return "", fmt.Errorf("failed to load colony config: %w", err)
	}
	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = constants.DefaultColonyPort
	}
	return fmt.Sprintf("http://localhost:%d", connectPort), nil
}

// duckdbAttachBase returns an HTTP base URL safe for DuckDB ATTACH.
// DuckDB's httpfs cannot verify self-signed TLS certificates, so for localhost
// HTTPS endpoints we fall back to the internal plain-HTTP server.
func duckdbAttachBase(configuredURL string) string {
	if !strings.HasPrefix(configuredURL, "https://") {
		return configuredURL
	}
	// HTTPS: check if colony is on localhost — if so, use the internal HTTP port.
	u, err := url.Parse(configuredURL)
	if err != nil {
		return configuredURL
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		if internalURL, err := resolveInternalColonyURL(); err == nil {
			return internalURL
		}
	}
	// Remote HTTPS: return as-is; requires a CA trusted by the system.
	return configuredURL
}

// resolveColonyBaseURL returns the colony base URL from config, including scheme.
func resolveColonyBaseURL() (string, error) {
	resolver, err := config.NewResolver()
	if err != nil {
		return "", fmt.Errorf("failed to create config resolver: %w", err)
	}
	url, err := resolver.ResolveColonyURL("")
	if err != nil {
		return "", fmt.Errorf("failed to get colony URL: %w", err)
	}

	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url, nil
	}

	return "", fmt.Errorf("unexpected colony URL format: %s", url)
}

// attachColonyDatabase attaches the colony database to the DuckDB connection.
func attachColonyDatabase(ctx context.Context, db *sql.DB, dbName string) error {
	baseURL, err := resolveColonyBaseURL()
	if err != nil {
		return fmt.Errorf("failed to resolve colony address: %w", err)
	}

	// Use HTTP for ATTACH: DuckDB's httpfs cannot verify self-signed TLS certificates.
	attachURL := duckdbAttachBase(baseURL)
	dbURL := strings.TrimRight(attachURL, "/") + "/duckdb/" + dbName

	// Attach database as read-only using DuckDB's HTTP attach.
	attachSQL := fmt.Sprintf("ATTACH '%s' AS colony (READ_ONLY);", dbURL)

	if _, err := db.ExecContext(ctx, attachSQL); err != nil {
		return fmt.Errorf("failed to attach colony database from %s: %w", dbURL, err)
	}

	return nil
}

// listColonyDatabases queries the colony for available databases.
func listColonyDatabases(ctx context.Context) ([]string, error) {
	baseURL, err := resolveColonyBaseURL()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve colony address: %w", err)
	}

	// Construct URL for colony database list endpoint, preserving the scheme.
	listURL := strings.TrimRight(baseURL, "/") + "/duckdb"

	// Build HTTP client with appropriate TLS configuration for the colony endpoint.
	httpClient, err := helpers.BuildHTTPClient("", baseURL)
	if err != nil {
		// Fall back to default client on config error.
		httpClient = http.DefaultClient
	}

	// Make HTTP request.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query colony: %w", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close() // TODO: errcheck
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("colony returned status %d", resp.StatusCode)
	}

	// Parse JSON response.
	var result struct {
		Databases []string `json:"databases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Databases, nil
}
