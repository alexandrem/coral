package telemetry

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlpmetrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlpresource "go.opentelemetry.io/proto/otlp/resource/v1"
)

func resourceWithAttrs(attrs map[string]*otlpcommon.AnyValue) *otlpresource.Resource {
	resource := &otlpresource.Resource{}
	for k, v := range attrs {
		resource.Attributes = append(resource.Attributes, &otlpcommon.KeyValue{Key: k, Value: v})
	}
	return resource
}

func TestExtractProcessPID_NilResource(t *testing.T) {
	if pid := extractProcessPID(nil); pid != 0 {
		t.Errorf("expected 0 for nil resource, got %d", pid)
	}
}

func TestExtractProcessPID_MissingAttribute(t *testing.T) {
	resource := resourceWithAttrs(map[string]*otlpcommon.AnyValue{
		"service.name": {Value: &otlpcommon.AnyValue_StringValue{StringValue: "checkout-svc"}},
	})

	if pid := extractProcessPID(resource); pid != 0 {
		t.Errorf("expected 0 when process.pid is absent, got %d", pid)
	}
}

func TestExtractProcessPID_IntValue(t *testing.T) {
	resource := resourceWithAttrs(map[string]*otlpcommon.AnyValue{
		"process.pid": {Value: &otlpcommon.AnyValue_IntValue{IntValue: 1234}},
	})

	if pid := extractProcessPID(resource); pid != 1234 {
		t.Errorf("expected pid 1234, got %d", pid)
	}
}

func TestExtractProcessPID_StringValue(t *testing.T) {
	resource := resourceWithAttrs(map[string]*otlpcommon.AnyValue{
		"process.pid": {Value: &otlpcommon.AnyValue_StringValue{StringValue: "5678"}},
	})

	if pid := extractProcessPID(resource); pid != 5678 {
		t.Errorf("expected pid 5678, got %d", pid)
	}
}

func TestExtractProcessPID_InvalidValue(t *testing.T) {
	resource := resourceWithAttrs(map[string]*otlpcommon.AnyValue{
		"process.pid": {Value: &otlpcommon.AnyValue_StringValue{StringValue: "not-a-number"}},
	})

	if pid := extractProcessPID(resource); pid != 0 {
		t.Errorf("expected 0 for unparseable pid, got %d", pid)
	}
}

func TestDrainMetrics_ReturnsEachBatchOnce(t *testing.T) {
	r, err := NewOTLPReceiver(Config{}, nil, zerolog.Nop())
	if err != nil {
		t.Fatalf("NewOTLPReceiver: %v", err)
	}
	wrapper := &metricsServiceWrapper{receiver: r}

	export := func(name string) {
		t.Helper()
		req := &otlpmetricsv1.ExportMetricsServiceRequest{
			ResourceMetrics: []*otlpmetrics.ResourceMetrics{{
				ScopeMetrics: []*otlpmetrics.ScopeMetrics{{
					Metrics: []*otlpmetrics.Metric{{Name: name}},
				}},
			}},
		}
		if _, err := wrapper.Export(context.Background(), req); err != nil {
			t.Fatalf("Export: %v", err)
		}
	}

	export("first")
	export("second")

	if got := r.DrainMetrics(); len(got) != 2 {
		t.Fatalf("first drain: expected 2 batches, got %d", len(got))
	}
	if got := r.DrainMetrics(); len(got) != 0 {
		t.Fatalf("second drain: expected buffer to be empty, got %d batches", len(got))
	}

	// Batches exported after a drain must be returned by the next drain only.
	export("third")
	got := r.DrainMetrics()
	if len(got) != 1 {
		t.Fatalf("third drain: expected 1 batch, got %d", len(got))
	}
	name := got[0].ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Name()
	if name != "third" {
		t.Errorf("expected metric %q, got %q", "third", name)
	}
}
