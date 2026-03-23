// Package beyla provides integration with Beyla for eBPF-based observability.
package beyla

// BeylaConfig represents Beyla's YAML configuration structure.
type BeylaConfig struct {
	LogLevel           string `yaml:"log_level,omitempty"`
	ContextPropagation string `yaml:"context_propagation,omitempty"`
	Discovery          struct {
		ExcludePorts    string `yaml:"exclude_ports,omitempty"`
		ExcludeServices []struct {
			ExePath string `yaml:"exe_path,omitempty"`
		} `yaml:"exclude_services,omitempty"`
		Services []struct {
			OpenPorts string `yaml:"open_ports,omitempty"`
			ExePath   string `yaml:"exe_path,omitempty"`
			Name      string `yaml:"name,omitempty"`
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
