package startup

import (
	"testing"

	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/logging"
)

// TestStorageManagerMonitorAllStartsBeyla verifies that, with no services
// configured, Beyla is initialized when Agent.MonitorAll is true (the new
// default) and left nil when it is false (RFD 103).
func TestStorageManagerMonitorAllStartsBeyla(t *testing.T) {
	tests := []struct {
		name       string
		monitorAll bool
		wantBeyla  bool
	}{
		{name: "monitor-all enabled starts Beyla", monitorAll: true, wantBeyla: true},
		{name: "monitor-all disabled with no services skips Beyla", monitorAll: false, wantBeyla: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			agentCfg := config.DefaultAgentConfig()
			agentCfg.Agent.MonitorAll = tt.monitorAll

			logger := logging.NewWithComponent(logging.Config{Level: "error"}, "test")
			mgr := NewStorageManager(logger, agentCfg, nil, "test-agent")

			result, err := mgr.Initialize()
			if err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}
			defer func() {
				if result.SharedDB != nil {
					_ = result.SharedDB.Close()
				}
			}()

			gotBeyla := result.BeylaConfig != nil
			if gotBeyla != tt.wantBeyla {
				t.Errorf("BeylaConfig present = %v, want %v", gotBeyla, tt.wantBeyla)
			}
			if gotBeyla && result.BeylaConfig.MonitorAll != tt.monitorAll {
				t.Errorf("BeylaConfig.MonitorAll = %v, want %v", result.BeylaConfig.MonitorAll, tt.monitorAll)
			}
		})
	}
}
