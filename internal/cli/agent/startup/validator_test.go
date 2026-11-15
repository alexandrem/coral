package startup

import (
	"testing"

	"github.com/coral-mesh/coral/internal/logging"
)

// newTestConfigValidator isolates the resolver from the developer's real
// ~/.coral state so tests never depend on (or mutate) local machine config.
func newTestConfigValidator(t *testing.T, noMonitorAll bool) *ConfigValidator {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CORAL_COLONY_ID", "")
	logger := logging.NewWithComponent(logging.Config{Level: "error"}, "test")
	return NewConfigValidator(logger, "", "test-colony", noMonitorAll)
}

// TestConfigValidatorDefaultMonitorAll verifies Beyla auto-instrumentation is
// on by default with no flags passed (RFD 103).
func TestConfigValidatorDefaultMonitorAll(t *testing.T) {
	v := newTestConfigValidator(t, false)

	result, err := v.Validate()
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	if !result.AgentConfig.Agent.MonitorAll {
		t.Error("expected Agent.MonitorAll to default to true")
	}
	if result.AgentMode != "monitor-all" {
		t.Errorf("expected AgentMode = monitor-all, got %q", result.AgentMode)
	}
}

// TestConfigValidatorNoMonitorAllOptsOut verifies --no-monitor-all disables
// Beyla auto-instrumentation regardless of the config-file default (RFD 103).
func TestConfigValidatorNoMonitorAllOptsOut(t *testing.T) {
	v := newTestConfigValidator(t, true)

	result, err := v.Validate()
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	if result.AgentConfig.Agent.MonitorAll {
		t.Error("expected Agent.MonitorAll to be false after --no-monitor-all")
	}
	if result.AgentMode != "passive" {
		t.Errorf("expected AgentMode = passive with no services configured, got %q", result.AgentMode)
	}
}
