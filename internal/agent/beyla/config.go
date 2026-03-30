// Package beyla provides integration with Beyla for eBPF-based observability.
package beyla

// ExcludeService defines a process exclusion rule.
type ExcludeService struct {
	ExePath string `yaml:"exe_path,omitempty"`
}

// InstrumentRule defines a process discovery/instrumentation rule.
type InstrumentRule struct {
	OpenPorts string `yaml:"open_ports,omitempty"`
	ExePath   string `yaml:"exe_path,omitempty"`
	Name      string `yaml:"name,omitempty"`
}

// BeylaConfig represents Beyla's YAML configuration structure.
type BeylaConfig struct {
	LogLevel string `yaml:"log_level,omitempty"`
	Ebpf     struct {
		ContextPropagation string `yaml:"context_propagation,omitempty"`
	} `yaml:"ebpf,omitempty"`
	Discovery struct {
		ExcludePorts      string           `yaml:"exclude_ports,omitempty"`
		ExcludeServices   []ExcludeService `yaml:"exclude_services,omitempty"`
		Services          []InstrumentRule `yaml:"services,omitempty"`
		NetworkInterfaces []string         `yaml:"network_interfaces,omitempty"`
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
