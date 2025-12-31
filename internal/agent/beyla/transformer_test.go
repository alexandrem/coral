package beyla

import (
	"testing"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestNewTransformer(t *testing.T) {
	logger := zerolog.Nop()
	transformer := NewTransformer(logger)

	if transformer == nil {
		t.Fatal("NewTransformer() returned nil")
	}
}

func TestSpanKindToString(t *testing.T) {
	tests := []struct {
		name string
		kind ptrace.SpanKind
		want string
	}{
		{
			name: "unspecified",
			kind: ptrace.SpanKindUnspecified,
			want: "unspecified",
		},
		{
			name: "internal",
			kind: ptrace.SpanKindInternal,
			want: "internal",
		},
		{
			name: "server",
			kind: ptrace.SpanKindServer,
			want: "server",
		},
		{
			name: "client",
			kind: ptrace.SpanKindClient,
			want: "client",
		},
		{
			name: "producer",
			kind: ptrace.SpanKindProducer,
			want: "producer",
		},
		{
			name: "consumer",
			kind: ptrace.SpanKindConsumer,
			want: "consumer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := spanKindToString(tt.kind)
			if got != tt.want {
				t.Errorf("spanKindToString(%v) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

func TestGetStringAttribute(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutStr("key1", "value1")
	attrs.PutStr("key2", "value2")
	attrs.PutStr("empty", "")

	tests := []struct {
		name         string
		key          string
		defaultValue string
		want         string
	}{
		{
			name:         "existing key",
			key:          "key1",
			defaultValue: "default",
			want:         "value1",
		},
		{
			name:         "another existing key",
			key:          "key2",
			defaultValue: "default",
			want:         "value2",
		},
		{
			name:         "empty value",
			key:          "empty",
			defaultValue: "default",
			want:         "",
		},
		{
			name:         "non-existent key",
			key:          "nonexistent",
			defaultValue: "default",
			want:         "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getStringAttribute(attrs, tt.key, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getStringAttribute(%q, %q) = %q, want %q",
					tt.key, tt.defaultValue, got, tt.want)
			}
		})
	}
}

func TestGetIntAttribute(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutInt("status_code", 200)
	attrs.PutInt("count", 42)
	attrs.PutInt("zero", 0)

	tests := []struct {
		name         string
		key          string
		defaultValue int64
		want         int64
	}{
		{
			name:         "existing status code",
			key:          "status_code",
			defaultValue: 500,
			want:         200,
		},
		{
			name:         "existing count",
			key:          "count",
			defaultValue: 0,
			want:         42,
		},
		{
			name:         "zero value",
			key:          "zero",
			defaultValue: 999,
			want:         0,
		},
		{
			name:         "non-existent key",
			key:          "nonexistent",
			defaultValue: 404,
			want:         404,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getIntAttribute(attrs, tt.key, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getIntAttribute(%q, %d) = %d, want %d",
					tt.key, tt.defaultValue, got, tt.want)
			}
		})
	}
}

func TestAttributesToMap(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func() pcommon.Map
		wantLen   int
		wantKeys  []string
	}{
		{
			name: "empty attributes",
			setupFunc: func() pcommon.Map {
				return pcommon.NewMap()
			},
			wantLen: 0,
		},
		{
			name: "string attributes",
			setupFunc: func() pcommon.Map {
				attrs := pcommon.NewMap()
				attrs.PutStr("http.method", "GET")
				attrs.PutStr("http.route", "/api/users")
				return attrs
			},
			wantLen:  2,
			wantKeys: []string{"http.method", "http.route"},
		},
		{
			name: "mixed type attributes",
			setupFunc: func() pcommon.Map {
				attrs := pcommon.NewMap()
				attrs.PutStr("service.name", "api-server")
				attrs.PutInt("http.status_code", 200)
				attrs.PutBool("is_error", false)
				attrs.PutDouble("latency", 123.45)
				return attrs
			},
			wantLen:  4,
			wantKeys: []string{"service.name", "http.status_code", "is_error", "latency"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := tt.setupFunc()
			result := attributesToMap(attrs)

			if len(result) != tt.wantLen {
				t.Errorf("attributesToMap() returned %d attributes, want %d", len(result), tt.wantLen)
			}

			// Verify expected keys exist
			for _, key := range tt.wantKeys {
				if _, ok := result[key]; !ok {
					t.Errorf("attributesToMap() missing expected key %q", key)
				}
			}

			// All values should be strings (result is map[string]string)
			for k, v := range result {
				if v == "" && tt.name != "empty attributes" {
					// Just verify it's a string (empty is valid)
					_ = k
					_ = v
				}
			}
		})
	}
}

