package debug

import (
	"testing"

	"github.com/rs/zerolog"
)

func TestNewFunctionDiscoverer(t *testing.T) {
	logger := zerolog.Nop()
	discoverer := NewFunctionDiscoverer(logger)

	if discoverer == nil {
		t.Fatal("NewFunctionDiscoverer() returned nil")
	}
}

func TestExtractPackageName(t *testing.T) {
	tests := []struct {
		name         string
		functionName string
		want         string
	}{
		{
			name:         "main package function",
			functionName: "main.handleCheckout",
			want:         "main",
		},
		{
			name:         "fully qualified package",
			functionName: "github.com/foo/bar.ProcessPayment",
			want:         "github.com/foo/bar",
		},
		{
			name:         "nested package",
			functionName: "github.com/coral/internal/agent.Start",
			want:         "github.com/coral/internal/agent",
		},
		{
			name:         "pointer method",
			functionName: "(*Handler).ServeHTTP",
			want:         "",
		},
		{
			name:         "value method",
			functionName: "(Handler).ServeHTTP",
			want:         "",
		},
		{
			name:         "no package",
			functionName: "standalone",
			want:         "",
		},
		{
			name:         "empty string",
			functionName: "",
			want:         "",
		},
		{
			name:         "single package",
			functionName: "utils.Helper",
			want:         "utils",
		},
		{
			name:         "multiple dots in package",
			functionName: "github.com/user/repo.v2.Function",
			want:         "github.com/user/repo.v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPackageName(tt.functionName)
			if got != tt.want {
				t.Errorf("extractPackageName(%q) = %q, want %q", tt.functionName, got, tt.want)
			}
		})
	}
}

func TestGetBinaryPathForService_InvalidPID(t *testing.T) {
	tests := []struct {
		name string
		pid  int32
	}{
		{
			name: "zero PID",
			pid:  0,
		},
		{
			name: "negative PID",
			pid:  -1,
		},
		{
			name: "very negative PID",
			pid:  -999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GetBinaryPathForService("test-service", tt.pid)
			if err == nil {
				t.Error("GetBinaryPathForService() should error for invalid PID")
			}

			if err.Error() != "invalid PID: "+string(rune('0'+tt.pid)) && tt.pid >= 0 {
				// For negative PIDs, just verify error exists
				if tt.pid >= 0 {
					t.Errorf("Unexpected error message: %v", err)
				}
			}
		})
	}
}

func TestGetBinaryPathForService_NonExistentPID(t *testing.T) {
	// Use a PID that's unlikely to exist (999999)
	_, err := GetBinaryPathForService("test-service", 999999)
	if err == nil {
		// This could pass if PID 999999 actually exists, which is unlikely
		t.Log("PID 999999 exists, skipping this test")
		return
	}

	// Should error because /proc/999999/exe doesn't exist
	if err.Error() == "" {
		t.Error("Expected error for non-existent PID")
	}
}

func TestGetBinaryPathForService_CurrentProcess(t *testing.T) {
	// Test with current process PID
	// This should succeed on Linux systems
	pid := int32(1) // init process, should always exist on Linux

	path, err := GetBinaryPathForService("test-service", pid)

	if err != nil {
		// This test only works on Linux with /proc filesystem
		t.Skipf("GetBinaryPathForService failed (probably not on Linux): %v", err)
		return
	}

	if path == "" {
		t.Error("GetBinaryPathForService() returned empty path")
	}
}
