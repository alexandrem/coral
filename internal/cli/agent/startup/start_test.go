package startup

import "testing"

// TestStartCmdMonitorAllFlagsRegistered verifies the RFD 103 flag surface:
// --monitor-all still parses (as a deprecated no-op, detected via Changed())
// and --no-monitor-all exists as the opt-out. --connect must be gone.
func TestStartCmdMonitorAllFlagsRegistered(t *testing.T) {
	cmd := NewStartCmd()

	if cmd.Flags().Lookup("connect") != nil {
		t.Error("--connect flag should have been removed from 'agent start' (RFD 103)")
	}
	if cmd.Flags().Lookup("no-monitor-all") == nil {
		t.Fatal("expected --no-monitor-all flag to be registered")
	}
	if cmd.Flags().Lookup("monitor-all") == nil {
		t.Fatal("expected --monitor-all flag to still be registered for backward compatibility")
	}

	if err := cmd.ParseFlags([]string{"--monitor-all"}); err != nil {
		t.Fatalf("ParseFlags(--monitor-all) error = %v", err)
	}
	if !cmd.Flags().Changed("monitor-all") {
		t.Error("expected Changed(\"monitor-all\") to be true after passing --monitor-all, " +
			"which drives the deprecation warning in RunE")
	}
}

// TestStartCmdMonitorAllFlagDefaultsUnset verifies that omitting both flags
// leaves Changed() false, so RunE does not emit a spurious deprecation
// warning for the default (no-flag) invocation.
func TestStartCmdMonitorAllFlagDefaultsUnset(t *testing.T) {
	cmd := NewStartCmd()

	if err := cmd.ParseFlags([]string{}); err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}
	if cmd.Flags().Changed("monitor-all") {
		t.Error("Changed(\"monitor-all\") should be false when the flag is not passed")
	}
	if cmd.Flags().Changed("no-monitor-all") {
		t.Error("Changed(\"no-monitor-all\") should be false when the flag is not passed")
	}
}
