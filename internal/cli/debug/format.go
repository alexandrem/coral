package debug

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
)

// OutputFormat represents the output format type.
type OutputFormat string

const (
	FormatText OutputFormat = "text"
	FormatJSON OutputFormat = "json"
	FormatCSV  OutputFormat = "csv"
)

// OutputFormatter formats debug command output.
type OutputFormatter interface {
	FormatSessions(sessions []*colonypb.DebugSession) (string, error)
	FormatResults(results *colonypb.GetDebugResultsResponse) (string, error)
	FormatAttachResponse(resp *colonypb.AttachUprobeResponse) (string, error)
}

// NewFormatter creates an output formatter for the given format.
func NewFormatter(format OutputFormat) OutputFormatter {
	switch format {
	case FormatJSON:
		return &JSONFormatter{}
	case FormatCSV:
		return &CSVFormatter{}
	default:
		return &TextFormatter{}
	}
}

// TextFormatter formats output as human-readable text.
type TextFormatter struct{}

// FormatSessions formats the debug sessions.
// nolint: errcheck
func (f *TextFormatter) FormatSessions(sessions []*colonypb.DebugSession) (string, error) {
	if len(sessions) == 0 {
		return "No active debug sessions found.\n", nil
	}

	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "SESSION ID\tSERVICE\tFUNCTION\tSTATUS\tEXPIRES")

	for _, session := range sessions {
		expiresIn := time.Until(session.ExpiresAt.AsTime()).Round(time.Second)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			session.SessionId,
			session.ServiceName,
			session.FunctionName,
			session.Status,
			expiresIn.String(),
		)
	}

	w.Flush()
	return buf.String(), nil
}

func (f *TextFormatter) FormatResults(results *colonypb.GetDebugResultsResponse) (string, error) {
	var buf strings.Builder

	fmt.Fprintf(&buf, "Debug Session Results\n")
	fmt.Fprintf(&buf, "=====================\n\n")
	fmt.Fprintf(&buf, "Session ID: %s\n", results.SessionId)
	fmt.Fprintf(&buf, "Function:   %s\n", results.Function)
	fmt.Fprintf(&buf, "Duration:   %s\n\n", results.Duration.AsDuration())

	if results.Statistics != nil {
		fmt.Fprintf(&buf, "Statistics:\n")
		fmt.Fprintf(&buf, "  Total Calls: %d\n", results.Statistics.TotalCalls)
		fmt.Fprintf(&buf, "  P50:         %s\n", results.Statistics.DurationP50.AsDuration())
		fmt.Fprintf(&buf, "  P95:         %s\n", results.Statistics.DurationP95.AsDuration())
		fmt.Fprintf(&buf, "  P99:         %s\n", results.Statistics.DurationP99.AsDuration())
		fmt.Fprintf(&buf, "  Max:         %s\n\n", results.Statistics.DurationMax.AsDuration())
	}

	if len(results.SlowOutliers) > 0 {
		fmt.Fprintf(&buf, "Top Slow Calls:\n")
		for i, outlier := range results.SlowOutliers {
			if i >= 5 {
				break // Limit to top 5
			}
			fmt.Fprintf(&buf, "  %d. %s - %s\n",
				i+1,
				outlier.Duration.AsDuration(),
				outlier.Timestamp.AsTime().Format(time.RFC3339),
			)
		}
	}

	return buf.String(), nil
}

func (f *TextFormatter) FormatAttachResponse(resp *colonypb.AttachUprobeResponse) (string, error) {
	var buf strings.Builder
	fmt.Fprintf(&buf, "✓ Debug session started\n")
	fmt.Fprintf(&buf, "  Session ID: %s\n", resp.SessionId)
	fmt.Fprintf(&buf, "  Expires at: %s\n", resp.ExpiresAt.AsTime().Format(time.RFC3339))
	return buf.String(), nil
}

// JSONFormatter formats output as JSON.
type JSONFormatter struct{}

func (f *JSONFormatter) FormatSessions(sessions []*colonypb.DebugSession) (string, error) {
	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return string(data) + "\n", nil
}

func (f *JSONFormatter) FormatResults(results *colonypb.GetDebugResultsResponse) (string, error) {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return string(data) + "\n", nil
}

func (f *JSONFormatter) FormatAttachResponse(resp *colonypb.AttachUprobeResponse) (string, error) {
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return string(data) + "\n", nil
}

// CSVFormatter formats output as CSV.
type CSVFormatter struct{}

func (f *CSVFormatter) FormatSessions(sessions []*colonypb.DebugSession) (string, error) {
	var buf strings.Builder
	w := csv.NewWriter(&buf)

	// Write header
	if err := w.Write([]string{"session_id", "service_name", "function_name", "status", "started_at", "expires_at", "event_count"}); err != nil {
		return "", fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write rows
	for _, session := range sessions {
		if err := w.Write([]string{
			session.SessionId,
			session.ServiceName,
			session.FunctionName,
			session.Status,
			session.StartedAt.AsTime().Format(time.RFC3339),
			session.ExpiresAt.AsTime().Format(time.RFC3339),
			fmt.Sprintf("%d", session.EventCount),
		}); err != nil {
			return "", fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("CSV writer error: %w", err)
	}

	return buf.String(), nil
}

func (f *CSVFormatter) FormatResults(results *colonypb.GetDebugResultsResponse) (string, error) {
	var buf strings.Builder
	w := csv.NewWriter(&buf)

	// Write header
	if err := w.Write([]string{"session_id", "function", "duration", "total_calls", "p50", "p95", "p99", "max"}); err != nil {
		return "", fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write row
	stats := results.Statistics
	if err := w.Write([]string{
		results.SessionId,
		results.Function,
		results.Duration.AsDuration().String(),
		fmt.Sprintf("%d", stats.TotalCalls),
		stats.DurationP50.AsDuration().String(),
		stats.DurationP95.AsDuration().String(),
		stats.DurationP99.AsDuration().String(),
		stats.DurationMax.AsDuration().String(),
	}); err != nil {
		return "", fmt.Errorf("failed to write CSV row: %w", err)
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("CSV writer error: %w", err)
	}

	return buf.String(), nil
}

func (f *CSVFormatter) FormatAttachResponse(resp *colonypb.AttachUprobeResponse) (string, error) {
	var buf strings.Builder
	w := csv.NewWriter(&buf)

	// Write header
	if err := w.Write([]string{"session_id", "expires_at", "success"}); err != nil {
		return "", fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write row
	if err := w.Write([]string{
		resp.SessionId,
		resp.ExpiresAt.AsTime().Format(time.RFC3339),
		fmt.Sprintf("%t", resp.Success),
	}); err != nil {
		return "", fmt.Errorf("failed to write CSV row: %w", err)
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("CSV writer error: %w", err)
	}

	return buf.String(), nil
}

// WriteOutput writes formatted output to the given writer.
func WriteOutput(w io.Writer, output string) error {
	_, err := fmt.Fprint(w, output)
	return err
}
