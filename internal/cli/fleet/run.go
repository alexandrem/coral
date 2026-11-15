package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"slices"
	"sort"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
)

type Options struct {
	Selector                        Selector
	All, DryRun                     bool
	Concurrency, Retries            int
	RequestTimeout                  time.Duration
	RetryFailed, ReportFile, Format string
}

func (o *Options) AddFlags(cmd *cobra.Command) {
	o.Selector.AddFlags(cmd)
	cmd.Flags().BoolVar(&o.All, "all", false, "Target all registered agents explicitly")
	cmd.Flags().BoolVar(&o.DryRun, "dry-run", false, "Preview the pinned target set without performing the operation")
	cmd.Flags().IntVar(&o.Concurrency, "concurrency", 8, "Maximum concurrent target operations (1-128)")
	cmd.Flags().IntVar(&o.Retries, "retries", 0, "Retries per failed target for transient errors (0-10)")
	cmd.Flags().DurationVar(&o.RequestTimeout, "request-timeout", 10*time.Second, "Timeout per target attempt (maximum 5m)")
	cmd.Flags().StringVar(&o.RetryFailed, "retry-failed", "", "Retry only failed/canceled targets from a saved JSON report")
	cmd.Flags().StringVar(&o.ReportFile, "report", "", "Save a JSON result report for inspection or --retry-failed")
	helpers.AddFormatFlag(cmd, &o.Format, helpers.FormatTable, []helpers.OutputFormat{helpers.FormatTable, helpers.FormatJSON})
}

func (o Options) Validate() error {
	if err := o.Selector.Validate(); err != nil {
		return err
	}
	if o.Concurrency < 1 || o.Concurrency > 128 {
		return fmt.Errorf("--concurrency must be between 1 and 128")
	}
	if o.Retries < 0 || o.Retries > 10 {
		return fmt.Errorf("--retries must be between 0 and 10")
	}
	if o.RequestTimeout <= 0 || o.RequestTimeout > 5*time.Minute {
		return fmt.Errorf("--request-timeout must be positive and at most 5m")
	}
	if o.All && o.Selector.HasFilters() {
		return fmt.Errorf("--all cannot be combined with target filters")
	}
	if o.RetryFailed != "" && (o.All || o.Selector.HasFilters()) {
		return fmt.Errorf("--retry-failed cannot be combined with target filters or --all")
	}
	return helpers.ValidateFormat(o.Format, []helpers.OutputFormat{helpers.FormatTable, helpers.FormatJSON})
}

// Target pins an identity for the operation; a missing registry entry remains an explicit failure.
type Target struct {
	ID       string
	Agent    *colonyv1.Agent
	Previous *Result
	Attempt  int
}

type Plan struct {
	Operation, ColonyID string
	Parameters          json.RawMessage
	Targets             []Target
	Client              colonyv1connect.ColonyServiceClient
}

