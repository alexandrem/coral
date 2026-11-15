package discovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeEnviron writes a null-delimited /proc/<pid>/environ file from a list
// of "KEY=VALUE" entries.
func writeEnviron(t *testing.T, root string, pid int, entries ...string) {
	t.Helper()
	content := ""
	for _, e := range entries {
		content += e + "\x00"
	}
	writeProcFile(t, root, fmt.Sprintf("%d/environ", pid), content)
}

func TestEnvVarProviderPresent(t *testing.T) {
	root := t.TempDir()
	writeEnviron(t, root, 100, "PATH=/usr/bin", "OTEL_SERVICE_NAME=otel-app", "HOME=/root")

	p := &EnvVarProvider{procRoot: root}
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	c := findCandidate(t, candidates, 100)
	if c.Name != "otel-app" {
		t.Errorf("Name = %q, want %q", c.Name, "otel-app")
	}
	if len(c.Ports) != 0 {
		t.Errorf("Ports = %v, want empty (EnvVarProvider reports name hints only)", c.Ports)
	}
}

func TestEnvVarProviderFallsBackToServiceName(t *testing.T) {
	root := t.TempDir()
	writeEnviron(t, root, 100, "PATH=/usr/bin", "SERVICE_NAME=legacy-worker")

	p := &EnvVarProvider{procRoot: root}
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	c := findCandidate(t, candidates, 100)
	if c.Name != "legacy-worker" {
		t.Errorf("Name = %q, want %q (SERVICE_NAME fallback)", c.Name, "legacy-worker")
	}
}

func TestEnvVarProviderPrefersOtelOverServiceName(t *testing.T) {
	root := t.TempDir()
	writeEnviron(t, root, 100, "SERVICE_NAME=legacy-worker", "OTEL_SERVICE_NAME=otel-app")

	p := &EnvVarProvider{procRoot: root}
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	c := findCandidate(t, candidates, 100)
	if c.Name != "otel-app" {
		t.Errorf("Name = %q, want %q (OTEL_SERVICE_NAME takes priority)", c.Name, "otel-app")
	}
}

func TestEnvVarProviderAbsent(t *testing.T) {
	root := t.TempDir()
	writeEnviron(t, root, 100, "PATH=/usr/bin", "HOME=/root")

	p := &EnvVarProvider{procRoot: root}
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	for _, c := range candidates {
		if c.PID == 100 {
			t.Errorf("expected no candidate for PID 100 (no service name env var set), got %+v", c)
		}
	}
}

func TestEnvVarProviderMalformedEnviron(t *testing.T) {
	root := t.TempDir()

	// A malformed entry with no '=' should be skipped without error, and a
	// valid entry elsewhere in the same file should still be found.
	pidDir := filepath.Join(root, "100")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("failed to create pid dir: %v", err)
	}
	content := "malformed_no_equals_sign\x00OTEL_SERVICE_NAME=otel-app\x00"
	if err := os.WriteFile(filepath.Join(pidDir, "environ"), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write environ: %v", err)
	}

	p := &EnvVarProvider{procRoot: root}
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v, want graceful skip of malformed entry", err)
	}

	c := findCandidate(t, candidates, 100)
	if c.Name != "otel-app" {
		t.Errorf("Name = %q, want %q despite malformed entry earlier in the file", c.Name, "otel-app")
	}
}

func TestEnvVarProviderProcessDisappearsBetweenScanAndRead(t *testing.T) {
	root := t.TempDir()

	// PID 300's directory exists (so it's listed) but has no environ file,
	// simulating the process exiting between the PID listing and the
	// environ read.
	if err := os.MkdirAll(filepath.Join(root, "300"), 0o755); err != nil {
		t.Fatalf("failed to create pid dir: %v", err)
	}

	// A healthy process should still be reported.
	writeEnviron(t, root, 100, "OTEL_SERVICE_NAME=otel-app")

	p := &EnvVarProvider{procRoot: root}
	candidates, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v, want graceful skip of exited process", err)
	}

	for _, c := range candidates {
		if c.PID == 300 {
			t.Errorf("expected PID 300 (exited, no environ file) to be skipped, got %+v", c)
		}
	}
	findCandidate(t, candidates, 100) // still present
}

func TestEnvVarProviderProbeAlwaysTrue(t *testing.T) {
	p := NewEnvVarProvider()
	if !p.Probe() {
		t.Error("Probe() = false, want true")
	}
	if p.Name() != "envvar" {
		t.Errorf("Name() = %q, want %q", p.Name(), "envvar")
	}
}
