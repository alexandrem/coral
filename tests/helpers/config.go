package helpers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

// ConfigBuilder helps build test configurations.
type ConfigBuilder struct {
	t       require.TestingT
	tempDir string
	configs map[string]string
}

// NewConfigBuilder creates a new config builder.
func NewConfigBuilder(t require.TestingT, tempDir string) *ConfigBuilder {
	return &ConfigBuilder{
		t:       t,
		tempDir: tempDir,
		configs: make(map[string]string),
	}
}

// WriteColonyConfig writes a colony configuration file.
func (cb *ConfigBuilder) WriteColonyConfig(name string, apiPort, grpcPort int) string {
	config := fmt.Sprintf(`# Colony Configuration
name: %s
api:
  host: 127.0.0.1
  port: %d

grpc:
  host: 127.0.0.1
  port: %d

database:
  path: %s

log:
  level: debug
  format: json
`,
		name,
		apiPort,
		grpcPort,
		filepath.Join(cb.tempDir, fmt.Sprintf("%s.db", name)),
	)

	configPath := filepath.Join(cb.tempDir, fmt.Sprintf("%s-colony.yaml", name))
	err := os.WriteFile(configPath, []byte(config), 0644)
	require.NoError(cb.t, err, "Failed to write colony config")

	cb.configs[name] = configPath
	return configPath
}

// WriteAgentConfig writes an agent configuration file.
func (cb *ConfigBuilder) WriteAgentConfig(name, colonyAddr string, grpcPort int) string {
	config := fmt.Sprintf(`# Agent Configuration
name: %s

colony:
  address: %s

grpc:
  host: 127.0.0.1
  port: %d

log:
  level: debug
  format: json

ebpf:
  enabled: false
`,
		name,
		colonyAddr,
		grpcPort,
	)

	configPath := filepath.Join(cb.tempDir, fmt.Sprintf("%s-agent.yaml", name))
	err := os.WriteFile(configPath, []byte(config), 0644)
	require.NoError(cb.t, err, "Failed to write agent config")

	cb.configs[name] = configPath
	return configPath
}

// WriteDiscoveryConfig writes a discovery service configuration file.
func (cb *ConfigBuilder) WriteDiscoveryConfig(name string, grpcPort, stunPort int) string {
	config := fmt.Sprintf(`# Discovery Service Configuration
name: %s

grpc:
  host: 127.0.0.1
  port: %d

stun:
  host: 127.0.0.1
  port: %d

registry:
  ttl: 30s
  cleanup_interval: 10s

relay:
  enabled: true
  port_range_start: 50000
  port_range_end: 50100

log:
  level: debug
  format: json
`,
		name,
		grpcPort,
		stunPort,
	)

	configPath := filepath.Join(cb.tempDir, fmt.Sprintf("%s-discovery.yaml", name))
	err := os.WriteFile(configPath, []byte(config), 0644)
	require.NoError(cb.t, err, "Failed to write discovery config")

	cb.configs[name] = configPath
	return configPath
}

// GetConfig returns a previously written config path.
func (cb *ConfigBuilder) GetConfig(name string) string {
	return cb.configs[name]
}

// Cleanup removes all created config files.
func (cb *ConfigBuilder) Cleanup() {
	for _, path := range cb.configs {
		_ = os.Remove(path)
	}
	cb.configs = make(map[string]string)
}