func TestExtractStatusCode(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func() pcommon.Map
		want      uint32
	}{
		{
			name: "HTTP response status code",
			setupFunc: func() pcommon.Map {
				attrs := pcommon.NewMap()
				attrs.PutInt("http.response.status_code", 200)
				return attrs
			},
			want: 200,
		},
		{
			name: "HTTP status code (legacy)",
			setupFunc: func() pcommon.Map {
				attrs := pcommon.NewMap()
				attrs.PutInt("http.status_code", 404)
				return attrs
			},
			want: 404,
		},
		{
			name: "gRPC status code",
			setupFunc: func() pcommon.Map {
				attrs := pcommon.NewMap()
				attrs.PutInt("rpc.grpc.status_code", 0)
				return attrs
			},
			want: 0,
		},
		{
			name: "HTTP response takes precedence over legacy",
			setupFunc: func() pcommon.Map {
				attrs := pcommon.NewMap()
				attrs.PutInt("http.response.status_code", 200)
				attrs.PutInt("http.status_code", 404)
				return attrs
			},
			want: 200,
		},
		{
			name: "no status code",
			setupFunc: func() pcommon.Map {
				attrs := pcommon.NewMap()
				attrs.PutStr("some.other.attribute", "value")
				return attrs
			},
			want: 0,
		},
		{
			name: "server error",
			setupFunc: func() pcommon.Map {
				attrs := pcommon.NewMap()
				attrs.PutInt("http.response.status_code", 500)
				return attrs
			},
			want: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := tt.setupFunc()
			got := extractStatusCode(attrs)
			if got != tt.want {
				t.Errorf("extractStatusCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBeylaConfig(t *testing.T) {
	// Test that beylaConfig struct can be created and fields are accessible
	config := beylaConfig{
		LogLevel: "info",
	}

	config.Discovery.Instrument = []struct {
		OpenPorts string `yaml:"open_ports,omitempty"`
		ExeName   string `yaml:"exe_name,omitempty"`
	}{
		{
			OpenPorts: "8080-9090",
			ExeName:   "my-app",
		},
	}

	config.Attributes.Kubernetes.Enable = "true"

	if config.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", config.LogLevel, "info")
	}

	if len(config.Discovery.Instrument) != 1 {
		t.Errorf("Instrument count = %d, want 1", len(config.Discovery.Instrument))
	}

	if config.Discovery.Instrument[0].OpenPorts != "8080-9090" {
		t.Errorf("OpenPorts = %q, want %q", config.Discovery.Instrument[0].OpenPorts, "8080-9090")
	}

	if config.Attributes.Kubernetes.Enable != "true" {
		t.Errorf("Kubernetes.Enable = %q, want %q", config.Attributes.Kubernetes.Enable, "true")
	}
}

func TestBeylaConfigWithExports(t *testing.T) {
	config := beylaConfig{}

	// Test OTEL traces export
	config.OtelTracesExport = &struct {
		Endpoint string `yaml:"endpoint,omitempty"`
		Protocol string `yaml:"protocol,omitempty"`
	}{
		Endpoint: "localhost:4317",
		Protocol: "grpc",
	}

	// Test OTEL metrics export
	config.OtelMetricsExport = &struct {
		Endpoint string `yaml:"endpoint,omitempty"`
		Protocol string `yaml:"protocol,omitempty"`
	}{
		Endpoint: "localhost:4318",
		Protocol: "http/protobuf",
	}

	// Test routes config
	config.Routes = &struct {
		Unmatch string `yaml:"unmatch,omitempty"`
	}{
		Unmatch: "wildcard",
	}

	if config.OtelTracesExport == nil {
		t.Fatal("OtelTracesExport should not be nil")
	}

	if config.OtelTracesExport.Endpoint != "localhost:4317" {
		t.Errorf("OtelTracesExport.Endpoint = %q, want %q",
			config.OtelTracesExport.Endpoint, "localhost:4317")
	}

	if config.OtelMetricsExport == nil {
		t.Fatal("OtelMetricsExport should not be nil")
	}

	if config.OtelMetricsExport.Protocol != "http/protobuf" {
		t.Errorf("OtelMetricsExport.Protocol = %q, want %q",
			config.OtelMetricsExport.Protocol, "http/protobuf")
	}

	if config.Routes == nil {
		t.Fatal("Routes should not be nil")
	}

	if config.Routes.Unmatch != "wildcard" {
		t.Errorf("Routes.Unmatch = %q, want %q", config.Routes.Unmatch, "wildcard")
	}
}
