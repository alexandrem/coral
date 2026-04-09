package duckdb

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/spf13/cobra"
)

// NewExportCmd creates the `coral duckdb export` subcommand (RFD 097).
func NewExportCmd() *cobra.Command {
	var (
		database string
		query    string
		output   string
	)

	cmd := &cobra.Command{
		Use:   "export <agent-id> [<table>]",
		Short: "Export agent DuckDB table or query as a Vortex (.vx) file",
		Long: `Export an agent's DuckDB table or custom SQL query result as a Vortex (.vx) file.

Vortex is a columnar format with fast selective column reads (faster than Parquet
for narrow queries). The exported file can be opened offline in Python, Polars, or
any Apache Arrow-compatible tool — no running agent connection required.

Examples:
  # Export a full table (auto-generated filename)
  coral duckdb export agent-abc123 beyla_http_metrics_local

  # Export to a specific file
  coral duckdb export agent-abc123 beyla_http_metrics_local --output traces.vx

  # Export a custom SQL query
  coral duckdb export agent-abc123 --query "SELECT * FROM beyla_http_metrics_local WHERE timestamp > now() - INTERVAL 15 MINUTES" --output recent.vx

  # Specify a non-default database
  coral duckdb export agent-abc123 beyla_traces_local --database beyla`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("agent ID is required")
			}
			if query == "" && len(args) < 2 {
				return fmt.Errorf("table name is required (or use --query for custom SQL)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExport(cmd.Context(), args, database, query, output)
		},
	}

	cmd.Flags().StringVarP(&database, "database", "d", "metrics", "Database name on the agent (default: metrics)")
	cmd.Flags().StringVar(&query, "query", "", "Custom SQL query to export (SELECT only; overrides <table>)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output file path (default: auto-generated from agent ID, table, and timestamp)")

	return cmd
}

// runExport implements the export command logic.
func runExport(ctx context.Context, args []string, database, query, output string) error {
	agentID := args[0]
	var table string
	if len(args) >= 2 {
		table = args[1]
	}

	// Resolve the agent's base URL via the colony proxy or direct mesh connection.
	agentBase, err := agentDuckDBBase(ctx, agentID, "")
	if err != nil {
		return fmt.Errorf("failed to resolve agent: %w", err)
	}

	// Build the vortex URL.
	vortexURL, err := buildVortexURL(agentBase, agentID, database, table, query)
	if err != nil {
		return err
	}

	// Auto-generate output filename if not provided.
	if output == "" {
		ts := time.Now().UTC().Format("2006-01-02T15-04Z")
		name := table
		if name == "" {
			name = "query-export"
		}
		output = fmt.Sprintf("%s-%s-%s.vx", agentID, name, ts)
	}

	// Make the HTTP request and stream the response to disk.
	if err := downloadVortexExport(ctx, agentBase, vortexURL, output); err != nil {
		return err
	}

	// Print file size and usage hint.
	info, err := os.Stat(output)
	if err != nil {
		return fmt.Errorf("failed to stat output file: %w", err)
	}
	sizeStr := formatBytes(info.Size())

	fmt.Printf("Saved %s → %s\n\n", sizeStr, output)
	fmt.Printf("    Open in Python:\n")
	fmt.Printf("      import vortex\n")
	fmt.Printf("      tbl = vortex.read(%q).to_arrow()\n", output)
	fmt.Println()

	return nil
}

// buildVortexURL constructs the /vortex endpoint URL for the given parameters.
// agentBase is the base URL from agentDuckDBBase (either http://meshIP:9001 or
// http(s)://colony/agent/{id}).
func buildVortexURL(agentBase, agentID, database, table, query string) (string, error) {
	base := strings.TrimRight(agentBase, "/")

	// agentDuckDBBase returns:
	//   - local:  http://meshIP:9001
	//   - proxy:  http(s)://colony/agent/{id}   (with duckdb path component included)
	// We need to replace the /duckdb component with /vortex.
	// For proxy mode, agentBase ends with /agent/{id}, so we just append /vortex/...
	// For local mode, agentBase is http://meshIP:9001, so we append /vortex/...
	// In both cases the pattern is: base + /vortex/<db>[/<table>][?query=...]

	var vortexPath string
	if table != "" {
		vortexPath = fmt.Sprintf("%s/vortex/%s/%s", base, url.PathEscape(database), url.PathEscape(table))
	} else if query != "" {
		vortexPath = fmt.Sprintf("%s/vortex/%s?query=%s", base, url.PathEscape(database), url.QueryEscape(query))
	} else {
		return "", fmt.Errorf("either table name or --query must be specified")
	}

	return vortexPath, nil
}

// downloadVortexExport streams the Vortex file from the agent to the output path.
func downloadVortexExport(ctx context.Context, agentBase, vortexURL, output string) error {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, vortexURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// Success — stream to file below.
	case http.StatusNotImplemented:
		return fmt.Errorf("vortex extension is not available on this agent (DuckDB community extension not installed)")
	case http.StatusRequestEntityTooLarge:
		return fmt.Errorf("agent refused export: insufficient disk space on agent host")
	case http.StatusNotFound:
		return fmt.Errorf("database or table not found on agent")
	case http.StatusBadRequest:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("invalid request: %s", strings.TrimSpace(string(body)))
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agent returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Create the output file.
	f, err := os.Create(output) //nolint:gosec // output path is from user's --output flag, intentional
	if err != nil {
		return fmt.Errorf("failed to create output file %s: %w", output, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	return nil
}

// formatBytes returns a human-readable byte size string.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
