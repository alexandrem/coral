package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
)

func testOptions() Options {
	return Options{Concurrency: 2, Retries: 1, RequestTimeout: time.Second, Format: "json"}
}

func testPlan(ids ...string) *Plan {
	plan := &Plan{Operation: "test", ColonyID: "test-colony", Parameters: json.RawMessage(`{"value":1}`)}
	for _, id := range ids {
		plan.Targets = append(plan.Targets, Target{ID: id, Agent: &colonyv1.Agent{AgentId: id}})
	}
	return plan
}

func TestRunnerBoundsConcurrencyAndRetriesOnlyTransientFailures(t *testing.T) {
	plan := testPlan("good-1", "flaky", "permanent", "good-2", "missing")
	plan.Targets[4].Agent = nil
	var active, maximum atomic.Int32
	started := make(chan struct{}, 10)
	gate := make(chan struct{})
	var mu sync.Mutex
	calls := map[string]int{}
	finished := make(chan Report, 1)
	go func() {
		finished <- plan.Run(context.Background(), testOptions(), func(ctx context.Context, target Target, prior json.RawMessage) (string, json.RawMessage, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for previous := maximum.Load(); current > previous && !maximum.CompareAndSwap(previous, current); previous = maximum.Load() {
			}
			started <- struct{}{}
			select {
			case <-gate:
			case <-ctx.Done():
				return "", nil, ctx.Err()
			}
			mu.Lock()
			calls[target.ID]++
			attempt := calls[target.ID]
			mu.Unlock()
			if target.ID == "flaky" && attempt == 1 {
				return "first step done", json.RawMessage(`{"step":1}`), connect.NewError(connect.CodeUnavailable, errors.New("try again"))
			}
			if target.ID == "flaky" && !bytes.Equal(prior, []byte(`{"step":1}`)) {
				t.Errorf("prior progress was lost: %s", prior)
			}
			if target.ID == "permanent" {
				return "", nil, connect.NewError(connect.CodePermissionDenied, errors.New("denied"))
			}
			return "done", json.RawMessage(`{"step":2}`), nil
		})
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			close(gate)
			t.Fatal("worker pool did not start two jobs")
		}
	}
	close(gate)
	var report Report
	select {
	case report = <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not finish")
	}
	require.Equal(t, int32(2), maximum.Load())
	require.Equal(t, map[string]int{"good-1": 1, "good-2": 1, "flaky": 2, "permanent": 1}, calls)
	require.Equal(t, Summary{Total: 5, Succeeded: 3, Failed: 2}, report.Summary)
	require.Equal(t, "missing", report.Results[4].AgentID)
	require.Zero(t, report.Results[4].Attempts)
}

func TestDryRunNeverCallsActionAndPartialFailureStillWritesJSON(t *testing.T) {
	options := testOptions()
	options.DryRun, options.ReportFile = true, filepath.Join(t.TempDir(), "report.json")
	report := testPlan("one", "two").Run(context.Background(), options, func(context.Context, Target, json.RawMessage) (string, json.RawMessage, error) {
		t.Error("dry-run performed an operation")
		return "", nil, nil
	})
	require.Equal(t, Summary{Total: 2, Planned: 2}, report.Summary)
	for _, result := range report.Results {
		require.Zero(t, result.Attempts)
	}
	report.DryRun = false
	report.Results[1].Status, report.Results[1].Error = "failed", "unavailable"
	report.Results[0].Status = "succeeded"
	report.summarize()
	cmd := &cobra.Command{}
	var output bytes.Buffer
	cmd.SetOut(&output)
	require.ErrorContains(t, options.Finish(cmd, report), "1 of 2")
	var decoded Report
	require.NoError(t, json.Unmarshal(output.Bytes(), &decoded))
	require.Equal(t, report.Summary, decoded.Summary)
	saved, err := readRetry(options.ReportFile, "test", report.Parameters)
	require.NoError(t, err)
	require.Equal(t, report.Results, saved.Results)
}

func TestCancellationReportsQueuedTargets(t *testing.T) {
	options := testOptions()
	options.Concurrency = 1
	ctx, cancel := context.WithCancel(context.Background())
	report := testPlan("first", "queued", "also-queued").Run(ctx, options, func(ctx context.Context, _ Target, _ json.RawMessage) (string, json.RawMessage, error) {
		cancel()
		<-ctx.Done()
		return "partial", json.RawMessage(`{"step":1}`), ctx.Err()
	})
	require.Equal(t, Summary{Total: 3, Canceled: 3}, report.Summary)
	require.Equal(t, 1, report.Results[0].Attempts)
	require.Zero(t, report.Results[1].Attempts)
	require.JSONEq(t, `{"step":1}`, string(report.Results[0].Data))
}

func TestRetryRejectsDifferentOperationOrParameters(t *testing.T) {
	report := Report{Version: 1, Operation: "test", ColonyID: "colony", Parameters: json.RawMessage(`{"value":1}`), Results: []Result{{AgentID: "one", Status: "failed"}}}
	filename := filepath.Join(t.TempDir(), "retry.json")
	require.NoError(t, saveReport(filename, report))
	_, err := readRetry(filename, "other", report.Parameters)
	require.ErrorContains(t, err, "completed other report")
	_, err = readRetry(filename, "test", json.RawMessage(`{"value":2}`))
	require.ErrorContains(t, err, "parameters differ")
	report.Results = append(report.Results, report.Results[0])
	require.NoError(t, saveReport(filename, report))
	_, err = readRetry(filename, "test", report.Parameters)
	require.ErrorContains(t, err, "duplicate")
	report.Results, report.DryRun = report.Results[:1], true
	require.NoError(t, saveReport(filename, report))
	_, err = readRetry(filename, "test", report.Parameters)
	require.Error(t, err)
}

func TestReportDestinationIsCheckedBeforeNetworking(t *testing.T) {
	options := testOptions()
	options.ReportFile = filepath.Join(t.TempDir(), "missing", "report.json")
	_, err := options.Prepare(context.Background(), "does-not-exist", "test", struct{}{})
	require.ErrorContains(t, err, "report destination is not writable")
}

func TestRunnerTimeoutIsReportedAndBounded(t *testing.T) {
	options := testOptions()
	options.Concurrency, options.Retries, options.RequestTimeout = 1, 0, 10*time.Millisecond
	report := testPlan("one").Run(context.Background(), options, func(ctx context.Context, _ Target, _ json.RawMessage) (string, json.RawMessage, error) {
		<-ctx.Done()
		return "", nil, ctx.Err()
	})
	require.Equal(t, 1, report.Summary.Failed)
	require.Equal(t, "deadline_exceeded", report.Results[0].ErrorCode)
	require.Equal(t, 1, report.Results[0].Attempts)
}
