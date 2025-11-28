package duckdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"connectrpc.com/connect"
	_ "github.com/marcboeker/go-duckdb" // Import DuckDB driver.

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
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
	// Try to read colony URL from config file.
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}

	configPath := filepath.Join(home, ".coral", "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		return nil, fmt.Errorf("colony config not found at %s: run 'coral init' first", configPath)
	}

	// For now, use default colony URL (localhost:9000).
	// TODO: Read from config file when colony URL configuration is implemented.
	colonyURL := "http://localhost:9000"

	client := colonyv1connect.NewColonyServiceClient(
		http.DefaultClient,
		colonyURL,
	)

	return client, nil
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
		if fetchDatabases && agent.MeshIpv4 != "" {
			databases, err := listAgentDatabases(ctx, agent.MeshIpv4)
			if err != nil {
				// Log error but don't fail - agent might be offline.
				agentInfo.Databases = []string{}
			} else {
				agentInfo.Databases = databases
			}
		}

		agents = append(agents, agentInfo)
	}

	return agents, nil
}

// resolveAgentAddress resolves an agent ID to its mesh IP address via colony registry.
func resolveAgentAddress(ctx context.Context, agentID string) (string, error) {
	agents, err := listAgents(ctx, false) // Don't fetch databases for address resolution.
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
	db, err := sql.Open("duckdb", "")
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
func attachAgentDatabase(ctx context.Context, db *sql.DB, agentID string, meshIP string, dbName string) error {
	// Construct HTTP URL for agent DuckDB database.
	agentAddr := net.JoinHostPort(meshIP, "9001")
	dbURL := fmt.Sprintf("http://%s/duckdb/%s", agentAddr, dbName)

	// Attach database as read-only using DuckDB's HTTP attach.
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
func listAgentDatabases(ctx context.Context, meshIP string) ([]string, error) {
	// Construct HTTP URL for agent database list endpoint.
	agentAddr := net.JoinHostPort(meshIP, "9001")
	listURL := fmt.Sprintf("http://%s/duckdb", agentAddr)

	// Make HTTP request.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
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
