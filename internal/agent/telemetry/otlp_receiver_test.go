package telemetry

import (
	"testing"

	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
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
