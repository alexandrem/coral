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
	Layer    string `json:"layer"`
}

// topologyJSON is the JSON-serializable representation of the topology response.
type topologyJSON struct {
	ColonyID    string                   `json:"colony_id"`
	Connections []topologyConnectionJSON `json:"connections"`
}

// NewTopologyCmd creates the 'coral query topology' command (RFD 092, RFD 033).
func NewTopologyCmd() *cobra.Command {
	var format string
	var includeL4 bool

	cmd := &cobra.Command{
		Use:   "topology",
		Short: "Show the service dependency graph",
		Long: `Show the live service dependency graph derived from observed trace and network data.

Displays all cross-service call relationships discovered in the last hour,
showing which services call which other services, over what protocol, and
at which evidence layer (L7 application trace or L4 network observation).

Examples:
  coral query topology                    # ASCII table (L4 + L7)
  coral query topology --include-l4=false # L7 (trace-derived) edges only
  coral query topology --format json      # JSON output
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

			conns := filterConnections(resp.Msg.Connections, includeL4)

			if format == "json" {
				return printTopologyJSON(resp.Msg.ColonyId, conns)
			}

			return printTopologyText(conns)
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json)")
	cmd.Flags().BoolVar(&includeL4, "include-l4", true, "Include L4 network edges (RFD 033)")
	return cmd
}

// filterConnections drops L4-only edges when includeL4 is false.
func filterConnections(conns []*colonypb.Connection, includeL4 bool) []*colonypb.Connection {
	if includeL4 {
		return conns
	}
	filtered := conns[:0:0]
	for _, c := range conns {
		if c.EvidenceLayer != colonypb.EvidenceLayer_EVIDENCE_LAYER_L4_NETWORK {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// evidenceLayerLabel returns the short display label for an evidence layer.
func evidenceLayerLabel(layer colonypb.EvidenceLayer) string {
	switch layer {
	case colonypb.EvidenceLayer_EVIDENCE_LAYER_L4_NETWORK:
		return "L4"
	case colonypb.EvidenceLayer_EVIDENCE_LAYER_BOTH:
		return "BOTH"
	default:
		// L7_TRACE and UNSPECIFIED (legacy) are both L7.
		return "L7"
	}
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
		minLayer    = len("LAYER")
	)
	fromW, toW, protoW, layerW := minFrom, minTo, minProtocol, minLayer
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
		if l := len(evidenceLayerLabel(c.EvidenceLayer)); l > layerW {
			layerW = l
		}
	}

	// Print header.
	fmtStr := fmt.Sprintf("%%-%ds  →  %%-%ds  %%-%ds  %%-%ds\n", fromW, toW, protoW, layerW)
	fmt.Printf(fmtStr, "FROM SERVICE", "TO SERVICE", "PROTOCOL", "LAYER")
	fmt.Printf("%s     %s  %s  %s\n",
		strings.Repeat("-", fromW),
		strings.Repeat("-", toW),
		strings.Repeat("-", protoW),
		strings.Repeat("-", layerW),
	)

	// Print rows.
	for _, c := range conns {
		fmt.Printf(fmtStr, c.SourceId, c.TargetId, strings.ToUpper(c.ConnectionType), evidenceLayerLabel(c.EvidenceLayer))
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
			Layer:    evidenceLayerLabel(c.EvidenceLayer),
		})
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal topology: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
