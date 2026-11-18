package startup

import (
	"os"
	"path/filepath"
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

// TestBackupDBFiles verifies that a database and its WAL are copied together
// and that an existing backup is never overwritten.
func TestBackupDBFiles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "metrics.duckdb")
	dst := src + ".corrupt"

	if err := os.WriteFile(src, []byte("db"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src+".wal", []byte("wal"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := backupDBFiles(src, dst); err != nil {
		t.Fatalf("backupDBFiles() error = %v", err)
	}
	for path, want := range map[string]string{dst: "db", dst + ".wal": "wal"} {
		got, err := os.ReadFile(path) // #nosec G304 -- test file.
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}

	if err := backupDBFiles(src, dst); err == nil {
		t.Error("backupDBFiles() over an existing backup succeeded, want error")
	}
}

// TestBackupDBFilesWithoutWAL verifies that a missing WAL is not an error.
func TestBackupDBFilesWithoutWAL(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "metrics.duckdb")
	if err := os.WriteFile(src, []byte("db"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := backupDBFiles(src, src+".corrupt"); err != nil {
		t.Fatalf("backupDBFiles() error = %v", err)
	}
	if _, err := os.Stat(src + ".corrupt.wal"); !os.IsNotExist(err) {
		t.Errorf("WAL backup exists without a source WAL (err = %v)", err)
	}
}
