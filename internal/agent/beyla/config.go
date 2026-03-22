// Package beyla provides integration with Beyla for eBPF-based observability.
package beyla

// beylaConfig represents Beyla's YAML configuration structure.
type beylaConfig struct {
	LogLevel           string `yaml:"log_level,omitempty"`
	ContextPropagation string `yaml:"context_propagation,omitempty"`
	Discovery          struct {
		ExcludePorts    string `yaml:"exclude_ports,omitempty"`
		ExcludeServices []struct {
			Exe string `yaml:"exe,omitempty"`
		} `yaml:"exclude_services,omitempty"`
		Services []struct {
			OpenPorts      string `yaml:"open_ports,omitempty"`
			ExecutableName string `yaml:"executable_name,omitempty"`
			Name           string `yaml:"name,omitempty"`
		} `yaml:"services,omitempty"`
	} `yaml:"discovery,omitempty"`
	Attributes struct {
		Kubernetes struct {
			Enable string `yaml:"enable,omitempty"`
		} `yaml:"kubernetes,omitempty"`
	} `yaml:"attributes,omitempty"`
	OtelTracesExport *struct {
		Endpoint string `yaml:"endpoint,omitempty"`
		Protocol string `yaml:"protocol,omitempty"`
	} `yaml:"otel_traces_export,omitempty"`
	OtelMetricsExport *struct {
		Endpoint string `yaml:"endpoint,omitempty"`
		Protocol string `yaml:"protocol,omitempty"`
	} `yaml:"otel_metrics_export,omitempty"`
	Routes *struct {
		Unmatch string `yaml:"unmatch,omitempty"`
	} `yaml:"routes,omitempty"`
}
