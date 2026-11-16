package mesh

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/fleet"
)

func printMeshReport(out io.Writer, report fleet.Report) error {
	if err := fleet.PrintSummary(out, report); err != nil {
		return err
	}
	if report.DryRun {
		return fleet.PrintResults(out, report)
	}
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	header := "AGENT ID\tMESH IP\tSTATUS\tATTEMPTS\t"
	if report.Operation == "mesh ping" {
		header += "LOSS\tAVG RTT\tERROR"
	} else {
		header += "NAT TYPE\tHANDSHAKE\tCOLONY OBSERVES\tAGENT REGISTERED\tERROR"
	}
	if _, err := fmt.Fprintln(w, header); err != nil {
		return err
	}
	for _, result := range report.Results {
		var fields []string
		address := "-"
		if report.Operation == "mesh ping" {
			var ping colonyv1.MeshPingResponse_AgentPingResult
			fields = []string{"-", "-"}
			if len(result.Data) > 0 {
				if err := json.Unmarshal(result.Data, &ping); err != nil {
					return err
				}
				address, fields[0] = ping.MeshIp, fmt.Sprintf("%.1f%%", ping.PacketLossPercentage)
				if ping.Received > 0 {
					fields[1] = fmt.Sprintf("%.3fms", ping.AvgRttMs)
				}
			}
		} else {
			var audit colonyv1.MeshAuditAgentResult
			fields = []string{"-", "-", "-", "-"}
			if len(result.Data) > 0 {
				if err := json.Unmarshal(result.Data, &audit); err != nil {
					return err
				}
				address = audit.MeshIp
				age := "never"
				if audit.HandshakeAgeSeconds >= 0 {
					age = formatAge(audit.HandshakeAgeSeconds)
				}
				fields = []string{audit.NatType, age, audit.ColonyObservedEndpoint, audit.AgentRegisteredEndpoint}
			}
		}
		fields = append([]string{result.AgentID, address, result.Status, fmt.Sprint(result.Attempts)}, fields...)
		fields = append(fields, strings.Join(strings.Fields(result.Error), " "))
		if _, err := fmt.Fprintln(w, strings.Join(fields, "\t")); err != nil {
			return err
		}
	}
	return w.Flush()
}
