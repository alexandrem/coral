package triage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateFlags(t *testing.T) {
	t.Run("valid text format, no attach", func(t *testing.T) {
		assert.NoError(t, validateFlags("text", false, false))
	})

	t.Run("valid json format with attach", func(t *testing.T) {
		assert.NoError(t, validateFlags("json", true, true))
	})

	t.Run("unsupported format", func(t *testing.T) {
		err := validateFlags("csv", false, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported format")
	})

	t.Run("attach-duration without attach is an argument error", func(t *testing.T) {
		err := validateFlags("text", false, true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--attach-duration requires --attach")
	})

	t.Run("attach-duration with attach is valid", func(t *testing.T) {
		assert.NoError(t, validateFlags("text", true, true))
	})
}

func TestNewCmd_FlagDefaults(t *testing.T) {
	cmd := NewCmd()
	assert.Equal(t, "triage [service]", cmd.Use)

	sinceFlag := cmd.Flags().Lookup("since")
	require.NotNil(t, sinceFlag)
	assert.Equal(t, DefaultSince, sinceFlag.DefValue)

	attachDurationFlag := cmd.Flags().Lookup("attach-duration")
	require.NotNil(t, attachDurationFlag)
	assert.Equal(t, DefaultAttachDuration.String(), attachDurationFlag.DefValue)

	formatFlag := cmd.Flags().Lookup("format")
	require.NotNil(t, formatFlag)
	assert.Equal(t, "text", formatFlag.DefValue)

	attachFlag := cmd.Flags().Lookup("attach")
	require.NotNil(t, attachFlag)
	assert.Equal(t, "false", attachFlag.DefValue)
}

func TestNewCmd_ArgsAcceptsZeroOrOneService(t *testing.T) {
	cmd := NewCmd()
	assert.NoError(t, cmd.Args(cmd, []string{}))
	assert.NoError(t, cmd.Args(cmd, []string{"api"}))
	assert.Error(t, cmd.Args(cmd, []string{"api", "extra"}))
}

func TestFormatText_Outcomes(t *testing.T) {
	t.Run("no_data", func(t *testing.T) {
		text := formatText(&Result{Service: "api", Outcome: OutcomeNoData, Attach: AttachOutcome{Status: AttachNotRequested}})
		assert.Contains(t, text, "No data available")
	})

	t.Run("nothing_degraded", func(t *testing.T) {
		text := formatText(&Result{Service: "", Outcome: OutcomeNothingDegraded, Attach: AttachOutcome{Status: AttachNotRequested}})
		assert.Contains(t, text, "No degraded or critical services")
	})

	t.Run("healthy", func(t *testing.T) {
		text := formatText(&Result{Service: "api", Outcome: OutcomeHealthy, Attach: AttachOutcome{Status: AttachNotRequested}})
		assert.Contains(t, text, "healthy")
	})

	t.Run("degraded with candidate and not-requested attach", func(t *testing.T) {
		text := formatText(&Result{
			Service:           "api",
			Outcome:           OutcomeDegraded,
			Summary:           &SummarySnapshot{Status: "critical", ErrorRate: 4.2, AvgLatencyMs: 892},
			CandidateStatus:   CandidateFound,
			CandidateFunction: &CandidateFunction{Name: "api.handleCheckout", File: "service/checkout.go", Line: 118, Source: SourceProfilingHotPath},
			Attach:            AttachOutcome{Status: AttachNotRequested},
		})
		assert.Contains(t, text, "api.handleCheckout")
		assert.Contains(t, text, "service/checkout.go:118")
		assert.Contains(t, text, "profiling hot path")
		assert.Contains(t, text, "not requested")
	})
}
