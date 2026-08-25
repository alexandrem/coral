package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// NewCmd creates the top-level `coral triage` command (RFD 114).
func NewCmd() *cobra.Command {
	var (
		since          string
		attach         bool
		attachDuration time.Duration
		format         string
	)

	cmd := &cobra.Command{
		Use:   "triage [service]",
		Short: "Diagnose the worst degraded service and resolve a probe candidate",
		Long: `Combine the health summary, profiling data, and function registry into one
bounded diagnosis. Diagnosis is read-only; pass --attach to instrument the
resolved candidate with a bounded eBPF probe.

With no service argument, diagnoses the worst currently degraded or critical
service across the fleet. With a service argument, diagnoses that service
regardless of its current health.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFlags(format, attach, cmd.Flags().Changed("attach-duration")); err != nil {
				return err
			}

			service := ""
			if len(args) > 0 {
				service = args[0]
			}

			result, err := Diagnose(context.Background(), "", Options{
				Service:        service,
				Since:          since,
				Attach:         attach,
				AttachDuration: attachDuration,
			})
			if err != nil {
				return err
			}

			if format == "json" {
				data, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to format output: %w", err)
				}
				fmt.Println(string(data))
				return nil
			}

			fmt.Print(formatText(result))
			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", DefaultSince, "Time range for the health summary (e.g. 5m, 1h)")
	cmd.Flags().BoolVar(&attach, "attach", false, "Attach a bounded eBPF probe to the resolved candidate")
	cmd.Flags().DurationVar(&attachDuration, "attach-duration", DefaultAttachDuration, "Probe duration when --attach is set")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json)")

	return cmd
}

// validateFlags checks argument combinations that cobra's flag definitions
// can't express: an unsupported --format value, and --attach-duration
// supplied without --attach.
func validateFlags(format string, attach bool, attachDurationChanged bool) error {
	if format != "text" && format != "json" {
		return fmt.Errorf("unsupported format %q, must be text or json", format)
	}
	if attachDurationChanged && !attach {
		return fmt.Errorf("--attach-duration requires --attach")
	}
	return nil
}

// formatText renders a Result as human-readable text. Failure fields (a
// candidate lookup error or a failed attachment) are always shown and never
// described as a successful outcome.
func formatText(r *Result) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Triage: %s\n", r.Service)
	fmt.Fprintf(&b, "  Outcome: %s\n", r.Outcome)

	if r.Summary != nil {
		fmt.Fprintf(&b, "  Status: %s (error_rate=%.1f%% avg_latency=%.0fms)\n",
			r.Summary.Status, r.Summary.ErrorRate, r.Summary.AvgLatencyMs)
	}

	switch r.Outcome {
	case OutcomeNoData:
		fmt.Fprintln(&b, "  No data available for the requested service and time range.")
		return b.String()
	case OutcomeNothingDegraded:
		fmt.Fprintln(&b, "  No degraded or critical services found.")
		return b.String()
	case OutcomeHealthy:
		fmt.Fprintln(&b, "  Service is healthy; no diagnosis needed.")
		return b.String()
	case OutcomeUnknown:
		fmt.Fprintln(&b, "  Service status is unknown; no diagnosis performed.")
		return b.String()
	}

	switch r.CandidateStatus {
	case CandidateFound:
		fn := r.CandidateFunction
		loc := fn.Name
		if fn.File != "" {
			loc = fmt.Sprintf("%s (%s:%d)", fn.Name, fn.File, fn.Line)
		}
		fmt.Fprintf(&b, "  Candidate: %s\n", loc)
		fmt.Fprintf(&b, "    Source: %s\n", candidateSourceLabel(fn.Source))
	case CandidateNotFound:
		fmt.Fprintln(&b, "  Candidate: not found")
	case CandidateUnavailable:
		fmt.Fprintf(&b, "  Candidate: UNAVAILABLE — %s\n", r.CandidateError)
	}

	switch r.Attach.Status {
	case AttachNotRequested:
		fmt.Fprintln(&b, "  Attach: not requested")
	case AttachSkipped:
		fmt.Fprintln(&b, "  Attach: skipped (no candidate)")
	case AttachAttached:
		fmt.Fprintf(&b, "  Attach: attached (session %s)\n", r.Attach.SessionID)
	case AttachFailed:
		fmt.Fprintf(&b, "  Attach: FAILED — %s\n", r.Attach.Error)
	}

	return b.String()
}

func candidateSourceLabel(source string) string {
	switch source {
	case SourceProfilingHotPath:
		return "profiling hot path"
	case SourceSemanticIssueMatch:
		return "semantic issue match"
	default:
		return source
	}
}
