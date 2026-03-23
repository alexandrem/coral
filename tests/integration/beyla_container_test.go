package integration

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/coral-mesh/coral/internal/agent/beyla"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"gopkg.in/yaml.v3"
)

// TestBeylaConfigValidation verifies that the YAML configuration we generate
// is considered valid by the actual Beyla binary (v2.7.8).
func TestBeylaConfigValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping testcontainers test in short mode")
	}

	ctx := context.Background()

	// 1. Create a representative BeylaConfig following the pattern in manager.go.
	cfg := beyla.BeylaConfig{
		LogLevel:           "INFO",
		ContextPropagation: "all",
	}
	cfg.Discovery.ExcludePorts = "4317,4318,4320,9000,9001"
	cfg.Discovery.ExcludeServices = []struct {
		ExePath string `yaml:"exe_path,omitempty"`
	}{
		{ExePath: ".*coral-agent.*"},
	}

	cfg.Discovery.Services = []struct {
		OpenPorts string `yaml:"open_ports,omitempty"`
		ExePath   string `yaml:"exe_path,omitempty"`
		Name      string `yaml:"name,omitempty"`
	}{
		{OpenPorts: "8080", Name: "cpu-app"},
		{OpenPorts: "8090", Name: "otel-app"},
		{OpenPorts: "1-65535"}, // Catch-all rule
	}

	// Add routes and export config (required fields).
	cfg.OtelTracesExport = &struct {
		Endpoint string `yaml:"endpoint,omitempty"`
		Protocol string `yaml:"protocol,omitempty"`
	}{Endpoint: "http://127.0.0.1:4320", Protocol: "http/protobuf"}

	cfg.OtelMetricsExport = &struct {
		Endpoint string `yaml:"endpoint,omitempty"`
		Protocol string `yaml:"protocol,omitempty"`
	}{Endpoint: "http://127.0.0.1:4320", Protocol: "http/protobuf"}

	cfg.Routes = &struct {
		Unmatch string `yaml:"unmatch,omitempty"`
	}{Unmatch: "wildcard"}

	data, err := yaml.Marshal(&cfg)
	require.NoError(t, err)
	t.Logf("Generated Beyla YAML:\n%s", string(data))

	// 2. Write to temp file for mounting into container.
	tmpFile, err := os.CreateTemp("", "beyla-valid-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.Write(data)
	require.NoError(t, err)
	tmpFile.Close()

	// 3. Start Beyla container to validate parsing.
	req := testcontainers.ContainerRequest{
		Image: "grafana/beyla:2.7.8",
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      tmpFile.Name(),
				ContainerFilePath: "/config.yaml",
			},
		},
		Cmd:        []string{"--config", "/config.yaml"},
		WaitingFor: wait.ForLog("Grafana Beyla").WithStartupTimeout(15 * time.Second),
	}

	beylaC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})

	// Beyla will likely fail to start eBPF probes without host PIDs/Privileges,
	// but it will ALWAYS log "wrong Beyla configuration" FIRST if the YAML is invalid.
	if beylaC != nil {
		defer func() {
			_ = beylaC.Terminate(ctx)
		}()

		// Give it a moment to parse and fail if invalid.
		time.Sleep(2 * time.Second)

		logs, err := beylaC.Logs(ctx)
		require.NoError(t, err)
		allLogs, err := io.ReadAll(logs)
		require.NoError(t, err)

		logStr := string(allLogs)
		t.Logf("Beyla logs:\n%s", logStr)

		// Assert that we don't see the configuration error.
		require.NotContains(t, logStr, "wrong Beyla configuration", "Beyla rejected the generated YAML config")
	} else {
		// If container failed to start, check if it was due to config error.
		require.NoError(t, err, "Beyla container failed to start - likely due to invalid YAML")
	}
}
