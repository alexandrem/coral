// Package beyla provides integration with Beyla for eBPF-based observability.
package beyla

// beylaConfig represents Beyla's YAML configuration structure.
type beylaConfig struct {
	LogLevel  string `yaml:"log_level,omitempty"`
	Discovery struct {
		Instrument []struct {
			OpenPorts string `yaml:"open_ports,omitempty"`
			ExeName   string `yaml:"exe_name,omitempty"`
		} `yaml:"instrument,omitempty"`
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
