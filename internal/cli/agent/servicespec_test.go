package agent

import (
	"testing"
)

func TestParseServiceSpec(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    *ServiceSpec
		wantErr bool
	}{
		{
			name: "simple service with port",
			spec: "api:8080",
			want: &ServiceSpec{
				Name:   "api",
				Port:   8080,
				Labels: map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "service with health endpoint",
			spec: "frontend:3000:/health",
			want: &ServiceSpec{
				Name:           "frontend",
				Port:           3000,
				HealthEndpoint: "/health",
				Labels:         map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "service with type only",
			spec: "redis:6379::redis",
			want: &ServiceSpec{
				Name:        "redis",
				Port:        6379,
				ServiceType: "redis",
				Labels:      map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "service with health and type",
			spec: "metrics:9090:/metrics:prometheus",
			want: &ServiceSpec{
				Name:           "metrics",
				Port:           9090,
				HealthEndpoint: "/metrics",
				ServiceType:    "prometheus",
				Labels:         map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "service with complex health path",
			spec: "app:8080:/api/v1/health:http",
			want: &ServiceSpec{
				Name:           "app",
				Port:           8080,
				HealthEndpoint: "/api/v1/health",
				ServiceType:    "http",
				Labels:         map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "service with hyphenated name",
			spec: "payment-api:8080",
			want: &ServiceSpec{
				Name:   "payment-api",
				Port:   8080,
				Labels: map[string]string{},
			},
			wantErr: false,
		},
		{
			name:    "empty spec",
			spec:    "",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "missing port",
			spec:    "api",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "missing name",
			spec:    ":8080",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid port - not a number",
			spec:    "api:port",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid port - out of range (too low)",
			spec:    "api:0",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid port - out of range (too high)",
			spec:    "api:99999",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid service name with space",
			spec:    "my service:8080",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid service name starting with hyphen",
			spec:    "-api:8080",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid health endpoint - no leading slash",
			spec:    "api:8080:health",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid service type with special characters",
			spec:    "api:8080::/health-check",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "too many components",
			spec:    "api:8080:/health:http:extra",
			want:    nil,
			wantErr: true,
		},
		{
			name: "valid port at minimum",
			spec: "api:1",
			want: &ServiceSpec{
				Name:   "api",
				Port:   1,
				Labels: map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "valid port at maximum",
			spec: "api:65535",
			want: &ServiceSpec{
				Name:   "api",
				Port:   65535,
				Labels: map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "health endpoint with dots",
			spec: "api:8080:/health.check",
			want: &ServiceSpec{
				Name:           "api",
				Port:           8080,
				HealthEndpoint: "/health.check",
				Labels:         map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "health endpoint with underscores",
			spec: "api:8080:/health_check",
			want: &ServiceSpec{
				Name:           "api",
				Port:           8080,
				HealthEndpoint: "/health_check",
				Labels:         map[string]string{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseServiceSpec(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseServiceSpec() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			if got.Name != tt.want.Name {
				t.Errorf("Name = %v, want %v", got.Name, tt.want.Name)
			}
			if got.Port != tt.want.Port {
				t.Errorf("Port = %v, want %v", got.Port, tt.want.Port)
			}
			if got.HealthEndpoint != tt.want.HealthEndpoint {
				t.Errorf("HealthEndpoint = %v, want %v", got.HealthEndpoint, tt.want.HealthEndpoint)
			}
			if got.ServiceType != tt.want.ServiceType {
				t.Errorf("ServiceType = %v, want %v", got.ServiceType, tt.want.ServiceType)
			}
		})
	}
}

func TestParseMultipleServiceSpecs(t *testing.T) {
	tests := []struct {
		name    string
		specs   []string
		want    int // number of specs expected
		wantErr bool
	}{
		{
			name:    "single service",
			specs:   []string{"api:8080"},
			want:    1,
			wantErr: false,
		},
		{
			name:    "multiple services",
			specs:   []string{"frontend:3000", "redis:6379", "metrics:9090"},
			want:    3,
			wantErr: false,
		},
		{
			name:    "empty list",
			specs:   []string{},
			want:    0,
			wantErr: true,
		},
		{
			name:    "duplicate service names",
			specs:   []string{"api:8080", "api:8081"},
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid service in list",
			specs:   []string{"api:8080", "invalid", "redis:6379"},
			want:    0,
			wantErr: true,
		},
		{
			name:    "same port different services",
			specs:   []string{"api:8080", "frontend:8080"},
			want:    2,
			wantErr: false, // Port collision is allowed (different containers)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMultipleServiceSpecs(tt.specs)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMultipleServiceSpecs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			if len(got) != tt.want {
				t.Errorf("ParseMultipleServiceSpecs() got %d specs, want %d", len(got), tt.want)
			}
		})
	}
}

func TestServiceSpecToProto(t *testing.T) {
	spec := &ServiceSpec{
		Name:           "api",
		Port:           8080,
		HealthEndpoint: "/health",
		ServiceType:    "http",
		Labels: map[string]string{
			"team": "platform",
		},
	}

	proto := spec.ToProto()

	if proto.ComponentName != spec.Name {
		t.Errorf("ComponentName = %v, want %v", proto.ComponentName, spec.Name)
	}
	if proto.Port != spec.Port {
		t.Errorf("Port = %v, want %v", proto.Port, spec.Port)
	}
	if proto.HealthEndpoint != spec.HealthEndpoint {
		t.Errorf("HealthEndpoint = %v, want %v", proto.HealthEndpoint, spec.HealthEndpoint)
	}
	if proto.ServiceType != spec.ServiceType {
		t.Errorf("ServiceType = %v, want %v", proto.ServiceType, spec.ServiceType)
	}
	if proto.Labels["team"] != "platform" {
		t.Errorf("Labels[team] = %v, want platform", proto.Labels["team"])
	}
}

func TestValidateServiceSpecs(t *testing.T) {
	tests := []struct {
		name    string
		specs   []*ServiceSpec
		wantErr bool
	}{
		{
			name: "valid single service",
			specs: []*ServiceSpec{
				{Name: "api", Port: 8080, Labels: make(map[string]string)},
			},
			wantErr: false,
		},
		{
			name: "valid multiple services",
			specs: []*ServiceSpec{
				{Name: "api", Port: 8080, Labels: make(map[string]string)},
				{Name: "frontend", Port: 3000, Labels: make(map[string]string)},
			},
			wantErr: false,
		},
		{
			name:    "empty list",
			specs:   []*ServiceSpec{},
			wantErr: true,
		},
		{
			name:    "nil list",
			specs:   nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateServiceSpecs(tt.specs)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateServiceSpecs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
