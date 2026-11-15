package fleet

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// Result records one pinned target, including partial operation data on failure.
type Result struct {
	AgentID   string          `json:"agent_id"`
	Status    string          `json:"status"`
	Attempts  int             `json:"attempts"`
	Error     string          `json:"error,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
	Message   string          `json:"message,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type Summary struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Canceled  int `json:"canceled"`
	Planned   int `json:"planned"`
}

// Report is also the input to --retry-failed. Parameters bind a retry to its operation.
type Report struct {
	Version    int             `json:"version"`
	Operation  string          `json:"operation"`
	ColonyID   string          `json:"colony_id"`
	Parameters json.RawMessage `json:"parameters"`
	DryRun     bool            `json:"dry_run"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Summary    Summary         `json:"summary"`
	Results    []Result        `json:"results"`
}

func (r *Report) summarize() {
	r.Summary = Summary{Total: len(r.Results)}
	for _, result := range r.Results {
		switch result.Status {
		case "succeeded":
			r.Summary.Succeeded++
		case "failed":
			r.Summary.Failed++
		case "canceled":
			r.Summary.Canceled++
		case "planned":
			r.Summary.Planned++
		}
	}
}

// Finish writes all target results before returning a partial-failure error.
func (o Options) Finish(cmd *cobra.Command, report Report) error {
	return o.FinishWithTable(cmd, report, printReport)
}

// FinishWithTable preserves command-specific diagnostic columns while sharing the JSON contract.
func (o Options) FinishWithTable(cmd *cobra.Command, report Report, table func(io.Writer, Report) error) error {
	var saveErr error
	if o.ReportFile != "" {
		saveErr = saveReport(o.ReportFile, report)
	}
	if o.Format == "json" {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	} else if err := table(cmd.OutOrStdout(), report); err != nil {
		return err
	}
	if saveErr != nil {
		return fmt.Errorf("could not save results: %w", saveErr)
	}
	if report.Summary.Failed+report.Summary.Canceled > 0 {
		return fmt.Errorf("%d of %d agents failed or were canceled", report.Summary.Failed+report.Summary.Canceled, report.Summary.Total)
	}
	return nil
}

func printReport(out io.Writer, report Report) error {
	if err := PrintSummary(out, report); err != nil {
		return err
	}
	return PrintResults(out, report)
}

func PrintSummary(out io.Writer, report Report) error {
	if _, err := fmt.Fprintf(out, "%s in colony %s: %d targets (%d succeeded, %d failed, %d canceled, %d planned)\n\n", strings.ToUpper(report.Operation), report.ColonyID, report.Summary.Total, report.Summary.Succeeded, report.Summary.Failed, report.Summary.Canceled, report.Summary.Planned); err != nil {
		return err
	}
	if report.DryRun {
		if _, err := fmt.Fprintf(out, "Parameters: %s\n\n", report.Parameters); err != nil {
			return err
		}
	}
	return nil
}

func PrintResults(out io.Writer, report Report) error {
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	if _, err := fmt.Fprintln(w, "AGENT ID\tSTATUS\tATTEMPTS\tDETAIL"); err != nil {
		return err
	}
	for _, result := range report.Results {
		detail := result.Message
		if result.Error != "" {
			detail = result.Error
		}
		// Keep errors and remotely supplied strings on one table row.
		detail = strings.Join(strings.Fields(detail), " ")
		if _, err := fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", result.AgentID, result.Status, result.Attempts, detail); err != nil {
			return err
		}
	}
	return w.Flush()
}

func saveReport(filename string, report Report) error {
	file, err := os.CreateTemp(filepath.Dir(filename), ".coral-fleet-*.json")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, filename)
}

// checkReportPath catches an unusable destination before an operation changes agents.
func checkReportPath(filename string) error {
	if filename == "" {
		return nil
	}
	if info, err := os.Stat(filename); err == nil && info.IsDir() {
		return fmt.Errorf("report destination is a directory: %s", filename)
	}
	file, err := os.CreateTemp(filepath.Dir(filename), ".coral-fleet-check-*")
	if err != nil {
		return fmt.Errorf("report destination is not writable: %w", err)
	}
	name := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
