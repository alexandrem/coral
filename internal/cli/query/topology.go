package query

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// topologyConnectionJSON is the JSON-serializable representation of a service connection.
type topologyConnectionJSON struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Protocol string `json:"protocol"`
}

// topologyJSON is the JSON-serializable representation of the topology response.
type topologyJSON struct {
	ColonyID    string                   `json:"colony_id"`
	Connections []topologyConnectionJSON `json:"connections"`
}

// NewTopologyCmd creates the 'coral query topology' command (RFD 092).
func NewTopologyCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "topology",
		Short: "Show the service dependency graph",
		Long: `Show the live service dependency graph derived from observed trace data.

Displays all cross-service call relationships discovered in the last hour,
showing which services call which other services and over what protocol.

Examples:
  coral query topology               # ASCII table
  coral query topology --format json # JSON output
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			client, err := helpers.GetColonyClient("")
			if err != nil {
				return fmt.Errorf("failed to connect to colony: %w", err)
			}

			resp, err := client.GetTopology(ctx, connect.NewRequest(&colonypb.GetTopologyRequest{}))
			if err != nil {
				return fmt.Errorf("failed to get topology: %w", err)
			}

			conns := resp.Msg.Connections

			if format == "json" {
				return printTopologyJSON(resp.Msg.ColonyId, conns)
			}

			return printTopologyText(conns)
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json)")
	return cmd
}

func printTopologyText(conns []*colonypb.Connection) error {
	if len(conns) == 0 {
		fmt.Println("Service Topology: no cross-service calls observed in the last hour")
		return nil
	}

	fmt.Printf("Service Topology (last 1h, %d connection(s)):\n\n", len(conns))

	// Calculate column widths.
	const (
		minFrom     = len("FROM SERVICE")
		minTo       = len("TO SERVICE")
		minProtocol = len("PROTOCOL")
	)
	fromW, toW, protoW := minFrom, minTo, minProtocol
	for _, c := range conns {
		if len(c.SourceId) > fromW {
			fromW = len(c.SourceId)
		}
		if len(c.TargetId) > toW {
			toW = len(c.TargetId)
		}
		if len(c.ConnectionType) > protoW {
			protoW = len(c.ConnectionType)
		}
	}

	// Print header.
	fmtStr := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds\n", fromW, toW, protoW)
	fmt.Printf(fmtStr, "FROM SERVICE", "TO SERVICE", "PROTOCOL")
	fmt.Printf("%s  %s  %s\n",
		strings.Repeat("-", fromW),
		strings.Repeat("-", toW),
		strings.Repeat("-", protoW),
	)

	// Print rows.
	for _, c := range conns {
		fmt.Printf(fmtStr, c.SourceId, c.TargetId, strings.ToUpper(c.ConnectionType))
	}

	return nil
}

func printTopologyJSON(colonyID string, conns []*colonypb.Connection) error {
	out := topologyJSON{
		ColonyID:    colonyID,
		Connections: make([]topologyConnectionJSON, 0, len(conns)),
	}

	for _, c := range conns {
		out.Connections = append(out.Connections, topologyConnectionJSON{
			From:     c.SourceId,
			To:       c.TargetId,
			Protocol: strings.ToUpper(c.ConnectionType),
		})
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal topology: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
