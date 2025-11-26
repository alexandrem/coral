package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
)

var (
	// Service name must be alphanumeric with hyphens.
	serviceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9\-]*$`)

	// Health path must start with / and contain valid path characters.
	healthPathRegex = regexp.MustCompile(`^/[a-zA-Z0-9\-\_\./]*$`)

	// Service type must be alphanumeric.
	serviceTypeRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*$`)
)

// ServiceSpec represents a parsed service specification.
type ServiceSpec struct {
	Name           string
	Port           int32
	HealthEndpoint string
	ServiceType    string
	Labels         map[string]string
}

// ParseServiceSpec parses a service specification string.
// Format: name:port[:health][:type]
// Examples:
//   - api:8080
//   - frontend:3000:/health
//   - redis:6379::redis
//   - metrics:9090:/metrics:prometheus
func ParseServiceSpec(spec string) (*ServiceSpec, error) {
	if spec == "" {
		return nil, fmt.Errorf("service spec cannot be empty")
	}

	// Split by colon to get components
	parts := strings.Split(spec, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid service spec format: must be name:port[:health][:type]")
	}

	// Parse name
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return nil, fmt.Errorf("service name cannot be empty")
	}
	if !serviceNameRegex.MatchString(name) {
		return nil, fmt.Errorf("invalid service name '%s': must be alphanumeric with hyphens", name)
	}

	// Parse port
	portStr := strings.TrimSpace(parts[1])
	if portStr == "" {
		return nil, fmt.Errorf("service port cannot be empty")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port '%s': must be a number", portStr)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
	}

	result := &ServiceSpec{
		Name:   name,
		Port:   int32(port),
		Labels: make(map[string]string),
	}

	// Parse optional health endpoint (part 2)
	if len(parts) > 2 && parts[2] != "" {
		health := strings.TrimSpace(parts[2])
		if !strings.HasPrefix(health, "/") {
			return nil, fmt.Errorf("invalid health endpoint '%s': must start with /", health)
		}
		if !healthPathRegex.MatchString(health) {
			return nil, fmt.Errorf("invalid health endpoint '%s': contains invalid characters", health)
		}
		result.HealthEndpoint = health
	}

	// Parse optional service type (part 3)
	if len(parts) > 3 && parts[3] != "" {
		serviceType := strings.TrimSpace(parts[3])
		if !serviceTypeRegex.MatchString(serviceType) {
			return nil, fmt.Errorf("invalid service type '%s': must be alphanumeric", serviceType)
		}
		result.ServiceType = serviceType
	}

	// Error on too many parts
	if len(parts) > 4 {
		return nil, fmt.Errorf("invalid service spec format: too many components (expected name:port[:health][:type])")
	}

	return result, nil
}

// ToProto converts a ServiceSpec to the protobuf ServiceInfo message.
func (s *ServiceSpec) ToProto() *meshv1.ServiceInfo {
	return &meshv1.ServiceInfo{
		Name:           s.Name,
		Port:           s.Port,
		HealthEndpoint: s.HealthEndpoint,
		ServiceType:    s.ServiceType,
		Labels:         s.Labels,
	}
}

// ParseMultipleServiceSpecs parses multiple service specifications.
func ParseMultipleServiceSpecs(specs []string) ([]*ServiceSpec, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("at least one service spec is required")
	}

	results := make([]*ServiceSpec, 0, len(specs))
	seenNames := make(map[string]bool)

	for i, spec := range specs {
		parsed, err := ParseServiceSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("invalid service spec at position %d (%s): %w", i+1, spec, err)
		}

		// Check for duplicate service names
		if seenNames[parsed.Name] {
			return nil, fmt.Errorf("duplicate service name '%s' at position %d", parsed.Name, i+1)
		}
		seenNames[parsed.Name] = true

		results = append(results, parsed)
	}

	return results, nil
}

// ValidateServiceSpecs performs additional validation on a list of service specs.
func ValidateServiceSpecs(specs []*ServiceSpec) error {
	if len(specs) == 0 {
		return fmt.Errorf("at least one service is required")
	}

	// Check for port collisions (warning only, not an error)
	seenPorts := make(map[int32][]string)
	for _, spec := range specs {
		seenPorts[spec.Port] = append(seenPorts[spec.Port], spec.Name)
	}

	// Note: We don't error on port collisions as they may be intentional
	// (e.g., different containers in the same pod)

	return nil
}