func (o *Options) Prepare(ctx context.Context, colonyID, operation string, parameters any) (*Plan, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if err := checkReportPath(o.ReportFile); err != nil {
		return nil, err
	}
	input, err := json.Marshal(parameters)
	if err != nil {
		return nil, err
	}
	previous := map[string]*Result{}
	selector := o.Selector
	if o.RetryFailed != "" {
		report, err := readRetry(o.RetryFailed, operation, input)
		if err != nil {
			return nil, err
		}
		if colonyID != "" && colonyID != report.ColonyID {
			return nil, fmt.Errorf("retry report belongs to colony %s, not %s", report.ColonyID, colonyID)
		}
		colonyID = report.ColonyID
		for i := range report.Results {
			result := &report.Results[i]
			if result.Status == "failed" || result.Status == "canceled" {
				selector.IDs = append(selector.IDs, result.AgentID)
				previous[result.AgentID] = result
			}
		}
		if len(selector.IDs) == 0 {
			return nil, fmt.Errorf("retry report contains no failed or canceled targets")
		}
	}
	if colonyID == "" {
		resolver, err := config.NewResolver()
		if err != nil {
			return nil, err
		}
		colonyID, err = resolver.ResolveColonyID()
		if err != nil {
			return nil, err
		}
	}
	client, _, err := helpers.GetColonyClientWithFallback(ctx, colonyID)
	if err != nil {
		return nil, err
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status, err := client.GetStatus(lookupCtx, connect.NewRequest(&colonyv1.GetStatusRequest{}))
	if err != nil {
		return nil, fmt.Errorf("verify target colony: %w", err)
	}
	if status.Msg.ColonyId != colonyID {
		return nil, fmt.Errorf("endpoint identifies colony %q, expected %q", status.Msg.ColonyId, colonyID)
	}
	inventory, err := client.ListAgents(lookupCtx, connect.NewRequest(&colonyv1.ListAgentsRequest{}))
	if err != nil {
		return nil, fmt.Errorf("resolve fleet targets: %w", err)
	}
	plan := &Plan{Operation: operation, ColonyID: colonyID, Parameters: input, Client: client, Targets: []Target{}}
	for _, agent := range selector.Select(inventory.Msg.Agents) {
		plan.Targets = append(plan.Targets, Target{ID: agent.AgentId, Agent: agent, Previous: previous[agent.AgentId]})
	}
	// Explicit IDs which disappeared must not silently vanish or select a replacement.
	for _, id := range selector.IDs {
		found := slices.ContainsFunc(inventory.Msg.Agents, func(agent *colonyv1.Agent) bool { return agent.AgentId == id })
		duplicate := slices.ContainsFunc(plan.Targets, func(target Target) bool { return target.ID == id })
		if !found && !duplicate {
			plan.Targets = append(plan.Targets, Target{ID: id, Previous: previous[id]})
		}
	}
	sort.Slice(plan.Targets, func(i, j int) bool { return plan.Targets[i].ID < plan.Targets[j].ID })
	if len(plan.Targets) == 0 && selector.HasFilters() {
		return nil, fmt.Errorf("no agents match the target selection")
	}
	return plan, nil
}

func readRetry(filename, operation string, input json.RawMessage) (*Report, error) {
	report, err := LoadRetryReport(filename, operation)
	if err != nil {
		return nil, err
	}
	var oldInput, newInput any
	if json.Unmarshal(report.Parameters, &oldInput) != nil || json.Unmarshal(input, &newInput) != nil || !reflect.DeepEqual(oldInput, newInput) {
		return nil, fmt.Errorf("operation parameters differ from the retry report; repeat the original parameters")
	}
	return report, nil
}

// LoadRetryReport lets commands restore recorded parameters when the caller omits them.
func LoadRetryReport(filename, operation string) (*Report, error) {
	data, err := os.ReadFile(filename) // #nosec G304 -- The user explicitly selects this local --retry-failed report.
	if err != nil {
		return nil, fmt.Errorf("read retry report: %w", err)
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("invalid retry report: %w", err)
	}
	if report.Version != 1 || report.Operation != operation || report.ColonyID == "" || report.DryRun {
		return nil, fmt.Errorf("retry report must be a completed %s report (version 1)", operation)
	}
	seen := map[string]bool{}
	for _, result := range report.Results {
		if result.AgentID == "" || seen[result.AgentID] || !slices.Contains([]string{"succeeded", "failed", "canceled"}, result.Status) {
			return nil, fmt.Errorf("retry report contains invalid or duplicate target results")
		}
		seen[result.AgentID] = true
	}
	return &report, nil
}

// Action receives the last attempt's data so multi-step operations can retain successful steps.
type Action func(context.Context, Target, json.RawMessage) (message string, data json.RawMessage, err error)

func (p *Plan) Run(ctx context.Context, options Options, action Action) Report {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	report := Report{Version: 1, Operation: p.Operation, ColonyID: p.ColonyID, Parameters: p.Parameters, DryRun: options.DryRun, StartedAt: time.Now().UTC(), Results: make([]Result, len(p.Targets))}
	jobs := make(chan int, len(p.Targets))
	for i := range p.Targets {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(options.Concurrency, len(p.Targets)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				report.Results[index] = runTarget(ctx, options, p.Targets[index], action)
			}
		}()
	}
	workers.Wait()
	report.FinishedAt = time.Now().UTC()
	report.summarize()
	return report
}

func runTarget(ctx context.Context, options Options, target Target, action Action) Result {
	result := Result{AgentID: target.ID}
	if target.Previous != nil {
		result.Data = bytes.Clone(target.Previous.Data)
	}
	if target.Agent == nil {
		result.Status, result.Error = "failed", "agent is no longer registered in the selected colony"
		result.ErrorCode = "not_found"
		return result
	}
	if options.DryRun {
		result.Status = "planned"
		return result
	}
	for attempt := 0; attempt <= options.Retries; attempt++ {
		if ctx.Err() != nil {
			result.Status, result.Error = "canceled", ctx.Err().Error()
			result.ErrorCode = "canceled"
			return result
		}
		attemptCtx, cancel := context.WithTimeout(ctx, options.RequestTimeout)
		target.Attempt = attempt + 1
		message, data, err := action(attemptCtx, target, result.Data)
		cancel()
		result.Attempts++
		result.Message, result.Data = message, data
		if err == nil {
			result.Status, result.Error = "succeeded", ""
			result.ErrorCode = ""
			return result
		}
		result.Status, result.Error = "failed", err.Error()
		result.ErrorCode = connect.CodeOf(err).String()
		if errors.Is(err, context.DeadlineExceeded) {
			result.ErrorCode = "deadline_exceeded"
		}
		if ctx.Err() != nil {
			result.Status = "canceled"
			result.ErrorCode = "canceled"
			return result
		}
		if attempt == options.Retries || !Retryable(err) {
			return result
		}
		timer := time.NewTimer(min(time.Duration(attempt+1)*100*time.Millisecond, time.Second))
		select {
		case <-ctx.Done():
			timer.Stop()
			result.Status, result.Error = "canceled", ctx.Err().Error()
			result.ErrorCode = "canceled"
			return result
		case <-timer.C:
		}
	}
	return result
}

func Retryable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return slices.Contains([]connect.Code{connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeResourceExhausted}, connect.CodeOf(err))
}
